package store

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"qingzhou/internal/singbox"
)

// TestRelayChaining exercises the full relay wiring: a relay inbound on server 1
// pointing at a landing inbound on server 2 must (a) get an upstream outbound +
// route rule in server 1's config, and (b) inject a matching relay user into the
// landing inbound in server 2's config.
func TestRelayChaining(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}

	relaySrv, err := st.CreateServer(Server{Name: "relay", Host: "1.2.3.4", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	landingSrv, err := st.CreateServer(Server{Name: "landing", Host: "5.6.7.8", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	// Landing inbound (trojan) on the landing server — exits to internet.
	landingID, err := st.SaveSbInbound(&SbInbound{
		ServerID: landingSrv, Type: "trojan", Tag: "L-trojan", ListenPort: 8443,
		Options: `{}`, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Relay inbound (vless) on server 1 → forwards to the landing inbound.
	relayID, err := st.SaveSbInbound(&SbInbound{
		ServerID: relaySrv, Type: "vless", Tag: "R-vless", ListenPort: 443,
		Options: `{}`, Enabled: true, UpstreamInboundID: landingID,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = relayID

	users := map[string][]singbox.User{
		"R-vless":  {{Name: "u1", UUID: "11111111-1111-1111-1111-111111111111"}},
		"L-trojan": {{Name: "u1", Password: "pw"}},
	}

	// --- Server 1 (relay): must carry the upstream outbound + route rule. ---
	relayCfg, err := st.BuildSingboxConfigForServer(relaySrv, singbox.DefaultBaseConfig, "", users)
	if err != nil {
		t.Fatal(err)
	}
	var rc map[string]any
	if err := json.Unmarshal(relayCfg, &rc); err != nil {
		t.Fatal(err)
	}
	wantTag := "relay-to-" + itoa(landingID)
	if !hasOutbound(rc, wantTag) {
		t.Errorf("relay server config missing upstream outbound %q:\n%s", wantTag, relayCfg)
	}
	if !hasRouteToOutbound(rc, wantTag, "R-vless") {
		t.Errorf("relay server config missing route rule R-vless -> %q:\n%s", wantTag, relayCfg)
	}

	// --- Server 2 (landing): must inject the relay user into the landing inbound. ---
	landingCfg, err := st.BuildSingboxConfigForServer(landingSrv, singbox.DefaultBaseConfig, "", users)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(landingCfg), "relay_"+itoa(landingID)) {
		t.Errorf("landing config missing injected relay user relay_%d:\n%s", landingID, landingCfg)
	}

	// The relay credential must be identical on both ends.
	ib, _ := st.GetSbInbound(landingID)
	if ib.RelaySecret == "" {
		t.Fatal("landing inbound relay_secret was not persisted")
	}
	// Landing is trojan, so its injected user (and the relay outbound) authenticate
	// with the derived password — the same value must appear on both ends.
	_, wantPassword := relayCred(ib.RelaySecret)
	if !strings.Contains(string(landingCfg), wantPassword) {
		t.Errorf("landing relay user password missing (want %s):\n%s", wantPassword, landingCfg)
	}
	if !strings.Contains(string(relayCfg), wantPassword) {
		t.Errorf("relay outbound password mismatch (want %s):\n%s", wantPassword, relayCfg)
	}
}

func itoa(n int64) string {
	return strings.TrimSpace(string(jsonNumber(n)))
}
func jsonNumber(n int64) []byte { b, _ := json.Marshal(n); return b }

func hasOutbound(cfg map[string]any, tag string) bool {
	obs, _ := cfg["outbounds"].([]any)
	for _, o := range obs {
		if m, ok := o.(map[string]any); ok && m["tag"] == tag {
			return true
		}
	}
	return false
}

func hasRouteToOutbound(cfg map[string]any, outboundTag, inboundTag string) bool {
	route, _ := cfg["route"].(map[string]any)
	rules, _ := route["rules"].([]any)
	for _, r := range rules {
		m, ok := r.(map[string]any)
		if !ok || m["outbound"] != outboundTag {
			continue
		}
		ins, _ := m["inbound"].([]any)
		for _, in := range ins {
			if in == inboundTag {
				return true
			}
		}
	}
	return false
}
