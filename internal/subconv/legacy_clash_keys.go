package subconv

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
)

// Frozen pre-WS-fidelity Clash import projection for preference compatibility.
// Only changed producer formats are represented. These lossy URIs are never
// published; their keys become candidates that the store must match against
// this source's actual pre-refresh cache before trusting any alias.
func legacyClashToLink(m map[string]any) string {
	server := str(m["server"])
	port := str(m["port"])
	if server == "" || port == "" {
		return ""
	}
	// Same bracketing rules as singbox.joinHostPort — see the reasoning there.
	// Kept as a local copy rather than shared: it is five lines, and reaching
	// across into the singbox package for it would invert the dependency.
	//
	// The already-bracketed case is the one that matters most on this path:
	// Clash YAML in the wild carries both `server: 2001:db8::1` and
	// `server: "[2001:db8::1]"`, and a naive JoinHostPort turns the second into
	// [[…]] and drops the node.
	addr := joinHostPort(server, port)
	frag := ""
	if name := str(m["name"]); name != "" {
		frag = "#" + url.QueryEscape(name)
	}

	switch str(m["type"]) {
	case "vless":
		q := url.Values{}
		net := strOr(str(m["network"]), "tcp")
		q.Set("type", net)
		sec := ""
		if cbool(m["tls"]) {
			sec = "tls"
		}
		if ro, ok := m["reality-opts"].(map[string]any); ok {
			sec = "reality"
			if v := str(ro["public-key"]); v != "" {
				q.Set("pbk", v)
			}
			if v := str(ro["short-id"]); v != "" {
				q.Set("sid", v)
			}
		}
		if sec != "" {
			q.Set("security", sec)
		}
		if v := str(m["servername"]); v != "" {
			q.Set("sni", v)
		}
		if v := str(m["flow"]); v != "" {
			q.Set("flow", v)
		}
		if v := str(m["client-fingerprint"]); v != "" {
			q.Set("fp", v)
		}
		if net == "ws" {
			path, host := legacyClashWSOpts(m)
			if path != "" {
				q.Set("path", path)
			}
			if host != "" {
				q.Set("host", host)
			}
		}
		if cbool(m["skip-cert-verify"]) {
			q.Set("allowInsecure", "1")
		}
		return "vless://" + str(m["uuid"]) + "@" + addr + qstr(q) + frag

	case "vmess":
		j := map[string]any{
			"v":    "2",
			"ps":   str(m["name"]),
			"add":  server,
			"port": port,
			"id":   str(m["uuid"]),
			"aid":  strOr(str(m["alterId"]), "0"),
			"net":  strOr(str(m["network"]), "tcp"),
			"type": "none",
		}
		if cbool(m["tls"]) {
			j["tls"] = "tls"
			if v := str(m["servername"]); v != "" {
				j["sni"] = v
			}
			// The mirror of the render-side gap: vmess stopped at servername here
			// while vless and trojan above already carried skip-cert-verify across.
			// Importing a Clash subscription with a self-signed vmess node dropped
			// the exemption at parse time, so no amount of fixing the renderers
			// could bring it back on re-export.
			if cbool(m["skip-cert-verify"]) {
				j["allowInsecure"] = "1"
			}
			if v := str(m["client-fingerprint"]); v != "" {
				j["fp"] = v
			}
			if v := legacyClashALPNStr(m["alpn"]); v != "" {
				j["alpn"] = v
			}
		}
		if strOr(str(m["network"]), "tcp") == "ws" {
			path, host := legacyClashWSOpts(m)
			j["path"] = path
			j["host"] = host
		}
		b, _ := json.Marshal(j)
		return "vmess://" + base64.StdEncoding.EncodeToString(b)

	case "trojan":
		q := url.Values{}
		if v := str(m["sni"]); v != "" {
			q.Set("sni", v)
		}
		if cbool(m["skip-cert-verify"]) {
			q.Set("allowInsecure", "1")
		}
		return "trojan://" + str(m["password"]) + "@" + addr + qstr(q) + frag

	}
	return ""
}

func legacyClashWSOpts(m map[string]any) (path, host string) {
	wo, ok := m["ws-opts"].(map[string]any)
	if !ok {
		return "", ""
	}
	path = str(wo["path"])
	if h, ok := wo["headers"].(map[string]any); ok {
		host = strOr(str(h["Host"]), str(h["host"]))
	}
	return path, host
}

func legacyClashALPNStr(v any) string {
	switch x := v.(type) {
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			if s := str(e); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ",")
	case string:
		return x
	}
	return ""
}
