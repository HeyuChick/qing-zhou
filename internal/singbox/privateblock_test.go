package singbox

import (
	"encoding/json"
	"testing"
)

func routeRules(t *testing.T, raw []byte) []map[string]interface{} {
	t.Helper()
	var cfg map[string]interface{}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("generated config is not valid JSON: %v", err)
	}
	route, _ := cfg["route"].(map[string]interface{})
	list, _ := route["rules"].([]interface{})
	out := make([]map[string]interface{}, 0, len(list))
	for _, it := range list {
		if m, ok := it.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	return out
}

func isPrivateReject(m map[string]interface{}) bool {
	return m["ip_is_private"] == true && m["action"] == "reject"
}

func isResolve(m map[string]interface{}) bool { return m["action"] == "resolve" }

// resolvedInbounds returns the inbound tags the resolve rule applies to.
func resolvedInbounds(m map[string]interface{}) []string {
	raw, _ := m["inbound"].([]interface{})
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func vlessInbound(tag string) Inbound {
	return Inbound{Type: "vless", Base: map[string]interface{}{
		"type": "vless", "tag": tag, "listen": "::", "listen_port": 443,
	}, Users: []User{{Name: "u1", UUID: "11111111-2222-3333-4444-555555555555"}}}
}

func TestPrivateBlockOffByDefault(t *testing.T) {
	raw, err := GenerateConfig(json.RawMessage(DefaultBaseConfig), []Inbound{vlessInbound("in")}, "")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	for _, r := range routeRules(t, raw) {
		if isPrivateReject(r) {
			t.Fatal("private-block rule appeared without the option set")
		}
	}
}

// `ip_is_private` only ever matches an address, and a proxy protocol delivers
// whatever the client asked for — usually a hostname. Without the resolve in
// front, dialling "localhost" walks straight past the reject (verified against
// a real sing-box), so an attacker only needs a DNS record pointing at
// 169.254.169.254.
func TestPrivateBlockResolvesBeforeRejecting(t *testing.T) {
	raw, err := GenerateConfigWithOptions(json.RawMessage(DefaultBaseConfig),
		[]Inbound{vlessInbound("in")}, Options{BlockPrivate: true})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	rules := routeRules(t, raw)
	if len(rules) < 2 {
		t.Fatalf("expected resolve + reject, got %v", rules)
	}
	if !isResolve(rules[0]) {
		t.Errorf("rule[0] is not the resolve: %v", rules[0])
	}
	if !isPrivateReject(rules[1]) {
		t.Errorf("rule[1] is not the reject: %v", rules[1])
	}
	// The resolve needs a DNS server to resolve with.
	var cfg map[string]interface{}
	_ = json.Unmarshal(raw, &cfg)
	route, _ := cfg["route"].(map[string]interface{})
	if route["default_domain_resolver"] != "local" {
		t.Errorf("default_domain_resolver = %v, want \"local\"", route["default_domain_resolver"])
	}
}

// Resolving a relay inbound's destination here would pin the user to whatever
// CDN node is closest to the RELAY rather than the landing. The landing applies
// these same rules on its own inbounds, so nothing is left unprotected.
func TestPrivateBlockDoesNotResolveRelayedInbounds(t *testing.T) {
	relay := Relay{
		Outbound:    map[string]interface{}{"type": "vless", "tag": "to-landing", "server": "203.0.113.9"},
		InboundTags: []string{"relayed"},
	}
	raw, err := GenerateConfigWithOptions(json.RawMessage(DefaultBaseConfig),
		[]Inbound{vlessInbound("direct-in"), vlessInbound("relayed")},
		Options{BlockPrivate: true, Relays: []Relay{relay}})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	rules := routeRules(t, raw)
	if !isResolve(rules[0]) {
		t.Fatalf("rule[0] is not the resolve: %v", rules[0])
	}
	got := resolvedInbounds(rules[0])
	if len(got) != 1 || got[0] != "direct-in" {
		t.Errorf("resolve applies to %v, want only [direct-in]", got)
	}
	// The reject still covers everything, relayed or not.
	if !isPrivateReject(rules[1]) {
		t.Errorf("rule[1] is not the reject: %v", rules[1])
	}
}

// A base config with no referenceable DNS server cannot support the resolve.
// Emitting it anyway would produce a config sing-box refuses, taking the node
// down — partial protection is better than a node that will not start.
func TestPrivateBlockFallsBackWithoutADNSTag(t *testing.T) {
	base := `{"dns":{"servers":[{"type":"local"}]},"outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`
	raw, err := GenerateConfigWithOptions(json.RawMessage(base),
		[]Inbound{vlessInbound("in")}, Options{BlockPrivate: true})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	rules := routeRules(t, raw)
	if len(rules) != 1 || !isPrivateReject(rules[0]) {
		t.Fatalf("want the reject alone, got %v", rules)
	}
	var cfg map[string]interface{}
	_ = json.Unmarshal(raw, &cfg)
	route, _ := cfg["route"].(map[string]interface{})
	if _, set := route["default_domain_resolver"]; set {
		t.Error("a resolver was referenced that the config does not define")
	}
}

// A fake-ip server would answer every destination with a 198.18.x address,
// which the reject would then treat as... public, silently disabling the block
// while looking configured.
func TestPrivateBlockNeverResolvesViaFakeIP(t *testing.T) {
	base := `{"dns":{"servers":[{"type":"fakeip","tag":"fake"},{"type":"local","tag":"real"}]},
	          "outbounds":[{"type":"direct","tag":"direct"}],"route":{"final":"direct"}}`
	raw, err := GenerateConfigWithOptions(json.RawMessage(base),
		[]Inbound{vlessInbound("in")}, Options{BlockPrivate: true})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	var cfg map[string]interface{}
	_ = json.Unmarshal(raw, &cfg)
	route, _ := cfg["route"].(map[string]interface{})
	if route["default_domain_resolver"] != "real" {
		t.Errorf("default_domain_resolver = %v, want the non-fakeip server", route["default_domain_resolver"])
	}
}

// Route rules are first-match. A relay's steering rule appended after the reject
// must not shadow it, or the whole point (a relay user cannot reach the landing
// machine's LAN either) is lost.
func TestPrivateBlockPrecedesRelayRules(t *testing.T) {
	relay := Relay{
		Outbound:    map[string]interface{}{"type": "vless", "tag": "to-landing", "server": "203.0.113.9"},
		InboundTags: []string{"in"},
	}
	raw, err := GenerateConfigWithOptions(json.RawMessage(DefaultBaseConfig),
		[]Inbound{vlessInbound("in")}, Options{BlockPrivate: true, Relays: []Relay{relay}})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	rules := routeRules(t, raw)
	if len(rules) < 2 {
		t.Fatalf("expected the guard rules + relay rule, got %v", rules)
	}
	// The reject must precede the relay steering rule; route rules are
	// first-match, so behind it the reject would never be consulted.
	rejectAt, relayAt := -1, -1
	for i, r := range rules {
		if isPrivateReject(r) && rejectAt < 0 {
			rejectAt = i
		}
		if r["outbound"] == "to-landing" && relayAt < 0 {
			relayAt = i
		}
	}
	if rejectAt < 0 || relayAt < 0 || rejectAt > relayAt {
		t.Errorf("reject at %d, relay at %d — reject must come first: %v", rejectAt, relayAt, rules)
	}
	// The relay outbound itself must survive — its dial to the landing is not
	// route-matched, so a private landing address keeps working.
	var cfg map[string]interface{}
	_ = json.Unmarshal(raw, &cfg)
	obs, _ := cfg["outbounds"].([]interface{})
	found := false
	for _, o := range obs {
		if m, ok := o.(map[string]interface{}); ok && m["tag"] == "to-landing" {
			found = true
		}
	}
	if !found {
		t.Error("relay outbound was dropped")
	}
}

// Regenerating must not stack the rule, and an admin who already wrote their own
// private-space reject into sb_base_config keeps exactly one.
func TestPrivateBlockNotDuplicated(t *testing.T) {
	base := `{
	  "log": {"level": "warn"},
	  "outbounds": [{"type": "direct", "tag": "direct"}],
	  "route": {"rules": [{"ip_is_private": true, "action": "reject"}], "final": "direct"}
	}`
	raw, err := GenerateConfigWithOptions(json.RawMessage(base),
		[]Inbound{vlessInbound("in")}, Options{BlockPrivate: true})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	n := 0
	for _, r := range routeRules(t, raw) {
		if isPrivateReject(r) {
			n++
		}
	}
	if n != 1 {
		t.Errorf("private-block rule count = %d, want 1", n)
	}
}

// A base config with no route block at all must still get one.
func TestPrivateBlockCreatesRouteBlock(t *testing.T) {
	raw, err := GenerateConfigWithOptions(json.RawMessage(`{"outbounds":[{"type":"direct","tag":"direct"}]}`),
		[]Inbound{vlessInbound("in")}, Options{BlockPrivate: true})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	rules := routeRules(t, raw)
	if len(rules) != 1 || !isPrivateReject(rules[0]) {
		t.Fatalf("route rules = %v", rules)
	}
}
