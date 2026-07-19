package subconv

import (
	"encoding/json"
	"fmt"
	"net"
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

	// Inject proxy server IPs into tun route_exclude_address to prevent routing loop.
	injectSingboxTunExclude(doc, proxies)
	// Domain-based node servers: keep them out of fake-ip and off the proxy
	// detour, mirroring the Clash injectNodeDomains fix (otherwise "TUN on → no
	// network" for any node whose server is a domain).
	injectSingboxDomains(doc, proxies)

	b, err := json.MarshalIndent(doc, "", "  ")
	return string(b), err
}

// mapSlice normalizes a JSON array value to []map[string]any. A template loaded
// via json.Unmarshal yields []any (of map[string]any); the Go-built default
// yields []map[string]any. Element maps are shared, so mutating them in place
// affects the underlying document either way.
func mapSlice(v any) []map[string]any {
	switch s := v.(type) {
	case []map[string]any:
		return s
	case []any:
		out := make([]map[string]any, 0, len(s))
		for _, e := range s {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

// prependRule inserts rule at the front of a dns/route rules array, preserving
// the array's concrete element type (which differs between a Go-built default
// and a json.Unmarshal'd custom template).
func prependRule(v any, rule map[string]any) any {
	switch s := v.(type) {
	case []map[string]any:
		return append([]map[string]any{rule}, s...)
	case []any:
		return append([]any{any(rule)}, s...)
	case nil:
		return []map[string]any{rule}
	default:
		return v
	}
}

// injectSingboxDomains makes the client resolve each domain-based node server via
// the direct "local" DNS (never fake-ip or the proxy-detoured "remote" server)
// and route the connection to that server directly. Without this, resolving a
// foreign node domain either yields a 198.18.x fake-ip dialed into the node's own
// TUN, or deadlocks bootstrapping DNS through the very proxy being established.
func injectSingboxDomains(doc map[string]any, proxies []*Proxy) {
	seen := map[string]bool{}
	for _, p := range proxies {
		if p.Server != "" && net.ParseIP(p.Server) == nil {
			seen[p.Server] = true
		}
	}
	if len(seen) == 0 {
		return
	}
	domains := sortedKeys(seen)
	if dns, ok := doc["dns"].(map[string]any); ok {
		dns["rules"] = prependRule(dns["rules"], map[string]any{"domain": domains, "server": "local"})
	}
	if route, ok := doc["route"].(map[string]any); ok {
		route["rules"] = prependRule(route["rules"], map[string]any{"domain": domains, "outbound": "direct"})
	}
}

func singboxURLTest(tag string, outbounds []string) map[string]any {
	return map[string]any{
		"type": "urltest", "tag": tag, "outbounds": outbounds,
		"url": "https://www.gstatic.com/generate_204", "interval": "3m", "tolerance": 50,
	}
}

// SingboxOutboundFromLink parses a share link and renders it as a sing-box
// outbound (dial-side) object. Used to build relay upstream outbounds that dial
// a landing inbound. Returns an error if the link is unparseable or its protocol
// has no outbound renderer.
func SingboxOutboundFromLink(link string) (map[string]any, error) {
	p, err := ParseLink(link)
	if err != nil {
		return nil, err
	}
	o := singboxOutbound(p)
	if o == nil {
		return nil, fmt.Errorf("subconv: protocol %q has no sing-box outbound renderer", p.Protocol)
	}
	return o, nil
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
		if t := sbTransport(p); t != nil {
			o["transport"] = t
		}
		// UDP over VLESS requires an agreed packet encoding. Leaving it unset
		// makes each client fall back to its own default, which breaks UDP
		// (and so QUIC downloads) while leaving TCP intact.
		//
		// Whitelisted, not passed through: these params come from links an admin
		// pasted or a remote subscription served, and sing-box rejects an
		// unknown packet_encoding by failing to load the *whole* config — one
		// bad node would take down every subscriber's profile.
		switch p.param("packetEncoding", "packet_encoding") {
		case "packetaddr":
			o["packet_encoding"] = "packetaddr"
		default:
			o["packet_encoding"] = "xudp"
		}
		o["tls"] = sbTLS(p, p.param("security"))
	case "vmess":
		o["type"] = "vmess"
		o["uuid"] = p.UUID
		o["alter_id"] = p.AlterID
		o["security"] = "auto"
		if str(p.VMess["net"]) == "ws" {
			o["transport"] = sbWS(str(p.VMess["path"]), str(p.VMess["host"]), 0, "")
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
		// trojan carries the same transports as vless; it had none at all here,
		// so a ws/grpc trojan node was dialled as plain TCP and never connected.
		if t := sbTransport(p); t != nil {
			o["transport"] = t
		}
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
		if p.param("zero_rtt", "reduce_rtt") == "1" {
			o["zero_rtt_handshake"] = true
		}
		o["tls"] = sbTLS(p, "tls")
	default:
		return nil
	}
	// TCP/multiplex dial tuning (vless/vmess/trojan). multiplex is incompatible
	// with vless xtls-rprx-vision, so drop it when a flow is set.
	switch p.Protocol {
	case "vless", "vmess", "trojan":
		t := p.tuning()
		_, hasFlow := o["flow"]
		sbApplyTuning(o, t, hasFlow)
	}
	return o
}

// sbApplyTuning sets sing-box's tcp_fast_open/tcp_multi_path/multiplex outbound
// options from t. mux is suppressed when suppressMux (a vless vision flow).
func sbApplyTuning(o map[string]any, t tuning, suppressMux bool) {
	if t.tfo {
		o["tcp_fast_open"] = true
	}
	if t.mptcp {
		o["tcp_multi_path"] = true
	}
	if t.mux && !suppressMux {
		mx := map[string]any{"enabled": true}
		if t.brutalUp > 0 && t.brutalDown > 0 {
			mx["brutal"] = map[string]any{"enabled": true, "up_mbps": t.brutalUp, "down_mbps": t.brutalDown}
		}
		o["multiplex"] = mx
	}
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

// sbTransport renders the transport block for a URL-style proxy, or nil for
// plain TCP.
//
// Only "ws" used to be handled, so a grpc / httpupgrade / h2 node was emitted
// with no transport at all — the client then dialled plain TCP at a server
// speaking gRPC and the node simply never connected, with nothing in the config
// to suggest why. An unrecognised value also yields nil rather than being passed
// through: sing-box rejects an unknown transport type by refusing the whole
// config, which would take down every node in the subscription, not just this one.
func sbTransport(p *Proxy) map[string]any {
	switch p.param("type") {
	case "ws":
		return sbWS(p.param("path"), p.param("host"),
			atoi(p.param("max_early_data")), p.param("early_data_header_name"))
	case "grpc":
		t := map[string]any{"type": "grpc"}
		if v := p.param("serviceName", "servicename"); v != "" {
			t["service_name"] = v
		}
		return t
	case "httpupgrade":
		t := map[string]any{"type": "httpupgrade"}
		if v := p.param("path"); v != "" {
			t["path"] = v
		}
		if v := p.param("host"); v != "" {
			t["host"] = v
		}
		return t
	case "h2", "http":
		t := map[string]any{"type": "http"}
		if v := p.param("path"); v != "" {
			t["path"] = v
		}
		if v := p.param("host"); v != "" {
			t["host"] = []string{v}
		}
		return t
	}
	return nil
}

func sbWS(path, host string, maxEarlyData int, edHeader string) map[string]any {
	t := map[string]any{"type": "ws"}
	if path != "" {
		t["path"] = path
	}
	if host != "" {
		t["headers"] = map[string]any{"Host": host}
	}
	// ws 0-RTT early data (must mirror the inbound).
	if maxEarlyData > 0 {
		t["max_early_data"] = maxEarlyData
		if edHeader != "" {
			t["early_data_header_name"] = edHeader
		}
	}
	return t
}

// injectSingboxTunExclude adds proxy server IPs to the TUN inbound's
// route_exclude_address to prevent routing loops on the client.
//
// The key is route_exclude_address, NOT exclude_address: sing-box has no field
// by the latter name, and it rejects unknown config fields fatally — so every
// sing-box-format subscription failed to load outright with
// "inbounds[0].exclude_address: json: unknown field". Verified against
// sing-box 1.13's option/tun.go, which declares route_exclude_address /
// route_exclude_address_set (plus the inet4_/inet6_ variants).
func injectSingboxTunExclude(doc map[string]any, proxies []*Proxy) {
	seen := map[string]bool{}
	for _, p := range proxies {
		// route_exclude_address entries must be CIDR prefixes; only real public IPs
		// qualify. Bare domains (net.ParseIP == nil) would be invalid and break
		// the client config, so they are skipped here (see injectRouteExclude).
		if ip := net.ParseIP(p.Server); ip != nil && !ip.IsPrivate() {
			seen[p.Server] = true
		}
	}
	if len(seen) == 0 {
		return
	}
	// Normalize the inbounds array — a custom template decodes as []any, not
	// []map[string]any, so a naked type assertion would silently skip the
	// exclusion and reintroduce the TUN routing loop.
	inbounds := mapSlice(doc["inbounds"])
	if inbounds == nil {
		return
	}
	for _, ib := range inbounds {
		if ib["type"] != "tun" {
			continue
		}
		// Same []any-not-[]string trap as the inbounds array above: a template
		// arrives via json.Unmarshal, so a naked []string assertion always fails
		// and the admin's own exclusions get overwritten with just the node IPs.
		existing, present := stringList(ib["route_exclude_address"])
		for _, ip := range sortedKeys(seen) {
			cidr := ensureCIDR(ip)
			if !present[cidr] {
				existing = append(existing, cidr)
				present[cidr] = true
			}
		}
		ib["route_exclude_address"] = existing
	}
}
