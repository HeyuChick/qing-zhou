package subconv

import (
	"encoding/json"
	"testing"
)

func TestStripEmptyDirectDetour(t *testing.T) {
	doc := map[string]any{
		"dns": map[string]any{
			"servers": []any{
				map[string]any{"tag": "local", "type": "https", "server": "223.5.5.5", "detour": "direct"},
				map[string]any{"tag": "remote", "type": "https", "server": "1.1.1.1", "detour": "proxy"},
			},
		},
		"outbounds": []any{
			map[string]any{"type": "selector", "tag": "proxy"},
			map[string]any{"type": "direct", "tag": "direct"},
		},
	}
	dns := doc["dns"].(map[string]any)
	servers := mapSlice(dns["servers"])
	stripEmptyDirectDetour(doc, servers)
	if _, ok := servers[0]["detour"]; ok {
		t.Fatalf("empty-direct detour must be stripped, got: %v", servers[0])
	}
	if servers[1]["detour"] != "proxy" {
		t.Fatalf("proxy detour must survive, got: %v", servers[1])
	}
}

func TestStripKeepsCustomizedDirect(t *testing.T) {
	doc := map[string]any{
		"dns": map[string]any{
			"servers": []any{
				map[string]any{"tag": "local", "type": "https", "server": "223.5.5.5", "detour": "direct"},
			},
		},
		"outbounds": []any{
			map[string]any{"type": "direct", "tag": "direct", "bind_interface": "eth9"},
		},
	}
	dns := doc["dns"].(map[string]any)
	stripEmptyDirectDetour(doc, mapSlice(dns["servers"]))
	srv := mapSlice(dns["servers"])[0]
	if srv["detour"] != "direct" {
		t.Fatalf("customized direct outbound must keep detour, got: %v", srv)
	}
}

func TestStripIgnoresDifferentDirectTag(t *testing.T) {
	doc := map[string]any{
		"dns": map[string]any{"servers": []any{
			map[string]any{"tag": "local", "type": "https", "server": "223.5.5.5", "detour": "direct"},
		}},
		"outbounds": []any{
			map[string]any{"type": "direct", "tag": "other"},
		},
	}
	dns := doc["dns"].(map[string]any)
	stripEmptyDirectDetour(doc, mapSlice(dns["servers"]))
	if got := mapSlice(dns["servers"])[0]["detour"]; got != "direct" {
		t.Fatalf("unrelated direct outbound changed detour: %v", got)
	}
}

// Existing installations may have saved the previous built-in template in the
// database. It has no outbounds array because the renderer historically owned
// that field, so cleanup must inspect the generated final outbounds rather than
// the template document seen by modernizeSingboxDNS.
func TestRenderedLegacyTemplateStripsEmptyDirectDetour(t *testing.T) {
	tpl := `{
	  "dns": {
	    "servers": [
	      {"tag":"remote", "type":"https", "server":"1.1.1.1", "detour":"proxy"},
	      {"tag":"local", "type":"https", "server":"223.5.5.5", "detour":"direct"}
	    ],
	    "final":"remote"
	  },
	  "route": {"final":"proxy", "default_domain_resolver":"local"}
	}`
	out, err := Singbox(ParseLinks([]string{
		"trojan://pw@node.example.com:443?security=tls&sni=node.example.com#A",
	}), tpl)
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{}
	if err := json.Unmarshal([]byte(out), &doc); err != nil {
		t.Fatal(err)
	}
	dns := doc["dns"].(map[string]any)
	servers := mapSlice(dns["servers"])
	if _, ok := servers[1]["detour"]; ok {
		t.Fatalf("legacy template kept detour to generated empty direct: %v", servers[1])
	}
	if servers[0]["detour"] != "proxy" {
		t.Fatalf("proxy detour was changed: %v", servers[0])
	}
}
