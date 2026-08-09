package subconv

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// modernize runs the rewrite over a JSON template and hands back the dns block.
func modernize(t *testing.T, tpl string) map[string]any {
	t.Helper()
	doc := map[string]any{}
	if err := json.Unmarshal([]byte(tpl), &doc); err != nil {
		t.Fatalf("bad fixture: %v", err)
	}
	modernizeSingboxDNS(doc)
	dns, _ := doc["dns"].(map[string]any)
	return dns
}

func dnsServers(t *testing.T, dns map[string]any) []map[string]any {
	t.Helper()
	return mapSlice(dns["servers"])
}

// The exact shape 轻舟 shipped before sing-box 1.14 removed it. Every field here
// has to land somewhere in the typed format or a user's DNS silently changes.
func TestModernizeLegacyDefaultTemplate(t *testing.T) {
	dns := modernize(t, `{"dns":{
		"servers":[
			{"tag":"remote","address":"https://1.1.1.1/dns-query","detour":"proxy"},
			{"tag":"local","address":"https://223.5.5.5/dns-query","detour":"direct"},
			{"tag":"fake","address":"fakeip"}
		],
		"fakeip":{"enabled":true,"inet4_range":"198.18.0.0/15","inet6_range":"fc00::/18"},
		"independent_cache":true,
		"final":"remote"
	}}`)

	if _, still := dns["fakeip"]; still {
		t.Error("legacy top-level dns.fakeip block survived the rewrite")
	}
	// Untouched keys must stay untouched.
	if dns["final"] != "remote" || dns["independent_cache"] != true {
		t.Errorf("rewrite clobbered unrelated dns keys: %v", dns)
	}

	srvs := dnsServers(t, dns)
	want := []map[string]any{
		{"tag": "remote", "type": "https", "server": "1.1.1.1", "detour": "proxy"},
		{"tag": "local", "type": "https", "server": "223.5.5.5", "detour": "direct"},
		{"tag": "fake", "type": "fakeip", "inet4_range": "198.18.0.0/15", "inet6_range": "fc00::/18"},
	}
	if len(srvs) != len(want) {
		t.Fatalf("server count = %d, want %d", len(srvs), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(srvs[i], want[i]) {
			t.Errorf("server[%d] = %v, want %v", i, srvs[i], want[i])
		}
	}
}

// Rendering the shipped default must not change it: it is already modern, and a
// rewrite that mutated it would mean the two representations had drifted.
func TestModernizeDefaultTemplateIsIdempotent(t *testing.T) {
	first := modernize(t, DefaultSingboxTemplate)
	b, _ := json.Marshal(map[string]any{"dns": first})
	second := modernize(t, string(b))
	if !reflect.DeepEqual(first, second) {
		t.Errorf("not idempotent:\n first=%v\nsecond=%v", first, second)
	}
	for _, s := range dnsServers(t, first) {
		if _, ok := s["type"]; !ok {
			t.Errorf("shipped default still has an untyped DNS server: %v", s)
		}
		if _, ok := s["address"]; ok {
			t.Errorf("shipped default still uses the removed `address` field: %v", s)
		}
	}
}

func TestModernizeAddressForms(t *testing.T) {
	cases := []struct {
		addr string
		want map[string]any
	}{
		{"local", map[string]any{"type": "local"}},
		{"hosts", map[string]any{"type": "hosts"}},
		{"8.8.8.8", map[string]any{"type": "udp", "server": "8.8.8.8"}},
		{"udp://8.8.8.8", map[string]any{"type": "udp", "server": "8.8.8.8"}},
		{"udp://8.8.8.8:5353", map[string]any{"type": "udp", "server": "8.8.8.8", "server_port": 5353}},
		{"tcp://1.1.1.1", map[string]any{"type": "tcp", "server": "1.1.1.1"}},
		{"tls://dns.google", map[string]any{"type": "tls", "server": "dns.google"}},
		{"tls://dns.google:8853", map[string]any{"type": "tls", "server": "dns.google", "server_port": 8853}},
		{"quic://dns.adguard.com", map[string]any{"type": "quic", "server": "dns.adguard.com"}},
		{"https://1.1.1.1/dns-query", map[string]any{"type": "https", "server": "1.1.1.1"}},
		{"h3://1.1.1.1/dns-query", map[string]any{"type": "h3", "server": "1.1.1.1"}},
		// A non-default path has to survive, or queries hit the wrong endpoint.
		{"https://doh.example/resolve", map[string]any{"type": "https", "server": "doh.example", "path": "/resolve"}},
		{"https://doh.example:8443/dns-query", map[string]any{"type": "https", "server": "doh.example", "server_port": 8443}},
		// IPv6 literals: the brackets are authority syntax, not part of the host.
		{"udp://[2001:4860:4860::8888]", map[string]any{"type": "udp", "server": "2001:4860:4860::8888"}},
		{"tls://[2606:4700:4700::1111]:853", map[string]any{"type": "tls", "server": "2606:4700:4700::1111"}},
		// "auto" is the typed format's default and is expressed by omission.
		{"dhcp://auto", map[string]any{"type": "dhcp"}},
		{"dhcp://eth0", map[string]any{"type": "dhcp", "interface": "eth0"}},
	}
	for _, c := range cases {
		t.Run(c.addr, func(t *testing.T) {
			srv := map[string]any{"address": c.addr}
			claimed := false
			if !convertLegacyDNSServer(srv, c.addr, nil, &claimed) {
				t.Fatalf("refused to convert %q", c.addr)
			}
			delete(srv, "address")
			if !reflect.DeepEqual(srv, c.want) {
				t.Errorf("got %v, want %v", srv, c.want)
			}
		})
	}
}

// A form with no unambiguous typed equivalent must be left exactly as it was.
// rcode:// became a DNS rule *action*, so rewriting the server alone would
// change what the rules referencing it resolve to.
func TestModernizeLeavesUnknownFormsAlone(t *testing.T) {
	dns := modernize(t, `{"dns":{"servers":[
		{"tag":"block","address":"rcode://success"},
		{"tag":"local","address":"local"}
	]}}`)
	srvs := dnsServers(t, dns)
	if !reflect.DeepEqual(srvs[0], map[string]any{"tag": "block", "address": "rcode://success"}) {
		t.Errorf("rcode:// server was modified: %v", srvs[0])
	}
	// The recognised entry alongside it still converts.
	if srvs[1]["type"] != "local" {
		t.Errorf("neighbouring convertible server was skipped: %v", srvs[1])
	}
}

// Legacy dial-side fields were renamed rather than removed; losing them means
// a DoH server addressed by domain can no longer bootstrap itself.
func TestModernizeRenamesDialFields(t *testing.T) {
	dns := modernize(t, `{"dns":{"servers":[
		{"tag":"remote","address":"https://dns.example/dns-query",
		 "address_resolver":"local","address_strategy":"ipv4_only",
		 "address_fallback_delay":"300ms","strategy":"prefer_ipv4","detour":"proxy"}
	]}}`)
	srv := dnsServers(t, dns)[0]
	want := map[string]any{
		"tag": "remote", "type": "https", "server": "dns.example",
		"domain_resolver": "local", "fallback_delay": "300ms", "detour": "proxy",
	}
	if !reflect.DeepEqual(srv, want) {
		t.Errorf("got %v, want %v", srv, want)
	}
}

// A template that only ever had `enabled` in its fakeip block still has to lose
// that block: 1.14 rejects it as an unknown field.
func TestModernizeDropsBareFakeIPBlock(t *testing.T) {
	dns := modernize(t, `{"dns":{
		"servers":[{"tag":"fake","address":"fakeip"}],
		"fakeip":{"enabled":true}
	}}`)
	if _, still := dns["fakeip"]; still {
		t.Error("bare fakeip block survived")
	}
	if got := dnsServers(t, dns)[0]; got["type"] != "fakeip" {
		t.Errorf("fakeip server not converted: %v", got)
	}
}

// A hand-written range on the server outranks the legacy block; the block is
// still consumed so nothing invalid is left behind.
func TestModernizeFakeIPServerRangesWin(t *testing.T) {
	dns := modernize(t, `{"dns":{
		"servers":[{"tag":"fake","type":"fakeip","inet4_range":"10.0.0.0/8"}],
		"fakeip":{"enabled":true,"inet4_range":"198.18.0.0/15","inet6_range":"fc00::/18"}
	}}`)
	srv := dnsServers(t, dns)[0]
	if srv["inet4_range"] != "10.0.0.0/8" {
		t.Errorf("hand-written inet4_range was clobbered: %v", srv)
	}
	if srv["inet6_range"] != "fc00::/18" {
		t.Errorf("missing inet6_range was not topped up: %v", srv)
	}
	if _, still := dns["fakeip"]; still {
		t.Error("fakeip block survived after being adopted")
	}
}

// End-to-end: whatever an install has stored, what reaches the client is typed.
func TestSingboxRenderModernizesStoredTemplate(t *testing.T) {
	legacy := `{
	  "dns": {
	    "servers": [
	      {"tag": "remote", "address": "https://1.1.1.1/dns-query", "detour": "proxy"},
	      {"tag": "local", "address": "https://223.5.5.5/dns-query", "detour": "direct"},
	      {"tag": "fake", "address": "fakeip"}
	    ],
	    "rules": [{"query_type": ["A","AAAA"], "server": "fake"}],
	    "fakeip": {"enabled": true, "inet4_range": "198.18.0.0/15", "inet6_range": "fc00::/18"},
	    "final": "remote"
	  },
	  "route": {"final": "proxy", "rules": []}
	}`
	out, err := Singbox(ParseLinks([]string{
		"vless://11111111-2222-3333-4444-555555555555@example.com:443?security=tls&sni=example.com#n1",
	}), legacy)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("rendered config is not valid JSON: %v", err)
	}
	dns, _ := doc["dns"].(map[string]any)
	if _, still := dns["fakeip"]; still {
		t.Error("rendered config still contains the legacy top-level fakeip block")
	}
	for i, s := range mapSlice(dns["servers"]) {
		if _, legacy := s["address"]; legacy {
			t.Errorf("dns.servers[%d] still uses the removed `address` field: %v", i, s)
		}
		if _, typed := s["type"]; !typed {
			t.Errorf("dns.servers[%d] has no type: %v", i, s)
		}
	}
	// The domain-based node still gets its direct-resolution rule prepended, so
	// the rewrite has not disturbed the anti-leak injection that runs after it.
	if !strings.Contains(out, `"example.com"`) {
		t.Error("node domain rule missing from rendered config")
	}
}

