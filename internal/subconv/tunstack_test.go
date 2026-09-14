package subconv

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSingboxTUNOmitsStackByDefault locks the 1.15+ path: default / "new client"
// sing-box subscriptions must not emit tun.stack (deprecated in 1.15, removed
// in 1.17). See https://sing-box.sagernet.org/migration/#migrate-tun-stack.
func TestSingboxTUNOmitsStackByDefault(t *testing.T) {
	out, err := Singbox(ParseLinks([]string{"trojan://pw@1.2.3.4:443?sni=a.com#n"}), "")
	if err != nil {
		t.Fatalf("Singbox: %v", err)
	}
	tun := tunInbound(t, out)
	if _, ok := tun["stack"]; ok {
		t.Fatalf("default TUN inbound still has stack=%v; want omitted for ≥1.15", tun["stack"])
	}
	// Render entry used by the subscription handler must match.
	body, _, err := RenderWithOptions(FormatSingbox, []string{"trojan://pw@1.2.3.4:443?sni=a.com#n"}, nil, "", "", "", RenderOptions{})
	if err != nil {
		t.Fatalf("RenderWithOptions: %v", err)
	}
	if _, ok := tunInbound(t, body)["stack"]; ok {
		t.Fatal("RenderWithOptions default still emits tun.stack")
	}
}

// TestSingboxLegacyTUNStackEmitsGvisor locks the ≤1.14 compat path
// (?tun_stack=gvisor / SingboxOptions.LegacyTUNStack).
func TestSingboxLegacyTUNStackEmitsGvisor(t *testing.T) {
	out, err := SingboxWithOptions(ParseLinks([]string{"trojan://pw@1.2.3.4:443?sni=a.com#n"}), "", ProfileLegacy, SingboxOptions{LegacyTUNStack: true})
	if err != nil {
		t.Fatalf("SingboxWithOptions: %v", err)
	}
	tun := tunInbound(t, out)
	if tun["stack"] != "gvisor" {
		t.Fatalf("legacy TUN stack = %v, want gvisor", tun["stack"])
	}

	body, _, err := RenderWithOptions(FormatSingbox, []string{"trojan://pw@1.2.3.4:443?sni=a.com#n"}, nil, "", "", "", RenderOptions{SingboxLegacyTUNStack: true})
	if err != nil {
		t.Fatalf("RenderWithOptions: %v", err)
	}
	if tunInbound(t, body)["stack"] != "gvisor" {
		t.Fatal("RenderWithOptions SingboxLegacyTUNStack did not emit stack=gvisor")
	}

	// With a non-legacy routing profile the toggle must still apply.
	out, err = SingboxWithOptions(ParseLinks([]string{"trojan://pw@1.2.3.4:443?sni=a.com#n"}), "", ProfileCNDirect, SingboxOptions{LegacyTUNStack: true})
	if err != nil {
		t.Fatalf("SingboxWithOptions cn-direct: %v", err)
	}
	if tunInbound(t, out)["stack"] != "gvisor" {
		t.Fatal("cn-direct + LegacyTUNStack did not emit stack=gvisor")
	}
}

func TestWantSingboxLegacyTUNStack(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{
		{"gvisor", true},
		{"Gvisor", true},
		{" gvisor ", true},
		{"", false},
		{"system", false},
		{"1", false},
		{"legacy", false},
	} {
		if got := WantSingboxLegacyTUNStack(tc.in); got != tc.want {
			t.Errorf("WantSingboxLegacyTUNStack(%q)=%v, want %v", tc.in, got, tc.want)
		}
	}
}

// Clash templates keep stack: gvisor — this issue does not change Clash.
func TestClashTemplateStillHasStack(t *testing.T) {
	if !strings.Contains(DefaultClashTemplate, "stack: gvisor") {
		t.Fatal("Clash default template lost stack: gvisor; #52 must leave Clash unchanged")
	}
}

// Sanity: rendered JSON stays valid when stack is omitted.
func TestSingboxDefaultTUNJSONRoundTrip(t *testing.T) {
	out, err := Singbox(ParseLinks([]string{"ss://YWVzLTI1Ni1nY206cHc@1.2.3.4:8388#n"}), "")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	tun := tunInbound(t, out)
	for _, key := range []string{"type", "tag", "address", "auto_route", "strict_route", "route_exclude_address"} {
		if _, ok := tun[key]; !ok {
			t.Errorf("default TUN missing %q", key)
		}
	}
}
