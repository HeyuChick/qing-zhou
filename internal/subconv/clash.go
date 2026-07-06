package subconv

import (
	"net"
	"strings"

	"gopkg.in/yaml.v3"
)

// Clash renders a mihomo/Clash YAML config, merging the anti-leak template
// (dns/tun/sniffer/rules) with the generated proxies and a "Proxy" selector.
func Clash(proxies []*Proxy, template string) (string, error) {
	if strings.TrimSpace(template) == "" {
		template = DefaultClashTemplate
	}
	doc := map[string]any{}
	if err := yaml.Unmarshal([]byte(template), &doc); err != nil {
		doc = map[string]any{}
	}

	// Convert first, then dedupe the kept set so url-test groups reference
	// unique, real node names (clashProxy drops unsupported protocols).
	type conv struct {
		m map[string]any
		p *Proxy
	}
	var cs []conv
	for _, p := range proxies {
		if m := clashProxy(p); m != nil {
			cs = append(cs, conv{m: m, p: p})
		}
	}
	kept := make([]*Proxy, len(cs))
	for i, c := range cs {
		kept[i] = c.p
	}
	dedupeNames(kept)
	list := make([]map[string]any, len(cs))
	for i, c := range cs {
		c.m["name"] = c.p.Name // sync after dedupe
		list[i] = c.m
	}
	doc["proxies"] = list
	doc["proxy-groups"] = clashGroups(buildAutoGroups(kept))
	// Always append the final catch-all referencing the ACTUAL primary group, so
	// the rule can never drift from the group name (templates must NOT hardcode
	// it — a stale "MATCH,<old name>" yields "proxy not found" in the client).
	rules, _ := doc["rules"].([]interface{})
	rules = append(rules, "MATCH,"+grpSelectClash)
	doc["rules"] = rules

	// Inject proxy server IPs into tun.route-exclude-address to prevent TUN
	// routing loop. These IPs are server-side data (the nodes we define), not
	// client-side config — the server knows its own node addresses.
	injectRouteExclude(doc, proxies)

	b, err := yaml.Marshal(doc)
	return string(b), err
}

// clashGroups builds the proxy-groups: a primary selector to pick manually, a
// global "auto" url-test, and one auto-select url-test per panel node-group
// (named after the group, so the client mirrors the admin's organisation).
func clashGroups(ag autoGroups) []map[string]any {
	if len(ag.all) == 0 {
		return []map[string]any{{
			"name": grpSelectClash, "type": "select", "proxies": []string{"DIRECT"},
		}}
	}
	sel := []string{grpAutoClash}
	for _, g := range ag.byGroup {
		sel = append(sel, g.name)
	}
	sel = append(sel, ag.all...)
	sel = append(sel, "DIRECT")

	groups := []map[string]any{
		{"name": grpSelectClash, "type": "select", "proxies": sel},
		clashURLTest(grpAutoClash, ag.all),
	}
	for _, g := range ag.byGroup {
		groups = append(groups, clashURLTest(g.name, g.names))
	}
	return groups
}

func clashURLTest(name string, proxies []string) map[string]any {
	return map[string]any{
		"name": name, "type": "url-test", "url": urltestURL,
		"interval": 300, "tolerance": 50, "proxies": proxies,
	}
}

