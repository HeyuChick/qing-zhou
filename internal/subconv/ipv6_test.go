package subconv

import (
	"encoding/json"
	"net/netip"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"qingzhou/internal/singbox"
)

// tunInbound pulls the TUN inbound out of a rendered sing-box config.
func tunInbound(t *testing.T, cfg string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(cfg), &doc); err != nil {
		t.Fatalf("render is not valid JSON: %v", err)
	}
	for _, ib := range mapSlice(doc["inbounds"]) {
		if ib["type"] == "tun" {
			return ib
		}
	}
	t.Fatal("no tun inbound in rendered config")
	return nil
}

func renderDefault(t *testing.T, links ...string) string {
	t.Helper()
	out, err := Singbox(ParseLinks(links), DefaultSingboxTemplate)
	if err != nil {
		t.Fatalf("Singbox: %v", err)
	}
	return out
}

// The DNS half of DefaultSingboxTemplate answers AAAA out of fakeip.inet6_range
// (fc00::/18). If the TUN carries no inet6 prefix, auto_route installs no IPv6
// route and every one of those fake addresses is unroutable: AAAA-resolved
// connections fail with ENETUNREACH and IPv6-only domains are dead. The two
// halves have to stay in sync.
func TestTunCarriesIPv6WhenFakeipHandsOutIPv6(t *testing.T) {
	if !strings.Contains(DefaultSingboxTemplate, "inet6_range") {
		t.Skip("template no longer fake-ips IPv6; TUN inet6 prefix not required")
	}
	addrs, _ := stringList(tunInbound(t, renderDefault(t, "trojan://pw@1.2.3.4:443?sni=a.com#n"))["address"])
	var v6 bool
	for _, a := range addrs {
		// Parse rather than looking for a colon: "not-an-address:" would satisfy
		// a substring check while being nothing sing-box can bind.
		p, err := netip.ParsePrefix(a)
		if err != nil {
			t.Errorf("tun.address entry %q is not a valid prefix: %v", a, err)
			continue
		}
		if p.Addr().Is6() {
			v6 = true
		}
	}
	if !v6 {
		t.Errorf("tun.address has no IPv6 prefix, but DNS hands out fake IPv6: %v", addrs)
	}
}

// fe80::/10 and ff00::/8 belong off-tunnel (neighbor discovery, SLAAC, mDNS).
// fc00::/7 does NOT: fakeip allocates fc00::/18 from inside it, so excluding
// the parent prefix would route every fake IPv6 back out of the tunnel.
func TestTunExcludesIPv6LocalButNotFakeipParent(t *testing.T) {
	_, present := stringList(tunInbound(t, renderDefault(t, "trojan://pw@1.2.3.4:443?sni=a.com#n"))["route_exclude_address"])
	for _, want := range []string{"fe80::/10", "ff00::/8"} {
		if !present[want] {
			t.Errorf("missing IPv6 local exclusion %s", want)
		}
	}
	if present["fc00::/7"] {
		t.Error("fc00::/7 excluded — this strands the fakeip inet6_range outside the tunnel")
	}
}

// Regression: stringList used to handle only []any, so the Go-built default
// inbound's literal []string exclusions were read as empty and silently
// overwritten the moment a node IP was appended.
func TestNodeIPInjectionPreservesBuiltinExclusions(t *testing.T) {
	_, present := stringList(tunInbound(t, renderDefault(t, "trojan://pw@1.2.3.4:443?sni=a.com#n"))["route_exclude_address"])
	if !present["1.2.3.4/32"] {
		t.Error("node IP was not injected")
	}
	if !present["fe80::/10"] {
		t.Error("built-in exclusion lost when the node IP was appended")
	}
}

func TestStringListAcceptsBothShapes(t *testing.T) {
	for name, in := range map[string]any{
		"[]string": []string{"a", "b"},
		"[]any":    []any{"a", "b"},
	} {
		got, _ := stringList(in)
		if len(got) != 2 || got[0] != "a" || got[1] != "b" {
			t.Errorf("%s: got %v, want [a b]", name, got)
		}
	}
}

// --- IPv6 node addresses ----------------------------------------------------

// hpProtocols are the share-link types whose authority goes through
// joinHostPort. vmess is deliberately absent: its base64 JSON keeps add and port
// in separate fields, so it never builds a host:port string at all.
var hpProtocols = []string{"vless", "trojan", "hysteria2", "tuic", "shadowsocks", "anytls", "hysteria"}

