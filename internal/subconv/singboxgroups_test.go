package subconv

import (
	"encoding/json"
	"strings"
	"testing"
)

func renderSingboxDoc(t *testing.T, tpl string, links ...string) map[string]any {
	t.Helper()
	out, err := Singbox(ParseLinks(links), tpl)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	doc := map[string]any{}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("rendered JSON does not parse: %v", err)
	}
	return doc
}

func singboxOutboundByTag(doc map[string]any, tag string) map[string]any {
	for _, outbound := range mapSlice(doc["outbounds"]) {
		if got, _ := outbound["tag"].(string); got == tag {
			return outbound
		}
	}
	return nil
}

func TestSingboxTemplateSelectorsMergeAndExpandAll(t *testing.T) {
	tpl := `{
  "outbounds": [
    {"type":"selector", "tag":"work", "outbounds":["proxy", "auto", "all"]},
    {"type":"selector", "tag":"social", "outbounds":["proxy", "B", "missing"]},
    {"type":"socks", "tag":"static", "server":"127.0.0.1", "server_port":1080}
  ]
}`
	doc := renderSingboxDoc(t, tpl, nodeLinks()...)
	outs := mapSlice(doc["outbounds"])
	wantOrder := []string{tagProxy, "work", "social", tagFixed, tagFallback, "A", "B", "direct"}
	if len(outs) != len(wantOrder) {
		t.Fatalf("outbound count = %d, want %d: %v", len(outs), len(wantOrder), outs)
	}
	for i, want := range wantOrder {
		if got, _ := outs[i]["tag"].(string); got != want {
			t.Fatalf("outbounds[%d].tag = %q, want %q", i, got, want)
		}
	}
	ai := stringSlice(singboxOutboundByTag(doc, "work")["outbounds"])
	if strings.Join(ai, ",") != "proxy,fallback,A,B" {
		t.Errorf("work selector = %v, want [proxy fallback A B]", ai)
	}
	social := stringSlice(singboxOutboundByTag(doc, "social")["outbounds"])
	if strings.Join(social, ",") != "proxy,B" {
		t.Errorf("social selector = %v, want [proxy B]", social)
	}
	if singboxOutboundByTag(doc, "static") != nil {
		t.Error("non-selector template outbound was retained")
	}
}

func TestSingboxTemplateSelectorCollisionsAreIgnored(t *testing.T) {
	tpl := `{"outbounds":[
  {"type":"selector", "tag":"proxy", "outbounds":["direct"]},
  {"type":"selector", "tag":"fixed", "outbounds":["direct"]},
	{"type":"selector", "tag":"fallback", "outbounds":["direct"]},
	{"type":"selector", "tag":"ai", "outbounds":["direct"]},
  {"type":"selector", "tag":"direct", "outbounds":["proxy"]},
	{"type":"selector", "tag":"A", "outbounds":["proxy"]},
	{"type":"selector", "tag":"platform", "outbounds":[], "default":"missing"},
  {"type":"selector", "tag":"platform", "outbounds":["direct"]}
]}`
	doc := renderSingboxDoc(t, tpl, nodeLinks()...)
	counts := map[string]int{}
	for _, outbound := range mapSlice(doc["outbounds"]) {
		tag, _ := outbound["tag"].(string)
		counts[tag]++
	}
	for _, tag := range []string{"proxy", "fixed", "fallback", "direct", "A", "A #2", "platform"} {
		if counts[tag] != 1 {
			t.Errorf("tag %q appears %d times, want 1", tag, counts[tag])
		}
	}
	if got := singboxOutboundByTag(doc, tagAI); got == nil || got["type"] != "selector" {
		t.Errorf("custom ai selector was dropped when no generated AI policy exists: %v", got)
	}
	platform := stringSlice(singboxOutboundByTag(doc, "platform")["outbounds"])
	if len(platform) != 1 || platform[0] != tagProxy {
		t.Errorf("empty platform selector = %v, want proxy fallback", platform)
	}
	if _, ok := singboxOutboundByTag(doc, "platform")["default"]; ok {
		t.Error("dangling selector default was retained")
	}
	if got := singboxOutboundByTag(doc, "A")["type"]; got != "selector" {
		t.Errorf("custom tag collision resolved to %v, want selector", got)
	}
}

func TestSingboxAIRouteIsAutomaticAndOrderedAfterSniff(t *testing.T) {
	ps := ParseLinks(nodeLinks())
	ps[0].AI = true
	out, err := Singbox(ps, "")
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	primary := stringSlice(singboxOutboundByTag(doc, tagProxy)["outbounds"])
	if strings.Contains(strings.Join(primary, ","), tagAI) {
		t.Fatalf("AI guard leaked into manual selector: %v", primary)
	}
	ai := singboxOutboundByTag(doc, tagAI)
	if ai == nil || ai["type"] != "selector" {
		t.Fatalf("single-node AI selector missing: %v", ai)
	}
	if refs := stringSlice(ai["outbounds"]); len(refs) != 1 || refs[0] != "A" {
		t.Errorf("AI outbounds = %v", refs)
	}
	route, _ := doc["route"].(map[string]any)
	rules := mapSlice(route["rules"])
	sniff, hijack, aiRoute := -1, -1, -1
	for i, rule := range rules {
		switch {
		case rule["action"] == "sniff":
			sniff = i
		case rule["action"] == "hijack-dns":
			hijack = i
		case rule["outbound"] == tagAI:
			aiRoute = i
		}
	}
	if sniff < 0 || hijack != sniff+1 || aiRoute != hijack+1 {
		t.Errorf("AI rule was not inserted after sniff/DNS actions: %v", rules)
	}
	found := false
	for _, rs := range mapSlice(route["rule_set"]) {
		if rs["tag"] == "qingzhou-ai" && rs["url"] == singboxAIRuleURL {
			found = true
		}
	}
	if !found {
		t.Error("AI rule-set missing")
	}
}

func TestDefaultSingboxUsesProxyForPublicTraffic(t *testing.T) {
	if strings.Contains(DefaultSingboxTemplate, "geosite-cn") || strings.Contains(DefaultSingboxTemplate, "geoip-cn") {
		t.Fatal("default sing-box template still contains a CN direct bypass")
	}
	doc := renderSingboxDoc(t, "", nodeLinks()...)
	route, _ := doc["route"].(map[string]any)
	if route["final"] != tagProxy {
		t.Errorf("route.final = %v, want proxy", route["final"])
	}
	inbounds := mapSlice(doc["inbounds"])
	if len(inbounds) == 0 {
		t.Fatal("default tun inbound is missing")
	}
	if inbounds[0]["strict_route"] != true {
		t.Errorf("default tun strict_route = %v, want true", inbounds[0]["strict_route"])
	}
}