// sing-box 1.13 refuses a config with no default_domain_resolver outright
// ("missing `route.default_domain_resolver` … will be removed in sing-box
// 1.14.0"), so migrating the DNS servers without this would have traded one
// fatal for another and left the subscription just as unloadable.
func TestModernizeAddsDomainResolver(t *testing.T) {
	doc := map[string]any{}
	if err := json.Unmarshal([]byte(DefaultSingboxTemplate), &doc); err != nil {
		t.Fatalf("bad default template: %v", err)
	}
	modernizeSingboxDNS(doc)
	route, _ := doc["route"].(map[string]any)
	if route["default_domain_resolver"] != "local" {
		t.Errorf("default_domain_resolver = %v, want \"local\"", route["default_domain_resolver"])
	}
}

func TestModernizeBackfillsDomainResolverOnLegacyTemplate(t *testing.T) {
	doc := map[string]any{}
	_ = json.Unmarshal([]byte(`{
		"dns": {"servers": [
			{"tag":"remote","address":"https://1.1.1.1/dns-query","detour":"proxy"},
			{"tag":"local","address":"https://223.5.5.5/dns-query","detour":"direct"},
			{"tag":"fake","address":"fakeip"}
		]},
		"route": {"final": "proxy"}
	}`), &doc)
	modernizeSingboxDNS(doc)
	route, _ := doc["route"].(map[string]any)
	if route["default_domain_resolver"] != "local" {
		t.Errorf("got %v, want \"local\"", route["default_domain_resolver"])
	}
}

