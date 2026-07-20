package subconv

import (
	"strconv"
	"strings"
)

func itoaPort(p int) string { return strconv.Itoa(p) }

// Surge renders a Surge 5 managed config. Surge has no VLESS/Reality or TUIC
// support, so those nodes are skipped — for a VLESS-heavy subscription the
// output is intentionally sparse. subURL, if set, becomes the MANAGED-CONFIG
// auto-update header.
func Surge(proxies []*Proxy, subURL string) string {
	var kept []*Proxy
	for _, p := range proxies {
		if surgeProxy(p) != "" {
			p.Name = surgeName(p.Name)
			kept = append(kept, p)
		}
	}
	dedupeNames(kept)

	var b strings.Builder
	if subURL != "" {
		b.WriteString("#!MANAGED-CONFIG " + subURL + " interval=43200 strict=false\n\n")
	}
	b.WriteString("[Proxy]\n")
	for _, p := range kept {
		b.WriteString(p.Name + " = " + surgeProxy(p) + "\n")
	}

	ag := buildAutoGroups(kept)
	b.WriteString("\n[Proxy Group]\n")
	if len(ag.all) == 0 {
		b.WriteString(grpSelectClash + " = select, DIRECT\n")
	} else {
		sel := []string{grpAutoClash}
		for _, g := range ag.byGroup {
			sel = append(sel, g.name)
		}
		sel = append(sel, ag.all...)
		sel = append(sel, "DIRECT")
		b.WriteString(grpSelectClash + " = select, " + strings.Join(sel, ", ") + "\n")
		b.WriteString(grpAutoClash + " = url-test, " + strings.Join(ag.all, ", ") +
			", url = http://www.gstatic.com/generate_204, interval = 300\n")
		for _, g := range ag.byGroup {
			b.WriteString(g.name + " = url-test, " + strings.Join(g.names, ", ") +
				", url = http://www.gstatic.com/generate_204, interval = 300\n")
		}
	}

	b.WriteString("\n[Rule]\n")
	b.WriteString("DOMAIN-SET,https://raw.githubusercontent.com/Loyalsoldier/surge-rules/release/reject.txt,REJECT\n")
	b.WriteString("GEOIP,CN,DIRECT\n")
	b.WriteString("FINAL," + grpSelectClash + ",dns-failed\n")
	return b.String()
}

// surgeName strips characters Surge treats as delimiters in proxy declarations.
//
// Newlines matter as much as the delimiters: the Surge renderer builds its
// output by hand (the Clash/sing-box ones go through yaml/json.Marshal, which
// escapes for us), and a node name is url-decoded out of the #fragment — so a
// remark carrying %0A would end the proxy line early and let the rest of the
// name become its own directive, e.g. a FINAL rule rewriting the user's routing.
// A leading # or ; would comment the whole line out instead, silently dropping a
// node the proxy groups still reference. Interior # is left alone: dedupeNames
// legitimately appends " #2" suffixes.
func surgeName(s string) string {
	s = strings.NewReplacer(",", " ", "=", " ", "\n", " ", "\r", " ").Replace(s)
	s = strings.TrimLeft(strings.TrimSpace(s), "#;")
	if s = strings.TrimSpace(s); s == "" {
		s = "node"
	}
	return s
}

// surgeProxy returns the right-hand side of a Surge proxy line, or "" if Surge
// can't express this protocol.
func surgeProxy(p *Proxy) string {
	switch p.Protocol {
	case "ss":
		parts := []string{"ss", p.Server, itoaPort(p.Port),
			"encrypt-method=" + p.Method, "password=" + p.Password, "udp-relay=true"}
		return strings.Join(parts, ", ")
	case "trojan":
		parts := []string{"trojan", p.Server, itoaPort(p.Port), "password=" + p.Password}
		if v := p.param("sni", "peer"); v != "" {
			parts = append(parts, "sni="+v)
		}
		if p.tlsInsecure() {
			parts = append(parts, "skip-cert-verify=true")
		}
		parts = append(parts, "udp-relay=true")
		return strings.Join(parts, ", ")
	case "vmess":
		parts := []string{"vmess", p.Server, itoaPort(p.Port), "username=" + p.UUID}
		if str(p.VMess["net"]) == "ws" {
			parts = append(parts, "ws=true")
			if path := str(p.VMess["path"]); path != "" {
				parts = append(parts, "ws-path="+path)
			}
			if host := str(p.VMess["host"]); host != "" {
				parts = append(parts, "ws-headers=Host:"+host)
			}
		}
		if str(p.VMess["tls"]) == "tls" {
			parts = append(parts, "tls=true")
			if sni := str(p.VMess["sni"]); sni != "" {
				parts = append(parts, "sni="+sni)
			}
			// Matches the trojan/hysteria2 branches; vmess was the only TLS
			// protocol here with no way to accept a self-signed certificate.
			if p.tlsInsecure() {
				parts = append(parts, "skip-cert-verify=true")
			}
		}
		return strings.Join(parts, ", ")
	case "hysteria2":
		parts := []string{"hysteria2", p.Server, itoaPort(p.Port), "password=" + p.Password}
		if v := p.param("sni"); v != "" {
			parts = append(parts, "sni="+v)
		}
		if p.tlsInsecure() {
			parts = append(parts, "skip-cert-verify=true")
		}
		return strings.Join(parts, ", ")
	case "anytls":
		// Surge iOS 5.17.0+ / Mac 6.4.3+. Older builds reject the line.
		parts := []string{"anytls", p.Server, itoaPort(p.Port), "password=" + p.Password}
		if v := p.param("sni"); v != "" {
			parts = append(parts, "sni="+v)
		}
		if p.tlsInsecure() {
			parts = append(parts, "skip-cert-verify=true")
		}
		return strings.Join(parts, ", ")
	default:
		// vless / tuic / hysteria v1 have no Surge equivalent — Surge's proxy
		// policy list covers hysteria2 but not v1.
		return ""
	}
}
