package subconv

import (
	"encoding/json"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// Node names come from a link's #fragment, so they are attacker-controlled
// whenever the panel ingests a remote subscription. These pin the renderers
// against names chosen to collide with, or break out of, the config they land in.

// TestNodeNameCannotTakeReservedTag: sing-box rejects a config with a duplicate
// outbound tag outright, so a node named "direct" colliding with the built-in
// direct outbound would take down every subscriber's profile, not just that node.
func TestNodeNameCannotTakeReservedTag(t *testing.T) {
	for _, name := range []string{"direct", "proxy", "auto"} {
		sb, err := Singbox(ParseLinks([]string{"trojan://pw@1.2.3.4:443?sni=a.com#" + name}), "")
		if err != nil {
			t.Fatalf("name=%q: %v", name, err)
		}
		var doc struct {
			Outbounds []struct {
				Tag string `json:"tag"`
			} `json:"outbounds"`
		}
		if err := json.Unmarshal([]byte(sb), &doc); err != nil {
			t.Fatalf("name=%q: %v", name, err)
		}
		seen := map[string]bool{}
		for _, o := range doc.Outbounds {
			if seen[o.Tag] {
				t.Errorf("name=%q produced a duplicate outbound tag %q", name, o.Tag)
			}
			seen[o.Tag] = true
		}
	}
}

// TestClashNodeNameCannotTakeReservedTag: mihomo pre-seeds DIRECT/REJECT/PASS
// into its proxy map, and the selector names are built by the renderer itself —
// a node claiming one is a duplicate-name error on the whole YAML.
func TestClashNodeNameCannotTakeReservedTag(t *testing.T) {
	for _, name := range []string{"DIRECT", "REJECT", grpSelectClash, grpAutoClash} {
		y, err := Clash(ParseLinks([]string{"trojan://pw@1.2.3.4:443?sni=a.com#" + name}), "")
		if err != nil {
			t.Fatalf("name=%q: %v", name, err)
		}
		var doc struct {
			Proxies []struct {
				Name string `yaml:"name"`
			} `yaml:"proxies"`
		}
		if err := yaml.Unmarshal([]byte(y), &doc); err != nil {
			t.Fatalf("name=%q: %v", name, err)
		}
		for _, p := range doc.Proxies {
			if p.Name == name {
				t.Errorf("node kept the reserved name %q", name)
			}
		}
	}
}

// TestSurgeNameCannotInjectLines: the Surge renderer concatenates strings by
// hand (Clash/sing-box go through yaml/json.Marshal, which escapes for us), so a
// %0A in a remark could close the proxy line and open an attacker-chosen
// directive — a FINAL rule here rewrites where all unmatched traffic goes.
func TestSurgeNameCannotInjectLines(t *testing.T) {
	hostile := []string{
		"trojan://pw@1.2.3.4:443?sni=a.com#x%0AFINAL%2CDIRECT",
		"trojan://pw@5.6.7.8:443?sni=b.com#y%0D%0A%5BRule%5D",
	}
	out := Surge(ParseLinks(hostile), "")
	section := out[strings.Index(out, "[Proxy]")+len("[Proxy]") : strings.Index(out, "[Proxy Group]")]
	for _, line := range strings.Split(strings.TrimSpace(section), "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		if !strings.Contains(line, " = ") {
			t.Errorf("[Proxy] line is not a proxy declaration — name broke out:\n%s", line)
		}
	}
	if strings.Contains(out, "FINAL,DIRECT") {
		t.Errorf("injected FINAL rule reached the output:\n%s", out)
	}
}

// TestSurgeNameCannotCommentOutNode: a leading # or ; makes Surge treat the
// whole line as a comment, so the node vanishes while the proxy groups built
// from the same name still reference it — a dangling reference, not a clean drop.
func TestSurgeNameCannotCommentOutNode(t *testing.T) {
	out := Surge(ParseLinks([]string{"trojan://pw@1.2.3.4:443?sni=a.com#%23hidden"}), "")
	section := out[strings.Index(out, "[Proxy]")+len("[Proxy]") : strings.Index(out, "[Proxy Group]")]
	if !strings.Contains(section, " = trojan") {
		t.Errorf("node was commented out of [Proxy]:\n%s", out)
	}
}

// TestTemplateRouteExcludeAddressPreserved: a custom template arrives via
// json.Unmarshal, so its arrays are []any — a []string type assertion silently
// yields nil and the admin's own TUN exclusions get overwritten with just the
// node IPs, quietly routing whatever they had carved out back into the tunnel.
func TestTemplateRouteExcludeAddressPreserved(t *testing.T) {
	tpl := `{"inbounds":[{"type":"tun","tag":"tun-in","route_exclude_address":["10.0.0.0/8","192.168.1.5/32"]}],"outbounds":[]}`

	sb, err := Singbox(ParseLinks([]string{"trojan://pw@1.2.3.4:443?sni=a.com#n"}), tpl)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Inbounds []struct {
			RouteExcludeAddress []string `json:"route_exclude_address"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal([]byte(sb), &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Inbounds) != 1 {
		t.Fatalf("expected the template's single tun inbound, got %d", len(doc.Inbounds))
	}
	got := map[string]bool{}
	for _, a := range doc.Inbounds[0].RouteExcludeAddress {
		got[a] = true
	}
	for _, want := range []string{"10.0.0.0/8", "192.168.1.5/32", "1.2.3.4/32"} {
		if !got[want] {
			t.Errorf("route_exclude_address lost %q, has %v", want, doc.Inbounds[0].RouteExcludeAddress)
		}
	}
}
