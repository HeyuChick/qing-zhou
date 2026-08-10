package store

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"qingzhou/internal/singbox"
)

// fixtureEgressPassword stands in for a proxy credential in these tests. Named
// rather than written inline next to a Password: field, because an opaque
// literal in that position is indistinguishable from a genuine leaked
// credential — to a reviewer skimming the diff and to the repo's secret scanner
// alike, and the scanner is the one that blocks the merge.
const fixtureEgressPassword = "fixture-egress-password"

func newEgressTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	st.SetSecretKey([]byte("test-secret"))
	return st
}

// TestEffectiveUDPMode pins the "" = decide-by-type rule, including the reason
// http differs: sing-box's http outbound has no UDP path, so passthrough there
// would only describe a failure rather than cause a different one.
func TestEffectiveUDPMode(t *testing.T) {
	for _, tc := range []struct {
		name, typ, stored, want string
	}{
		{"socks unset defaults to passthrough", "socks", "", UDPModePassthrough},
		{"http unset defaults to block", "http", "", UDPModeBlock},
		{"explicit block on socks", "socks", UDPModeBlock, UDPModeBlock},
		{"explicit passthrough on socks", "socks", UDPModePassthrough, UDPModePassthrough},
		{"unknown value falls back like unset", "http", "sideways", UDPModeBlock},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := &SbEgress{Type: tc.typ, UDPMode: tc.stored}
			if got := e.EffectiveUDPMode(); got != tc.want {
				t.Errorf("EffectiveUDPMode() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestEffectiveConnectTimeout(t *testing.T) {
	if got := (&SbEgress{}).EffectiveConnectTimeoutMS(); got != defaultEgressConnectTimeoutMS {
		t.Errorf("unset timeout = %d, want the default %d", got, defaultEgressConnectTimeoutMS)
	}
	if got := (&SbEgress{ConnectTimeoutMS: 1200}).EffectiveConnectTimeoutMS(); got != 1200 {
		t.Errorf("explicit timeout = %d, want 1200", got)
	}
}

// TestEgressUDPBlockRuleOrder is the regression test for the UDP policy.
//
// Route rules are first-match, so a reject placed after the steering rule is
// dead weight and the UDP reaches the proxy anyway. What the config must show
// is the reject for this inbound sitting strictly ahead of its steering rule.
func TestEgressUDPBlockRuleOrder(t *testing.T) {
	st := newEgressTestStore(t)

	blockID, err := st.SaveSbEgress(&SbEgress{
		Name: "阻断UDP", Type: "socks", Host: "9.9.9.9", Port: 1080, UDPMode: UDPModeBlock,
	})
	if err != nil {
		t.Fatal(err)
	}
	passID, err := st.SaveSbEgress(&SbEgress{
		Name: "透传UDP", Type: "socks", Host: "8.8.8.8", Port: 1080, UDPMode: UDPModePassthrough,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, ib := range []struct {
		tag  string
		port int
		eg   int64
	}{{"udp-blocked", 443, blockID}, {"udp-open", 444, passID}} {
		if _, err := st.SaveSbInbound(&SbInbound{
			Type: "vless", Tag: ib.tag, ListenPort: ib.port, Options: `{}`, Enabled: true, EgressID: ib.eg,
		}); err != nil {
			t.Fatal(err)
		}
	}

	user := []singbox.User{{Name: "u1", UUID: "11111111-1111-1111-1111-111111111111"}}
	cfgBytes, err := st.BuildSingboxConfig(singbox.DefaultBaseConfig, "",
		map[string][]singbox.User{"udp-blocked": user, "udp-open": user})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(cfgBytes, &cfg); err != nil {
		t.Fatal(err)
	}
	route, _ := cfg["route"].(map[string]any)
	rules, _ := route["rules"].([]any)

	rejectAt, steerAt := -1, -1
	for i, r := range rules {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		ins, _ := m["inbound"].([]any)
		if len(ins) != 1 || ins[0] != "udp-blocked" {
			// The passthrough egress must not have produced a reject at all.
			if m["action"] == "reject" && m["network"] == "udp" {
				if len(ins) == 1 && ins[0] == "udp-open" {
					t.Errorf("passthrough egress must not block UDP:\n%s", cfgBytes)
				}
			}
			continue
		}
		if m["action"] == "reject" && m["network"] == "udp" {
			rejectAt = i
		}
		if m["outbound"] == "egress-"+itoa(blockID) {
			steerAt = i
		}
	}
	if rejectAt < 0 {
		t.Fatalf("no UDP reject rule for the blocking egress:\n%s", cfgBytes)
	}
	if steerAt < 0 {
		t.Fatalf("no steering rule for the blocking egress:\n%s", cfgBytes)
	}
	if rejectAt > steerAt {
		t.Errorf("UDP reject at %d is behind the steering rule at %d — first-match means it never runs:\n%s",
			rejectAt, steerAt, cfgBytes)
	}
}

// TestEgressUDPBlockCoversEveryBoundInbound covers the grouping path: several
// inbounds sharing one egress collapse onto a single outbound, and the tag list
// is appended to AFTER the Relay (and its RejectUDP flag) was constructed for
// the first one. An inbound added later must end up in the reject rule too —
// otherwise the second and subsequent inbounds on a UDP-blocking egress quietly
// keep sending UDP into a proxy that cannot carry it.
func TestEgressUDPBlockCoversEveryBoundInbound(t *testing.T) {
	st := newEgressTestStore(t)

	egID, err := st.SaveSbEgress(&SbEgress{
		Name: "共享出口", Type: "socks", Host: "9.9.9.9", Port: 1080, UDPMode: UDPModeBlock,
	})
	if err != nil {
		t.Fatal(err)
	}
	tags := []string{"share-a", "share-b", "share-c"}
	for i, tag := range tags {
		if _, err := st.SaveSbInbound(&SbInbound{
			Type: "vless", Tag: tag, ListenPort: 9000 + i, Options: `{}`, Enabled: true, EgressID: egID,
		}); err != nil {
			t.Fatal(err)
		}
	}
	users := map[string][]singbox.User{}
	for _, tag := range tags {
		users[tag] = []singbox.User{{Name: "u1", UUID: "11111111-1111-1111-1111-111111111111"}}
	}
	cfgBytes, err := st.BuildSingboxConfig(singbox.DefaultBaseConfig, "", users)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(cfgBytes, &cfg); err != nil {
		t.Fatal(err)
	}
	route, _ := cfg["route"].(map[string]any)
	rules, _ := route["rules"].([]any)

	var rejectTags, steerTags []string
	rejectAt, steerAt := -1, -1
	for i, r := range rules {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		// The private-space reject carries no inbound list at all, so this must
		// be a checked assertion.
		ins, _ := m["inbound"].([]any)
		var got []string
		for _, in := range ins {
			if s, ok := in.(string); ok {
				got = append(got, s)
			}
		}
		if m["action"] == "reject" && m["network"] == "udp" {
			rejectTags, rejectAt = got, i
		}
		if m["outbound"] == "egress-"+itoa(egID) {
			steerTags, steerAt = got, i
		}
	}
	if rejectAt < 0 || steerAt < 0 {
		t.Fatalf("missing reject or steering rule:\n%s", cfgBytes)
	}
	if rejectAt > steerAt {
		t.Errorf("reject at %d is behind steering at %d", rejectAt, steerAt)
	}
	// One outbound shared, so one rule of each — listing every bound inbound.
	if len(rejectTags) != len(tags) || len(steerTags) != len(tags) {
		t.Fatalf("reject=%v steer=%v, want all of %v", rejectTags, steerTags, tags)
	}
	for _, want := range tags {
		found := false
		for _, got := range rejectTags {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Errorf("inbound %q is bound to a UDP-blocking egress but not in the reject rule: %v", want, rejectTags)
		}
	}
}

// TestEgressConnectTimeoutEmitted checks the dial bound reaches the outbound,
// including that the stored millisecond value becomes a sing-box duration.
func TestEgressConnectTimeoutEmitted(t *testing.T) {
	st := newEgressTestStore(t)

	defID, err := st.SaveSbEgress(&SbEgress{Name: "默认", Type: "socks", Host: "9.9.9.9", Port: 1080})
	if err != nil {
		t.Fatal(err)
	}
	customID, err := st.SaveSbEgress(&SbEgress{
		Name: "自定义", Type: "socks", Host: "8.8.8.8", Port: 1080, ConnectTimeoutMS: 1500,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, ib := range []struct {
		tag  string
		port int
		eg   int64
	}{{"a", 443, defID}, {"b", 444, customID}} {
		if _, err := st.SaveSbInbound(&SbInbound{
			Type: "vless", Tag: ib.tag, ListenPort: ib.port, Options: `{}`, Enabled: true, EgressID: ib.eg,
		}); err != nil {
			t.Fatal(err)
		}
	}
	user := []singbox.User{{Name: "u1", UUID: "11111111-1111-1111-1111-111111111111"}}
	cfgBytes, err := st.BuildSingboxConfig(singbox.DefaultBaseConfig, "",
		map[string][]singbox.User{"a": user, "b": user})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(cfgBytes, &cfg); err != nil {
		t.Fatal(err)
	}
	find := func(tag string) map[string]any {
		obs, _ := cfg["outbounds"].([]any)
		for _, o := range obs {
			if m, ok := o.(map[string]any); ok && m["tag"] == tag {
				return m
			}
		}
		return nil
	}
	if got := find("egress-" + itoa(defID))["connect_timeout"]; got != "5000ms" {
		t.Errorf("default connect_timeout = %v, want 5000ms", got)
	}
	if got := find("egress-" + itoa(customID))["connect_timeout"]; got != "1500ms" {
		t.Errorf("custom connect_timeout = %v, want 1500ms", got)
	}
}

// TestCloneSbEgress is the regression test for the reason cloning lives on the
// server. The API masks the password as "***" on the way out and only honours
// that sentinel when an id is present, so a client-side copy-and-create would
// silently store three asterisks. A clone must carry the real secret.
func TestCloneSbEgress(t *testing.T) {
	st := newEgressTestStore(t)

	srcID, err := st.SaveSbEgress(&SbEgress{
		Name: "静态IP-香港", Type: "http", Host: "1.2.3.4", Port: 8080,
		Username: "user1", Password: fixtureEgressPassword, TLSEnabled: true, SNI: "proxy.example.com",
		UDPMode: UDPModeBlock, ConnectTimeoutMS: 2500,
	})
	if err != nil {
		t.Fatal(err)
	}

	cloned, err := st.CloneSbEgress(srcID)
	if err != nil || cloned == nil {
		t.Fatalf("CloneSbEgress: %+v, %v", cloned, err)
	}
	if cloned.ID == srcID {
		t.Fatal("clone reused the source id")
	}
	if cloned.Password != fixtureEgressPassword {
		t.Errorf("clone password = %q, want the source's plaintext", cloned.Password)
	}
	if cloned.Name != "静态IP-香港（副本）" {
		t.Errorf("clone name = %q", cloned.Name)
	}
	if cloned.Type != "http" || cloned.Host != "1.2.3.4" || cloned.Port != 8080 ||
		cloned.Username != "user1" || !cloned.TLSEnabled || cloned.SNI != "proxy.example.com" ||
		cloned.UDPMode != UDPModeBlock || cloned.ConnectTimeoutMS != 2500 {
		t.Errorf("clone did not copy every field: %+v", cloned)
	}
	// The copy must be encrypted at rest like any other row, not written back
	// as the plaintext the clone path had in hand.
	var raw string
	if err := st.db.QueryRow(`SELECT password FROM sb_egresses WHERE id=?`, cloned.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if raw == fixtureEgressPassword {
		t.Error("cloned password stored in cleartext")
	}

	// Cloning twice must not produce two rows with the same name.
	second, err := st.CloneSbEgress(srcID)
	if err != nil || second == nil {
		t.Fatalf("second CloneSbEgress: %v", err)
	}
	if second.Name == cloned.Name {
		t.Errorf("two clones share the name %q", second.Name)
	}

	// A source whose password cannot be decrypted must be refused rather than
	// duplicated into a second row that fails the same way.
	st.SetSecretKey([]byte("different-key"))
	if _, err := st.CloneSbEgress(srcID); err == nil {
		t.Error("cloning an undecryptable egress should fail")
	}
}

// TestCloneSbEgressLongName pins the name cap. The cap used to be applied to
// the finished candidate, which broke twice over on a long Chinese name: the
// byte cut landed inside a character, and — worse — it took the （副本）suffix
// back off, so both clones came out holding the same truncated prefix while the
// uniqueness loop that was supposed to prevent exactly that had already passed.
func TestCloneSbEgressLongName(t *testing.T) {
	st := newEgressTestStore(t)

	long := strings.Repeat("香", 120) // 360 bytes, well past the old 200-byte cut
	srcID, err := st.SaveSbEgress(&SbEgress{
		Name: long, Type: "socks", Host: "1.2.3.4", Port: 1080, Password: fixtureEgressPassword,
	})
	if err != nil {
		t.Fatal(err)
	}

	first, err := st.CloneSbEgress(srcID)
	if err != nil || first == nil {
		t.Fatalf("CloneSbEgress: %v", err)
	}
	second, err := st.CloneSbEgress(srcID)
	if err != nil || second == nil {
		t.Fatalf("second CloneSbEgress: %v", err)
	}

	for _, n := range []string{first.Name, second.Name} {
		if !utf8.ValidString(n) {
			t.Errorf("clone name is not valid UTF-8: %q", n)
		}
		if !strings.Contains(n, "（副本") {
			t.Errorf("clone name lost its 副本 suffix: %q", n)
		}
	}
	if first.Name == second.Name {
		t.Errorf("two clones of a long name share %q", first.Name)
	}
}

// TestRelayHopCarriesMultiplex covers fix A: the relay→landing hop used to be
// the one dial that never mirrored the landing inbound's multiplex setting, so
// every short connection through a chain paid a full extra handshake.
func TestRelayHopCarriesMultiplex(t *testing.T) {
	st := newEgressTestStore(t)

	// A landing that a relay can actually dial: vless over plain TLS, no vision
	// (sing-box rejects multiplex together with the vision flow).
	tlsID, err := st.SaveSbTls(&SbTls{
		Name:       "landing-tls",
		ServerJSON: `{"enabled":true,"server_name":"landing.example.com"}`,
		ClientJSON: `{"insecure":true}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	landingID, err := st.SaveSbInbound(&SbInbound{
		Type: "vless", Tag: "landing", ListenPort: 443, TlsID: tlsID, ServerID: 0,
		Options: `{"multiplex":{"enabled":true,"brutal":{"enabled":true,"up_mbps":100,"down_mbps":200}}}`,
		Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveSbInbound(&SbInbound{
		Type: "vless", Tag: "relay", ListenPort: 8443, TlsID: tlsID, ServerID: 0,
		Options: `{}`, Enabled: true, UpstreamInboundID: landingID,
	}); err != nil {
		t.Fatal(err)
	}

	user := []singbox.User{{Name: "u1", UUID: "11111111-1111-1111-1111-111111111111"}}
	cfgBytes, err := st.BuildSingboxConfig(singbox.DefaultBaseConfig, "",
		map[string][]singbox.User{"landing": user, "relay": user})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(cfgBytes, &cfg); err != nil {
		t.Fatal(err)
	}
	var ob map[string]any
	obs, _ := cfg["outbounds"].([]any)
	for _, o := range obs {
		if m, ok := o.(map[string]any); ok && m["tag"] == "relay-to-"+itoa(landingID) {
			ob = m
		}
	}
	if ob == nil {
		t.Fatalf("no relay outbound in config:\n%s", cfgBytes)
	}
	mx, _ := ob["multiplex"].(map[string]any)
	if mx == nil || mx["enabled"] != true {
		t.Fatalf("relay hop did not mirror the landing's multiplex: %v", ob)
	}
	// Brutal is deliberately not mirrored: its bandwidths describe a subscriber's
	// line, and pacing a relay machine to those numbers is worse than not pacing.
	if _, ok := mx["brutal"]; ok {
		t.Errorf("relay hop must not carry brutal: %v", mx)
	}
}

// TestRelayHopWithoutMultiplexStaysPlain guards the other direction: enabling
// multiplex must remain the landing inbound's decision, not something the relay
// wiring turns on for everyone.
func TestRelayHopWithoutMultiplexStaysPlain(t *testing.T) {
	st := newEgressTestStore(t)

	tlsID, err := st.SaveSbTls(&SbTls{
		Name:       "landing-tls",
		ServerJSON: `{"enabled":true,"server_name":"landing.example.com"}`,
		ClientJSON: `{"insecure":true}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	landingID, err := st.SaveSbInbound(&SbInbound{
		Type: "vless", Tag: "landing", ListenPort: 443, TlsID: tlsID,
		Options: `{}`, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveSbInbound(&SbInbound{
		Type: "vless", Tag: "relay", ListenPort: 8443, TlsID: tlsID,
		Options: `{}`, Enabled: true, UpstreamInboundID: landingID,
	}); err != nil {
		t.Fatal(err)
	}
	user := []singbox.User{{Name: "u1", UUID: "11111111-1111-1111-1111-111111111111"}}
	cfgBytes, err := st.BuildSingboxConfig(singbox.DefaultBaseConfig, "",
		map[string][]singbox.User{"landing": user, "relay": user})
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(cfgBytes, &cfg); err != nil {
		t.Fatal(err)
	}
	obs, _ := cfg["outbounds"].([]any)
	for _, o := range obs {
		m, ok := o.(map[string]any)
		if !ok || m["tag"] != "relay-to-"+itoa(landingID) {
			continue
		}
		if _, has := m["multiplex"]; has {
			t.Errorf("relay hop enabled multiplex the landing never asked for: %v", m)
		}
	}
}