// An admin who set one keeps it.
func TestModernizeDoesNotOverrideDomainResolver(t *testing.T) {
	doc := map[string]any{}
	_ = json.Unmarshal([]byte(`{
		"dns": {"servers": [{"tag":"local","address":"local"}]},
		"route": {"default_domain_resolver": "mine"}
	}`), &doc)
	modernizeSingboxDNS(doc)
	route, _ := doc["route"].(map[string]any)
	if route["default_domain_resolver"] != "mine" {
		t.Errorf("admin's resolver was overwritten: %v", route["default_domain_resolver"])
	}
}

func TestPickDomainResolver(t *testing.T) {
	cases := []struct {
		name    string
		servers []map[string]any
		want    string
	}{
		{"prefers the conventional local tag", []map[string]any{
			{"tag": "remote", "type": "https", "detour": "proxy"},
			{"tag": "local", "type": "https", "detour": "direct"},
		}, "local"},
		// Resolving the proxy's own hostname through the proxy is circular, so a
		// directly-dialled server wins over a detoured one.
		{"prefers a direct server over a detoured one", []map[string]any{
			{"tag": "viaproxy", "type": "https", "detour": "proxy"},
			{"tag": "plain", "type": "https"},
		}, "plain"},
		// Every dial would otherwise be handed a 198.18.x fake address.
		{"never picks fakeip", []map[string]any{
			{"tag": "fake", "type": "fakeip"},
			{"tag": "remote", "type": "https", "detour": "proxy"},
		}, "remote"},
		{"falls back to a detoured server when that is all there is", []map[string]any{
			{"tag": "only", "type": "https", "detour": "proxy"},
		}, "only"},
		// A dangling tag is a hard error, while the missing field is only
		// deprecated — so emit nothing rather than invent a reference.
		{"nothing usable yields nothing", []map[string]any{
			{"tag": "fake", "type": "fakeip"},
			{"type": "https"},
		}, ""},
		{"no servers at all", nil, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := pickDomainResolver(c.servers); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}
