package subconv

import (
	"encoding/base64"
	"encoding/json"
	"net"
	"net/url"
	"strings"

	"gopkg.in/yaml.v3"
)

// looksLikeClash reports whether a subscription blob is a Clash/mihomo YAML
// config rather than a base64 link list. Every Clash node config carries a
// top-level "proxies:" sequence; a base64 blob or link list never does.
func looksLikeClash(blob string) bool {
	var doc clashDoc
	if err := yaml.Unmarshal([]byte(blob), &doc); err != nil {
		return false
	}
	return len(doc.Proxies) > 0
}

type clashDoc struct {
	Proxies []map[string]any `yaml:"proxies"`
}

// ParseClashYAML extracts proxies from a Clash/mihomo YAML config, rebuilding a
// canonical share link for each so the result flows through the same ParseLink
// path as base64 subscriptions. Unsupported proxy types are skipped.
func ParseClashYAML(blob string) []*Proxy {
	var doc clashDoc
	if err := yaml.Unmarshal([]byte(blob), &doc); err != nil {
		return nil
	}
	out := make([]*Proxy, 0, len(doc.Proxies))
	for _, m := range doc.Proxies {
		link := clashToLink(m)
		if link == "" {
			continue
		}
		if p, err := ParseLink(link); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// clashToLink converts a single Clash proxy map into a canonical share URI.
// Returns "" for unsupported / malformed entries.
func clashToLink(m map[string]any) string {
	server := str(m["server"])
	port := str(m["port"])
	if server == "" || port == "" {
		return ""
	}
	// Same IPv6 bracketing requirement as singbox.BuildShareLink: an imported
	// Clash node with server 2001:db8::1 produced an unparseable URI, so those
	// nodes were dropped on re-export instead of round-tripping.
	addr := net.JoinHostPort(server, port)
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
			path, host := clashWSOpts(m)
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
			if v := alpnStr(m["alpn"]); v != "" {
				j["alpn"] = v
			}
		}
		if strOr(str(m["network"]), "tcp") == "ws" {
			path, host := clashWSOpts(m)
			j["path"] = path
			j["host"] = host
		}
		b, _ := json.Marshal(j)
		return "vmess://" + base64.StdEncoding.EncodeToString(b)

	case "ss":
		userinfo := base64.RawURLEncoding.EncodeToString([]byte(str(m["cipher"]) + ":" + str(m["password"])))
		return "ss://" + userinfo + "@" + addr + frag

	case "trojan":
		q := url.Values{}
		if v := str(m["sni"]); v != "" {
			q.Set("sni", v)
		}
		if cbool(m["skip-cert-verify"]) {
			q.Set("allowInsecure", "1")
		}
		return "trojan://" + str(m["password"]) + "@" + addr + qstr(q) + frag

	// hysteria (v1) is deliberately NOT folded in here: it is a different wire
	// protocol with different fields (auth-str, up/down), so rendering it as a
	// hysteria2:// link produced "hysteria2://@host:443" — v1 has no `password`
	// key, so the credential came out empty. That node then shipped in every
	// user's subscription and could never connect. Dropping it is the honest
	// outcome; emitting a broken node is worse than emitting none.
	case "hysteria2":
		q := url.Values{}
		if v := str(m["sni"]); v != "" {
			q.Set("sni", v)
		}
		if cbool(m["skip-cert-verify"]) {
			q.Set("insecure", "1")
		}
		return "hysteria2://" + str(m["password"]) + "@" + addr + qstr(q) + frag

	case "tuic":
		q := url.Values{}
		if v := str(m["sni"]); v != "" {
			q.Set("sni", v)
		}
		if v := alpnStr(m["alpn"]); v != "" {
			q.Set("alpn", v)
		}
		if v := str(m["congestion-controller"]); v != "" {
			q.Set("congestion_control", v)
		}
		if cbool(m["skip-cert-verify"]) {
			q.Set("allow_insecure", "1")
		}
		return "tuic://" + str(m["uuid"]) + ":" + str(m["password"]) + "@" + addr + qstr(q) + frag
	}
	return ""
}

func clashWSOpts(m map[string]any) (path, host string) {
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

func alpnStr(v any) string {
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

func cbool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		return x == "true" || x == "1"
	}
	return false
}

func strOr(v, fallback string) string {
	if v == "" {
		return fallback
	}
	return v
}

func qstr(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}
