package singbox

import (
	"encoding/json"
	"testing"
)

// A mixed (HTTP/SOCKS5) inbound authenticates by username/password — not the
// name/uuid shape the circumvention protocols use. Its username must equal the
// 轻舟 user identity so the v2ray_api stats key matches for per-user metering.
func TestRenderUserMixed(t *testing.T) {
	u := User{Name: "qz_alice_pkg1", UUID: "ignored", Password: "s3cret"}
	got := renderUser("mixed", u, map[string]interface{}{})
	if got["username"] != "qz_alice_pkg1" {
		t.Fatalf("username = %v, want qz_alice_pkg1", got["username"])
	}
	if got["password"] != "s3cret" {
		t.Fatalf("password = %v, want s3cret", got["password"])
	}
	if _, ok := got["uuid"]; ok {
		t.Fatalf("mixed user must not carry a uuid field: %v", got)
	}
	if _, ok := got["name"]; ok {
		t.Fatalf("mixed user must use username, not name: %v", got)
	}
}

// The generated config must place the mixed inbound with its users[] and list
// the username under experimental.v2ray_api.stats.users, or sing-box won't meter
// its traffic per user.
func TestGenerateConfigMixed(t *testing.T) {
	ib := Inbound{
		Type: "mixed",
		Base: map[string]interface{}{
			"type": "mixed", "tag": "mixed-proxy", "listen": "::", "listen_port": 7890,
		},
		Users: []User{{Name: "qz_alice_pkg1", Password: "s3cret"}},
	}
	raw, err := GenerateConfig(json.RawMessage(DefaultBaseConfig), []Inbound{ib}, "127.0.0.1:18080")
	if err != nil {
		t.Fatalf("GenerateConfig: %v", err)
	}
	var cfg struct {
		Inbounds []struct {
			Type  string `json:"type"`
			Users []struct {
				Username string `json:"username"`
				Password string `json:"password"`
			} `json:"users"`
		} `json:"inbounds"`
		Experimental struct {
			V2RayAPI struct {
				Stats struct {
					Users []string `json:"users"`
				} `json:"stats"`
			} `json:"v2ray_api"`
		} `json:"experimental"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal generated config: %v\n%s", err, raw)
	}
	if len(cfg.Inbounds) != 1 || len(cfg.Inbounds[0].Users) != 1 {
		t.Fatalf("want 1 inbound with 1 user, got %s", raw)
	}
	if u := cfg.Inbounds[0].Users[0]; u.Username != "qz_alice_pkg1" || u.Password != "s3cret" {
		t.Fatalf("mixed user rendered wrong: %+v", u)
	}
	if got := cfg.Experimental.V2RayAPI.Stats.Users; len(got) != 1 || got[0] != "qz_alice_pkg1" {
		t.Fatalf("stats.users = %v, want [qz_alice_pkg1] for per-user metering", got)
	}
}

// A mixed inbound with no entitled users must NOT be emitted: sing-box treats an
// empty users[] as no-auth, which would run it as an open public proxy. Assert
// the inbound is dropped from the config entirely during that entitlement gap.
func TestGenerateConfigMixedNoUsersDropped(t *testing.T) {
	ib := Inbound{
		Type: "mixed",
		Base: map[string]interface{}{
			"type": "mixed", "tag": "mixed-proxy", "listen": "::", "listen_port": 7890,
		},
		Users: nil, // nobody entitled
	}
	raw, err := GenerateConfig(json.RawMessage(DefaultBaseConfig), []Inbound{ib}, "127.0.0.1:18080")
	if err != nil {
		t.Fatalf("GenerateConfig: %v", err)
	}
	var cfg struct {
		Inbounds []map[string]interface{} `json:"inbounds"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cfg.Inbounds) != 0 {
		t.Fatalf("userless mixed inbound must be dropped (open-proxy risk), got:\n%s", raw)
	}
	// A circumvention protocol with empty users IS still emitted (harmless).
	vl := Inbound{Type: "vless", Base: map[string]interface{}{"type": "vless", "tag": "v", "listen_port": 443}}
	raw2, _ := GenerateConfig(json.RawMessage(DefaultBaseConfig), []Inbound{vl}, "")
	_ = json.Unmarshal(raw2, &cfg)
	if len(cfg.Inbounds) != 1 {
		t.Fatalf("userless vless should still be emitted, got:\n%s", raw2)
	}
}