// An IPv6 authority must be bracketed, per RFC 3986.
//
// The assertion is on the link TEXT on purpose. Asserting a parse round-trip
// proves nothing here: Go's url.Parse splits host and port at the LAST colon, so
// it recovers "…@2001:db8::1:443" perfectly and this package round-trips the
// unbracketed form just fine. The clients do not — v2rayN, mihomo and sing-box
// parse the authority per spec — so the bug is invisible to a round-trip test
// and only shows up in the client.
func TestIPv6LinkAuthorityIsBracketed(t *testing.T) {
	for _, typ := range hpProtocols {
		link := singbox.BuildShareLink(singbox.LinkParams{
			Type: typ, Tag: "v6", Host: "2001:db8::1", Port: 443, UUID: "u",
			Password: "pw", Method: "2022-blake3-aes-128-gcm", TLS: true, SNI: "a.com",
		})
		if link == "" {
			t.Errorf("%s: no link built", typ)
			continue
		}
		if !strings.Contains(link, "[2001:db8::1]:443") {
			t.Errorf("%s: authority not bracketed: %q", typ, link)
		}
	}
}

// A host that already carries brackets must not gain a second pair. This is the
// regression the naive net.JoinHostPort introduced: [2001:db8::1] became
// [[2001:db8::1]]:443, which really is unparseable everywhere — so a node that
// worked before started being dropped.
func TestAlreadyBracketedIPv6HostNotDoubled(t *testing.T) {
	for _, typ := range hpProtocols {
		link := singbox.BuildShareLink(singbox.LinkParams{
			Type: typ, Tag: "v6", Host: "[2001:db8::1]", Port: 443, UUID: "u",
			Password: "pw", Method: "2022-blake3-aes-128-gcm", TLS: true, SNI: "a.com",
		})
		if strings.Contains(link, "[[") {
			t.Errorf("%s: host double-bracketed: %q", typ, link)
		}
		if !strings.Contains(link, "[2001:db8::1]:443") {
			t.Errorf("%s: authority not bracketed exactly once: %q", typ, link)
		}
	}
}

// An IPv4 host or a domain must never acquire brackets.
func TestNonIPv6HostUnbracketed(t *testing.T) {
	for _, host := range []string{"1.2.3.4", "example.com"} {
		for _, typ := range hpProtocols {
			link := singbox.BuildShareLink(singbox.LinkParams{
				Type: typ, Tag: "n", Host: host, Port: 443, UUID: "u",
				Password: "pw", Method: "2022-blake3-aes-128-gcm", TLS: true, SNI: "a.com",
			})
			if strings.Contains(link, "[") {
				t.Errorf("%s/%s: host was bracketed: %q", typ, host, link)
			}
		}
	}
}

// The Clash-YAML import path applies the same rules, for both input shapes.
func TestClashImportBracketsIPv6Authority(t *testing.T) {
	for _, server := range []string{`"2001:db8::1"`, `"[2001:db8::1]"`} {
		yaml := "proxies:\n  - {name: v6, type: trojan, server: " + server +
			", port: 443, password: pw, sni: a.com}\n"
		proxies := ParseList(yaml)
		if len(proxies) != 1 {
			t.Fatalf("server %s: node dropped on import, got %d proxies", server, len(proxies))
		}
		if strings.Contains(proxies[0].Raw, "[[") {
			t.Errorf("server %s: double-bracketed link %q", server, proxies[0].Raw)
		}
		if !strings.Contains(proxies[0].Raw, "[2001:db8::1]:443") {
			t.Errorf("server %s: authority not bracketed: %q", server, proxies[0].Raw)
		}
		if got := proxies[0].Server; got != "2001:db8::1" {
			t.Errorf("server %s: parsed server = %q", server, got)
		}
	}
}

// --- Clash side -------------------------------------------------------------

