package subconv

import "testing"

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
