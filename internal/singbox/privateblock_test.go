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

func TestPrivateBlockIsFirstRule(t *testing.T) {
	raw, err := GenerateConfigWithOptions(json.RawMessage(DefaultBaseConfig),
		[]Inbound{vlessInbound("in")}, Options{BlockPrivate: true})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	rules := routeRules(t, raw)
	if len(rules) == 0 || !isPrivateReject(rules[0]) {
		t.Fatalf("private-block rule is not first: %v", rules)
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
		t.Fatalf("expected reject + relay rule, got %v", rules)
	}
	if !isPrivateReject(rules[0]) {
		t.Errorf("rule[0] is not the reject: %v", rules[0])
	}
	if rules[1]["outbound"] != "to-landing" {
		t.Errorf("relay rule missing or reordered: %v", rules[1])
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