func renderClash(t *testing.T, links ...string) map[string]any {
	t.Helper()
	out, err := Clash(ParseLinks(links), DefaultClashTemplate)
	if err != nil {
		t.Fatalf("Clash: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("rendered Clash config is not valid YAML: %v", err)
	}
	return doc
}

// mihomo's parseIPV6 nulls out both fake-ip-range6 and tun.inet6-address unless
// the top-level ipv6 switch is true, so the v6 pool below is inert without it.
func TestClashTopLevelIPv6Enabled(t *testing.T) {
	doc := renderClash(t, "trojan://pw@1.2.3.4:443?sni=a.com#n")
	if doc["ipv6"] != true {
		t.Errorf("top-level ipv6 = %v, want true — fake-ip-range6 is dead without it", doc["ipv6"])
	}
}

// Without a v6 fake pool, fake-ip answers AAAA empty and IPv6-only hostnames are
// unreachable. The value must also stay inside the prefix that mihomo's stock
// tun.inet6-address default (fdfe:dcba:9876::1/126) sits in — mihomo does not
// derive one from the other the way it does for IPv4.
func TestClashFakeIPv6PoolMatchesStockInet6Address(t *testing.T) {
	dns, ok := renderClash(t, "trojan://pw@1.2.3.4:443?sni=a.com#n")["dns"].(map[string]any)
	if !ok {
		t.Fatal("no dns section")
	}
	got, _ := dns["fake-ip-range6"].(string)
	if got == "" {
		t.Fatal("dns.fake-ip-range6 missing — AAAA collapses to empty and IPv6-only sites break")
	}
	pool, err := netip.ParsePrefix(got)
	if err != nil {
		t.Fatalf("fake-ip-range6 %q is not a prefix: %v", got, err)
	}
	// mihomo's hardcoded default, config.go:547.
	stock := netip.MustParsePrefix("fdfe:dcba:9876::1/126")
	if !pool.Contains(stock.Addr()) {
		t.Errorf("fake-ip-range6 %s does not contain the stock tun.inet6-address %s; "+
			"set tun.inet6-address explicitly or fake v6 stops being on-link", pool, stock)
	}
}

// dns.ipv6 must stay false: it is the only thing suppressing a real IPv6 answer
// on the fake-ip-filter escape path (it does not affect fake AAAA at all).
func TestClashDNSIPv6StaysFalseForFilterEscapePath(t *testing.T) {
	dns, _ := renderClash(t, "trojan://pw@1.2.3.4:443?sni=a.com#n")["dns"].(map[string]any)
	if v, ok := dns["ipv6"]; !ok || v != false {
		t.Errorf("dns.ipv6 = %v (present=%v), want false", v, ok)
	}
}

// tun.ipv6 is not a mihomo option — it never existed at any version, and yaml.v3
// non-strict parsing silently dropped it. Its presence reads as suppression that
// isn't happening, so it must not come back.
func TestClashTunHasNoPhantomIPv6Key(t *testing.T) {
	tun, ok := renderClash(t, "trojan://pw@1.2.3.4:443?sni=a.com#n")["tun"].(map[string]any)
	if !ok {
		t.Fatal("no tun section")
	}
	if _, present := tun["ipv6"]; present {
		t.Error("tun.ipv6 is back — mihomo has no such option; it silently does nothing")
	}
}

// fc00::/7 contains fake-ip-range6. Excluding it would route every fake IPv6 out
// of the tunnel and undo the whole fix.
func TestClashRouteExcludeNeverStrandsFakeIPv6(t *testing.T) {
	doc := renderClash(t, "trojan://pw@1.2.3.4:443?sni=a.com#n")
	tun, _ := doc["tun"].(map[string]any)
	excludes, _ := stringList(tun["route-exclude-address"])
	// Without this the loop below is vacuous: every stock entry is IPv4, so a
	// v6-only assertion silently covers nothing and would keep passing even if
	// the whole route-exclude-address block disappeared.
	if len(excludes) == 0 {
		t.Fatal("route-exclude-address is empty — the node-IP loop guard is gone")
	}

	dns, _ := doc["dns"].(map[string]any)
	poolStr, _ := dns["fake-ip-range6"].(string)
	pool := netip.MustParsePrefix(poolStr)

	checked := 0
	for _, e := range excludes {
		p, err := netip.ParsePrefix(e)
		if err != nil {
			t.Errorf("route-exclude-address entry %q is not a valid prefix: %v", e, err)
			continue
		}
		if !p.Addr().Is6() {
			continue
		}
		checked++
		if p.Overlaps(pool) {
			t.Errorf("route-exclude-address %s overlaps fake-ip-range6 %s — fake IPv6 would leave the tunnel", e, pool)
		}
	}
	t.Logf("%d IPv6 exclusion(s) checked against pool %s", checked, pool)
}

// route-exclude entries must be routable unicast. net.IP.IsPrivate covers only
// ULA on the v6 side, so link-local slipped through before isPublicIP.
func TestIsPublicIP(t *testing.T) {
	cases := map[string]bool{
		"1.2.3.4":     true,
		"2001:db8::1": true,
		"192.168.1.1": false,
		"10.0.0.1":    false,
		"fc00::1":     false, // ULA
		"fe80::1":     false, // link-local — the case IsPrivate misses
		"169.254.1.1": false, // v4 link-local
		"127.0.0.1":   false,
		"::1":         false,
		"example.com": false, // not an IP at all
		"":            false,
	}
	for in, want := range cases {
		if got := isPublicIP(in); got != want {
			t.Errorf("isPublicIP(%q) = %v, want %v", in, got, want)
		}
	}
}

// A link-local node address must not reach route_exclude_address. The node under
// test has to actually BE link-local — asserting this against a public-IPv4 node
// is vacuous, since fe80::1/128 could never appear regardless of isPublicIP.
func TestLinkLocalNodeNotExcluded(t *testing.T) {
	cfg := renderDefault(t,
		"trojan://pw@[fe80::1]:443?sni=a.com#linklocal",
		"trojan://pw@[2001:db8::1]:443?sni=a.com#global",
	)
	_, present := stringList(tunInbound(t, cfg)["route_exclude_address"])
	if present["fe80::1/128"] {
		t.Error("link-local node address injected as an exclusion")
	}
	// The global-unicast IPv6 node in the same subscription must still be
	// excluded, or this test would also pass if injection broke entirely.
	if !present["2001:db8::1/128"] {
		t.Error("global-unicast IPv6 node was not excluded — loop guard missing")
	}
}
