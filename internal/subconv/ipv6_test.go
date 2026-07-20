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

// An IPv6 node address must be bracketed in the share link. Concatenating
// host+":"+port yielded "…@2001:db8::1:443", whose port url.Parse cannot
// recover, so validate() rejected it as "port out of range" and the node
// disappeared from every subscription format with nothing shown to the user.
func TestIPv6NodeSurvivesLinkRoundTrip(t *testing.T) {
	for _, typ := range []string{"vless", "trojan", "vmess", "hysteria2", "tuic"} {
		link := singbox.BuildShareLink(singbox.LinkParams{
			Type: typ, Tag: "v6", Host: "2001:db8::1", Port: 443, UUID: "u",
			Password: "pw", TLS: true, SNI: "a.com",
		})
		if link == "" {
			t.Errorf("%s: no link built", typ)
			continue
		}
		proxies := ParseLinks([]string{link})
		if len(proxies) != 1 {
			t.Errorf("%s: node dropped during parse — link was %q", typ, link)
			continue
		}
		if got := proxies[0].Server; got != "2001:db8::1" {
			t.Errorf("%s: server = %q, want 2001:db8::1", typ, got)
		}
		if got := proxies[0].Port; got != 443 {
			t.Errorf("%s: port = %d, want 443", typ, got)
		}
	}
}

// An IPv4 node must not acquire brackets.
func TestIPv4NodeLinkUnbracketed(t *testing.T) {
	link := singbox.BuildShareLink(singbox.LinkParams{
		Type: "trojan", Tag: "v4", Host: "1.2.3.4", Port: 443, Password: "pw", TLS: true,
	})
	if strings.Contains(link, "[") {
		t.Errorf("IPv4 host was bracketed: %q", link)
	}
}

// The Clash-YAML import path has the same bracketing requirement on re-export.
func TestClashImportOfIPv6NodeRoundTrips(t *testing.T) {
	yaml := `proxies:
  - {name: v6, type: trojan, server: "2001:db8::1", port: 443, password: pw, sni: a.com}
`
	proxies := ParseList(yaml)
	if len(proxies) != 1 {
		t.Fatalf("IPv6 node dropped on Clash import, got %d proxies", len(proxies))
	}
	if got := proxies[0].Server; got != "2001:db8::1" {
		t.Errorf("server = %q, want 2001:db8::1", got)
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
