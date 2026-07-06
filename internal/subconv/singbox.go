package subconv

import (
	"encoding/json"
	"strings"
)

// Singbox renders a sing-box JSON config: a "proxy" selector over the parsed
// outbounds, a direct outbound, default tun+mixed inbounds, and the anti-leak
// dns/route template merged in. Best-effort for the official sing-box client.
func Singbox(proxies []*Proxy, template string) (string, error) {
	if strings.TrimSpace(template) == "" {
		template = DefaultSingboxTemplate
	}
	doc := map[string]any{}
	_ = json.Unmarshal([]byte(template), &doc)

	// Convert first, then dedupe so url-test/selector tags are unique and real.
	type conv struct {
		o map[string]any
		p *Proxy
	}
	var cs []conv
	for _, p := range proxies {
		if o := singboxOutbound(p); o != nil {
			cs = append(cs, conv{o: o, p: p})
		}
	}
	kept := make([]*Proxy, len(cs))
	for i, c := range cs {
		kept[i] = c.p
	}
	dedupeNames(kept)
	outs := make([]map[string]any, len(cs))
	for i, c := range cs {
		c.o["tag"] = c.p.Name // sync after dedupe
		outs[i] = c.o
	}
	ag := buildAutoGroups(kept)

	// Primary selector: manual pick across auto + per-group auto-select + nodes.
	sel := []string{}
	if len(ag.all) > 0 {
		sel = append(sel, tagAuto)
	}
	for _, g := range ag.byGroup {
		sel = append(sel, g.name)
	}
	sel = append(sel, ag.all...)
	sel = append(sel, "direct")

	all := []map[string]any{{"type": "selector", "tag": tagProxy, "outbounds": sel}}
	if len(ag.all) > 0 {
		all = append(all, singboxURLTest(tagAuto, ag.all))
		for _, g := range ag.byGroup {
			all = append(all, singboxURLTest(g.name, g.names))
		}
	}
	all = append(all, outs...)
	all = append(all, map[string]any{"type": "direct", "tag": "direct"})
	doc["outbounds"] = all

	if _, ok := doc["inbounds"]; !ok {
		doc["inbounds"] = []map[string]any{
			{"type": "tun", "tag": "tun-in", "address": []string{"172.19.0.1/30"}, "auto_route": true, "strict_route": false, "stack": "gvisor"},
			{"type": "mixed", "tag": "mixed-in", "listen": "127.0.0.1", "listen_port": 2080},
		}
	}

	// Inject proxy server IPs into tun exclude_address to prevent routing loop.
	injectSingboxTunExclude(doc, proxies)

	b, err := json.MarshalIndent(doc, "", "  ")
	return string(b), err
}

func singboxURLTest(tag string, outbounds []string) map[string]any {
	return map[string]any{
		"type": "urltest", "tag": tag, "outbounds": outbounds,
		"url": "https://www.gstatic.com/generate_204", "interval": "3m", "tolerance": 50,
	}
}

func singboxOutbound(p *Proxy) map[string]any {
	o := map[string]any{"tag": p.Name, "server": p.Server, "server_port": p.Port}
	switch p.Protocol {
	case "vless":
		o["type"] = "vless"
		o["uuid"] = p.UUID
		if v := p.param("flow"); v != "" {
			o["flow"] = v
		}
		if net := p.param("type"); net == "ws" {
			o["transport"] = sbWS(p.param("path"), p.param("host"))
		}
		o["tls"] = sbTLS(p, p.param("security"))
	case "vmess":
		o["type"] = "vmess"
		o["uuid"] = p.UUID
		o["alter_id"] = p.AlterID
		o["security"] = "auto"
		if str(p.VMess["net"]) == "ws" {
			o["transport"] = sbWS(str(p.VMess["path"]), str(p.VMess["host"]))
		}
		if str(p.VMess["tls"]) == "tls" {
			tls := map[string]any{"enabled": true}
			if sni := str(p.VMess["sni"]); sni != "" {
				tls["server_name"] = sni
			}
			o["tls"] = tls
		}
	case "ss":
		o["type"] = "shadowsocks"
		o["method"] = p.Method
		o["password"] = p.Password
	case "trojan":
		o["type"] = "trojan"
		o["password"] = p.Password
		o["tls"] = sbTLS(p, "tls")
	case "hysteria2":
		o["type"] = "hysteria2"
		o["password"] = p.Password
		o["tls"] = sbTLS(p, "tls")
	case "tuic":
		o["type"] = "tuic"
		o["uuid"] = p.UUID
		o["password"] = p.Password
		if v := p.param("congestion_control", "congestion-controller"); v != "" {
			o["congestion_control"] = v
		}
		o["tls"] = sbTLS(p, "tls")
	default:
		return nil
	}
	return o
}

func sbTLS(p *Proxy, sec string) map[string]any {
	if sec != "tls" && sec != "reality" {
		return map[string]any{"enabled": false}
	}
	tls := map[string]any{"enabled": true}
	if sni := p.param("sni"); sni != "" {
		tls["server_name"] = sni
	}
	if alpn := p.param("alpn"); alpn != "" {
		tls["alpn"] = strings.Split(alpn, ",")
	}
	if fp := p.param("fp"); fp != "" {
		tls["utls"] = map[string]any{"enabled": true, "fingerprint": fp}
	}
	if sec == "reality" {
		re := map[string]any{"enabled": true}
		if v := p.param("pbk"); v != "" {
			re["public_key"] = v
		}
		if v := p.param("sid"); v != "" {
			re["short_id"] = v
		}
		tls["reality"] = re
	}
	if p.param("insecure", "allowInsecure", "allow_insecure") == "1" {
		tls["insecure"] = true
	}
	return tls
}

func sbWS(path, host string) map[string]any {
	t := map[string]any{"type": "ws"}
	if path != "" {
		t["path"] = path
	}
	if host != "" {
		t["headers"] = map[string]any{"Host": host}
	}
	return t
}

// injectSingboxTunExclude adds proxy server IPs to the TUN inbound's
// exclude_address to prevent routing loops on the client.
func injectSingboxTunExclude(doc map[string]any, proxies []*Proxy) {
	seen := map[string]bool{}
	for _, p := range proxies {
		if p.Server != "" && !isPrivateIP(p.Server) {
			seen[p.Server] = true
		}
	}
	if len(seen) == 0 {
		return
	}
	inbounds, ok := doc["inbounds"].([]map[string]any)
	if !ok {
		return
	}
	for _, ib := range inbounds {
		if ib["type"] != "tun" {
			continue
		}
		existing, _ := ib["exclude_address"].([]string)
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
		ib["exclude_address"] = existing
	}
}
