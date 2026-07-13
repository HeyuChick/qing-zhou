package store

import (
	"path/filepath"
	"strings"
	"testing"

	"qingzhou/internal/singbox"
)

// A mixed (HTTP/SOCKS5) inbound must render into the server config with its
// per-user username/password and have that username tracked in v2ray_api stats,
// or 轻舟 can't meter the proxy's traffic per user (users would bypass quota).
func TestMixedInboundConfig(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}

	if _, err := st.SaveSbInbound(&SbInbound{
		Type: "mixed", Tag: "mixed-proxy", Listen: "::", ListenPort: 7890,
		Options: `{}`, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	users := map[string][]singbox.User{
		"mixed-proxy": {{Name: "qz_alice", Password: "s3cret"}},
	}
	cfg, err := st.BuildSingboxConfig(singbox.DefaultBaseConfig, "127.0.0.1:18080", users)
	if err != nil {
		t.Fatal(err)
	}
	s := string(cfg)
	for _, want := range []string{`"type": "mixed"`, `"username": "qz_alice"`, `"password": "s3cret"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("mixed inbound config missing %s:\n%s", want, s)
		}
	}
	// The username must also appear in experimental.v2ray_api.stats.users so
	// sing-box tracks per-user traffic for this inbound.
	if i := strings.Index(s, "v2ray_api"); i < 0 || !strings.Contains(s[i:], `"qz_alice"`) {
		t.Fatalf("mixed inbound username not tracked in v2ray_api stats:\n%s", s)
	}
}