func clashProxy(p *Proxy) map[string]any {
	m := map[string]any{"name": p.Name, "server": p.Server, "port": p.Port}
	switch p.Protocol {
	case "vless":
		m["type"] = "vless"
		m["uuid"] = p.UUID
		m["udp"] = true
		network := p.param("type")
		if network == "" {
			network = "tcp"
		}
		m["network"] = network
		sec := p.param("security")
		if sec == "tls" || sec == "reality" {
			m["tls"] = true
		}
		if v := p.param("sni"); v != "" {
			m["servername"] = v
		}
		if v := p.param("flow"); v != "" {
			m["flow"] = v
		}
		if v := p.param("fp"); v != "" {
			m["client-fingerprint"] = v
		}
		if sec == "reality" {
			ro := map[string]any{}
			if v := p.param("pbk"); v != "" {
				ro["public-key"] = v
			}
			if v := p.param("sid"); v != "" {
				ro["short-id"] = v
			}
			m["reality-opts"] = ro
		}
		clashWS(m, p, network, p.param("path"), p.param("host"))
		if p.param("allowInsecure", "insecure") == "1" {
			m["skip-cert-verify"] = true
		}
	case "vmess":
		m["type"] = "vmess"
		m["uuid"] = p.UUID
		m["alterId"] = p.AlterID
		m["cipher"] = "auto"
		m["udp"] = true
		network := str(p.VMess["net"])
		if network == "" {
			network = "tcp"
		}
		m["network"] = network
		if str(p.VMess["tls"]) == "tls" {
			m["tls"] = true
			if sni := str(p.VMess["sni"]); sni != "" {
				m["servername"] = sni
			}
		}
		clashWS(m, p, network, str(p.VMess["path"]), str(p.VMess["host"]))
	case "ss":
		m["type"] = "ss"
		m["cipher"] = p.Method
		m["password"] = p.Password
		m["udp"] = true
	case "trojan":
		m["type"] = "trojan"
		m["password"] = p.Password
		m["udp"] = true
		if v := p.param("sni", "peer"); v != "" {
			m["sni"] = v
		}
		if p.param("allowInsecure", "insecure") == "1" {
			m["skip-cert-verify"] = true
		}
	case "hysteria2":
		m["type"] = "hysteria2"
		m["password"] = p.Password
		if v := p.param("sni"); v != "" {
			m["sni"] = v
		}
		if p.param("insecure", "allowInsecure") == "1" {
			m["skip-cert-verify"] = true
		}
	case "tuic":
		m["type"] = "tuic"
		m["uuid"] = p.UUID
		m["password"] = p.Password
		if v := p.param("sni"); v != "" {
			m["sni"] = v
		}
		if v := p.param("alpn"); v != "" {
			m["alpn"] = strings.Split(v, ",")
		}
		if v := p.param("congestion_control", "congestion-controller"); v != "" {
			m["congestion-controller"] = v
		}
		if p.param("allow_insecure", "insecure", "allowInsecure") == "1" {
			m["skip-cert-verify"] = true
		}
	default:
		return nil
	}
	return m
}

func clashWS(m map[string]any, p *Proxy, network, path, host string) {
	if network != "ws" {
		return
	}
	wo := map[string]any{}
	if path != "" {
		wo["path"] = path
	}
	if host != "" {
		wo["headers"] = map[string]any{"Host": host}
	}
	m["ws-opts"] = wo
}

// injectRouteExclude collects unique server IPs from all proxies and merges
// them into tun.route-exclude-address with CIDR format. This prevents TUN
// routing loops where the client's connection to the proxy server gets
// captured by TUN. The IPs come from the server's own node definitions.
func injectRouteExclude(doc map[string]any, proxies []*Proxy) {
	seen := map[string]bool{}
	for _, p := range proxies {
		if p.Server != "" && !isPrivateIP(p.Server) {
			seen[p.Server] = true
		}
	}
	if len(seen) == 0 {
		return
	}
	tun, ok := doc["tun"].(map[string]any)
	if !ok {
		return
	}
	var existing []string
	if list, ok := tun["route-exclude-address"].([]any); ok {
		for _, v := range list {
			if s, ok := v.(string); ok {
				existing = append(existing, s)
			}
		}
	}
	for ip := range seen {
		cidr := ensureCIDR(ip)
		dup := false
		for _, e := range existing {
			if e == cidr {
				dup = true
				break
			}
		}
		if !dup {
			existing = append(existing, cidr)
		}
	}
	tun["route-exclude-address"] = existing
}

func isPrivateIP(ip string) bool {
	parsed := net.ParseIP(ip)
	return parsed != nil && parsed.IsPrivate()
}

// ensureCIDR appends "/32" (IPv4) or "/128" (IPv6) to bare IP addresses.
// mihomo's route-exclude-address requires CIDR format.
func ensureCIDR(ip string) string {
	if strings.Contains(ip, "/") {
		return ip
	}
	if net.ParseIP(ip) == nil {
		return ip
	}
	if strings.Contains(ip, ":") {
		return ip + "/128"
	}
	return ip + "/32"
}
