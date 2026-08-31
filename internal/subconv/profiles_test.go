package subconv

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestNormalizeRoutingProfileIsExplicitAndSafe(t *testing.T) {
	for input, want := range map[string]RoutingProfile{
		"cn-direct":   ProfileCNDirect,
		" CN-DIRECT ": ProfileCNDirect,
		"proxy-all":   ProfileProxyAll,
		"":            ProfileLegacy,
		"cn-driect":   ProfileLegacy,
		"legacy":      ProfileLegacy,
	} {
		if got := NormalizeRoutingProfile(input); got != want {
			t.Errorf("NormalizeRoutingProfile(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestLegacyProfileDelegatesWithoutChangingOutput(t *testing.T) {
	links := nodeLinks()
	for _, format := range []string{FormatClash, FormatSingbox, FormatSurge, FormatBase64} {
		want, wantType, wantErr := Render(format, links, nil, "", "", "https://example.com/sub/t")
		got, gotType, gotErr := RenderWithProfile(format, links, nil, "", "", "https://example.com/sub/t", ProfileLegacy)
		if (wantErr == nil) != (gotErr == nil) || wantType != gotType || want != got {
			t.Errorf("legacy %s changed: type %q/%q err %v/%v equal=%v", format, wantType, gotType, wantErr, gotErr, want == got)
		}
	}
}

func TestClashCNDirectOwnsRoutingAndDNS(t *testing.T) {
	ps := ParseLinks(nodeLinks())
	ps[0].AI = true
	out, err := ClashWithProfile(ps, "", ProfileCNDirect)
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	rules, _ := doc["rules"].([]any)
	wantCN := []string{"GEOSITE,CN,DIRECT", "GEOIP,CN,DIRECT,no-resolve"}
	for _, want := range wantCN {
		if !containsAnyString(rules, want) {
			t.Errorf("CN-direct rule %q missing: %v", want, rules)
		}
	}
	if rules[0] != "RULE-SET,qingzhou-ai,"+grpAIClash {
		t.Errorf("AI guard lost top priority: %v", rules)
	}
	if indexAnyString(rules, "GEOSITE,category-ads-all,REJECT") > indexAnyString(rules, wantCN[0]) {
		t.Errorf("CN bypass precedes ad rejection: %v", rules)
	}

	dns, _ := doc["dns"].(map[string]any)
	policy, _ := dns["nameserver-policy"].(map[string]any)
	if len(anyStrings(policy["geosite:cn"])) != 2 {
		t.Errorf("CN nameserver policy = %v", policy["geosite:cn"])
	}
	if len(anyStrings(dns["direct-nameserver"])) != 2 || dns["direct-nameserver-follow-policy"] != true {
		t.Errorf("direct resolver is incomplete: %v", dns)
	}
}

func TestClashProxyAllReplacesManagedCNBypass(t *testing.T) {
	tpl := `
dns:
  nameserver-policy:
    geosite:cn: https://dns.alidns.com/dns-query
rules:
  - GEOSITE,CN,DIRECT
  - GEOIP,CN,DIRECT,no-resolve
`
	out, err := ClashWithProfile(ParseLinks(nodeLinks()), tpl, ProfileProxyAll)
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{}
	if err := yaml.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	rules, _ := doc["rules"].([]any)
	for _, want := range []string{
		"GEOSITE,CN," + grpSelectClash,
		"GEOIP,CN," + grpSelectClash + ",no-resolve",
	} {
		if !containsAnyString(rules, want) {
			t.Errorf("proxy-all rule %q missing: %v", want, rules)
		}
	}
	for _, raw := range rules {
		if s, _ := raw.(string); strings.Contains(s, "CN,DIRECT") {
			t.Errorf("old CN bypass survived proxy-all: %v", rules)
		}
	}
	dns, _ := doc["dns"].(map[string]any)
	policy, _ := dns["nameserver-policy"].(map[string]any)
	if _, ok := policy["geosite:cn"]; ok {
		t.Errorf("CN DNS policy survived proxy-all: %v", policy)
	}
}

func TestSingboxCNDirectUsesLocalResolverAfterDomainRouting(t *testing.T) {
	out, err := SingboxWithProfile(ParseLinks(nodeLinks()), "", ProfileCNDirect)
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	route, _ := doc["route"].(map[string]any)
	if route["default_domain_resolver"] != "local" {
		t.Errorf("default_domain_resolver = %v", route["default_domain_resolver"])
	}
	rules := mapSlice(route["rules"])
	for _, tag := range []string{"geosite-cn", "geoip-cn"} {
		found := false
		for _, rule := range rules {
			if rule["rule_set"] == tag && rule["outbound"] == "direct" {
				found = true
			}
		}
		if !found {
			t.Errorf("direct route for %s missing: %v", tag, rules)
		}
	}
	sets := mapSlice(route["rule_set"])
	for tag, wantURL := range map[string]string{"geosite-cn": singboxCNGeositeURL, "geoip-cn": singboxCNGeoIPURL} {
		found := false
		for _, set := range sets {
			if set["tag"] == tag && set["url"] == wantURL && set["download_detour"] == tagProxy {
				found = true
			}
		}
		if !found {
			t.Errorf("rule-set %s missing or unsafe: %v", tag, sets)
		}
	}
	// CN requests remain fake-IP initially; resolving them to a real address
	// here would discard the domain before geosite-cn can make the decision.
	dns, _ := doc["dns"].(map[string]any)
	dnsRules := mapSlice(dns["rules"])
	fakeKept := false
	for _, rule := range dnsRules {
		if rule["server"] == "fake" {
			fakeKept = true
		}
	}
	if !fakeKept {
		t.Errorf("fake-IP DNS path changed: %v", dnsRules)
	}
}

func TestSingboxProxyAllRemovesOnlyManagedCNDirectRules(t *testing.T) {
	tpl := `{
  "dns": {"servers":[{"tag":"local","type":"https","server":"223.5.5.5"}]},
  "route": {
    "rules": [
      {"rule_set":"geosite-cn","outbound":"direct"},
      {"domain_suffix":"company.cn","outbound":"direct"}
    ]
  }
}`
	out, err := SingboxWithProfile(ParseLinks(nodeLinks()), tpl, ProfileProxyAll)
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	route, _ := doc["route"].(map[string]any)
	rules := mapSlice(route["rules"])
	customKept, managedKept := false, false
	for _, rule := range rules {
		if rule["domain_suffix"] == "company.cn" {
			customKept = true
		}
		if rule["rule_set"] == "geosite-cn" {
			managedKept = true
		}
	}
	if !customKept || managedKept {
		t.Errorf("proxy-all removed the wrong custom rules: %v", rules)
	}
}

func TestSurgeProfilesKeepLegacyAndSeparateProxyAll(t *testing.T) {
	ps := ParseLinks(nodeLinks())
	legacy := Surge(ps, "https://example.com/sub/t")
	cn := SurgeWithProfile(ParseLinks(nodeLinks()), "https://example.com/sub/t?profile=cn-direct", ProfileCNDirect)
	all := SurgeWithProfile(ParseLinks(nodeLinks()), "https://example.com/sub/t?profile=proxy-all", ProfileProxyAll)
	if !strings.Contains(legacy, "GEOIP,CN,DIRECT") || !strings.Contains(cn, "GEOIP,CN,DIRECT") {
		t.Error("legacy or cn-direct lost Surge's CN bypass")
	}
	if strings.Contains(all, "GEOIP,CN,DIRECT") {
		t.Error("proxy-all Surge still bypasses CN")
	}
	if !strings.Contains(cn, "#!MANAGED-CONFIG https://example.com/sub/t?profile=cn-direct") {
		t.Error("Surge refresh URL dropped the routing profile")
	}
}

// Unit assertions verify our intended structure; the real binary is the final
// authority on whether a client will accept it. CI/local verification can opt in
// without making ordinary test runs download sing-box.
func TestCNDirectProfilePassesSingboxCheck(t *testing.T) {
	bin := os.Getenv("QZ_SINGBOX_TEST_BIN")
	if bin == "" {
		t.Skip("set QZ_SINGBOX_TEST_BIN to a sing-box binary to run this")
	}
	body, err := SingboxWithProfile(ParseLinks(nodeLinks()), "", ProfileCNDirect)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "client.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, "check", "-c", path).CombinedOutput()
	if err != nil {
		t.Fatalf("sing-box check failed: %v\n%s\n--- config ---\n%s", err, out, body)
	}
	if len(out) > 0 {
		t.Logf("sing-box check output:\n%s", out)
	}
}

func containsAnyString(values []any, want string) bool { return indexAnyString(values, want) >= 0 }

func indexAnyString(values []any, want string) int {
	for i, value := range values {
		if value == want {
			return i
		}
	}
	return -1
}

func anyStrings(value any) []string {
	values, _ := value.([]any)
	out := make([]string, 0, len(values))
	for _, value := range values {
		if s, ok := value.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
