package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"qingzhou/internal/singbox"
)

func activateMeteringFixture(t *testing.T, landingProtocol ...string) (*Store, int64, int64, int64, map[string][]singbox.User) {
	t.Helper()
	st, a, b, _, bi, users := meteringRelayFixture(t)
	if len(landingProtocol) > 0 {
		target, err := st.GetSbInbound(bi)
		if err != nil {
			t.Fatal(err)
		}
		target.Type = landingProtocol[0]
		if _, err = st.SaveSbInbound(target); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.PrepareRelayMetering(); err != nil {
		t.Fatal(err)
	}
	landing, err := st.BuildSingboxConfigForServer(b, singbox.DefaultBaseConfig, "127.0.0.1:18080", users)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.RecordRelayConfigApplied(b, landing); err != nil {
		t.Fatal(err)
	}
	entry, err := st.BuildSingboxConfigForServer(a, singbox.DefaultBaseConfig, "127.0.0.1:18080", users)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.RecordRelayConfigApplied(a, entry); err != nil {
		t.Fatal(err)
	}
	return st, a, b, bi, users
}
func recordDrainedPolls(t *testing.T, st *Store, serverID int64) {
	t.Helper()
	for i := int64(1); i <= 2; i++ {
		p := NewTrafficPoll(serverID, map[string]UsageDelta{})
		p.ObservedAt = time.Now().Unix() + i
		if _, err := st.RecordTrafficPoll(p); err != nil {
			t.Fatal(err)
		}
	}
}
func TestLegacyRelayRetirementDrainAndSafeRestore(t *testing.T) {
	st, _, b, bi, users := activateMeteringFixture(t)
	v := RelayCredentialView{Kind: "legacy", ServerID: b, InboundID: bi}
	if err := st.ChangeRelayCredential(1, v, "retire"); err == nil {
		t.Fatal("retired before counter drain")
	}
	recordDrainedPolls(t, st, b)
	if err := st.ChangeRelayCredential(1, v, "retire"); err != nil {
		t.Fatal(err)
	}
	if err := st.ConfigureTrafficMetering(false, false); err == nil {
		t.Fatal("disabled while rollback credential unavailable")
	}
	raw, err := st.BuildSingboxConfigForServer(b, singbox.DefaultBaseConfig, "127.0.0.1:18080", users)
	if err != nil {
		t.Fatal(err)
	}
	assertLegacyRelayConfigUser(t, st, raw, b, bi, false)
	if err = st.RecordRelayConfigApplied(b, raw); err != nil {
		t.Fatal(err)
	}
	state, _ := st.LegacyRelayCompatibility(b, bi)
	if state != "retired" {
		t.Fatalf("phase %s", state)
	}
	if err = st.ChangeRelayCredential(1, v, "restore"); err != nil {
		t.Fatal(err)
	}
	if err = st.ConfigureTrafficMetering(false, false); err == nil {
		t.Fatal("disabled before restore applied")
	}
	restored, err := st.BuildSingboxConfigForServer(b, singbox.DefaultBaseConfig, "127.0.0.1:18080", users)
	if err != nil {
		t.Fatal(err)
	}
	assertLegacyRelayConfigUser(t, st, restored, b, bi, true)
	if err = st.RecordRelayConfigApplied(b, restored); err != nil {
		t.Fatal(err)
	}
	if err = st.ConfigureTrafficMetering(false, false); err != nil {
		t.Fatal(err)
	}
	var events int
	if err = st.DB().QueryRow(`SELECT COUNT(*) FROM relay_credential_audit`).Scan(&events); err != nil || events != 2 {
		t.Fatalf("audit %d %v", events, err)
	}
}
func TestRetireOneGenerationKeepsHistoricalAttribution(t *testing.T) {
	st, a, b, bi, users := activateMeteringFixture(t)
	old, _ := st.RelayMeteringLinks()
	oldName := old[0].IdentityName
	target, _ := st.GetSbInbound(bi)
	target.ListenPort++
	if _, err := st.SaveSbInbound(target); err != nil {
		t.Fatal(err)
	}
	if err := st.PrepareRelayMetering(); err != nil {
		t.Fatal(err)
	}
	landing, _ := st.BuildSingboxConfigForServer(b, singbox.DefaultBaseConfig, "127.0.0.1:18080", users)
	if err := st.RecordRelayConfigApplied(b, landing); err != nil {
		t.Fatal(err)
	}
	entry, err := st.BuildSingboxConfigForServer(a, singbox.DefaultBaseConfig, "127.0.0.1:18080", users)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.RecordRelayConfigApplied(a, entry); err != nil {
		t.Fatal(err)
	}
	recordDrainedPolls(t, st, b)
	v := RelayCredentialView{Kind: "generation", LinkID: old[0].ID, ServerID: b, InboundID: bi, Generation: 1}
	if err = st.ChangeRelayCredential(1, v, "retire"); err != nil {
		t.Fatal(err)
	}
	raw, err := st.BuildSingboxConfigForServer(b, singbox.DefaultBaseConfig, "127.0.0.1:18080", users)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), oldName) {
		t.Fatal("retired generation still accepted")
	}
	if _, err = st.RecordTrafficPoll(NewTrafficPoll(b, map[string]UsageDelta{oldName: {Down: 20}})); err != nil {
		t.Fatal(err)
	}
	report, err := st.ServerServiceTraffic(b, 0)
	if err != nil || report.Sources[0].Kind != "relay_link" {
		t.Fatal("retirement erased old mapping")
	}
	v.Generation = 2
	if err = st.ChangeRelayCredential(1, v, "retire"); err == nil {
		t.Fatal("current generation could be retired")
	}
}

func TestLegacyRelayRetirementSeparatesCustomerUsageFromSharedDrain(t *testing.T) {
	st, _, serverID, inboundID, users := activateMeteringFixture(t)
	oldName := fmt.Sprintf("relay_%d", inboundID)
	newName, err := legacyRelayStatsNameWith(st.db, serverID, inboundID)
	if err != nil || newName == oldName {
		t.Fatalf("fixture has no separated shared name: %q %v", newName, err)
	}
	pkg := mkPlan(t, st, "numeric-customer", 1, 10, 30)
	uid, bucketID := trafficCompatCustomer(t, st, pkg, "account", oldName)
	const mixedTag = "retirement-customer-mixed"
	if _, err = st.SaveSbInbound(&SbInbound{ServerID: serverID, Type: "mixed", Tag: mixedTag, ListenPort: 17891, Options: `{}`, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	users[mixedTag] = []singbox.User{{Name: oldName, Password: "compat-fixture"}}
	raw, err := st.BuildSingboxConfigForServer(serverID, singbox.DefaultBaseConfig, "127.0.0.1:18080", users)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.RecordRelayConfigApplied(serverID, raw); err != nil {
		t.Fatal(err)
	}
	const epoch = "separated-retirement-fixture"
	if err = st.RecordRelayNamespaceEpoch(serverID, epoch, raw); err != nil {
		t.Fatal(err)
	}
	v := RelayCredentialView{Kind: "legacy", ServerID: serverID, InboundID: inboundID}
	base := time.Now().Unix() + 10
	poll := func(offset int64, name, process string, down int64) TrafficPoll {
		t.Helper()
		values := map[string]UsageDelta{}
		if name != "" {
			values[name] = UsageDelta{Down: down}
		}
		p := NewTrafficPoll(serverID, values)
		p.ObservedAt, p.Epoch = base+offset, process
		trafficCompatRecord(t, st, p)
		return p
	}
	poll(1, "", epoch, 0)
	poll(2, "", epoch, 0)
	customer := poll(3, oldName, epoch, 23)
	trafficCompatSource(t, st, customer, oldName, "direct_user", "", uid)
	if err = st.canRetireRelayCredential(st.db, v); err != nil {
		t.Fatalf("proven numeric customer's traffic blocked unrelated shared retirement: %v", err)
	}
	shared := poll(4, newName, epoch, 100)
	trafficCompatSource(t, st, shared, newName, "legacy_shared_relay", "", 0)
	if err = st.canRetireRelayCredential(st.db, v); err == nil {
		t.Fatal("new shared statistics name was ignored by the drain boundary")
	}
	poll(5, oldName, epoch, 39)
	if err = st.canRetireRelayCredential(st.db, v); err == nil {
		t.Fatal("one successful collection after real shared traffic was enough to retire")
	}
	poll(6, oldName, epoch, 41)
	if err = st.canRetireRelayCredential(st.db, v); err != nil {
		t.Fatalf("ongoing numeric customer prevented two successful shared-drain collections: %v", err)
	}
	// A late unproven old-process sample is still potentially shared traffic;
	// removing the broad-name check must not silently exempt that uncertainty.
	unknown := poll(7, oldName, "old-unproven-process", 111)
	trafficCompatSource(t, st, unknown, oldName, "ambiguous_identity", "relay_identity_collision", 0)
	if err = st.canRetireRelayCredential(st.db, v); err == nil {
		t.Fatal("unproven old shared sample was ignored by the drain boundary")
	}
	poll(8, oldName, epoch, 43)
	poll(9, oldName, epoch, 47)
	if err = st.ChangeRelayCredential(1, v, "retire"); err != nil {
		t.Fatalf("drained shared credential could not retire while customer remained active: %v", err)
	}
	trafficCompatTotals(t, st, uid, bucketID, 0, 193)
}

func TestLegacyRelayRetirementRejectsOldWireConfigAcknowledgement(t *testing.T) {
	for _, protocol := range []string{"trojan", "vless"} {
		t.Run(protocol, func(t *testing.T) {
			st, _, serverID, inboundID, users := activateMeteringFixture(t, protocol)
			oldName := fmt.Sprintf("relay_%d", inboundID)
			newName, err := legacyRelayStatsNameWith(st.db, serverID, inboundID)
			if err != nil || newName == oldName {
				t.Fatalf("fixture has no separated shared name: %q %v", newName, err)
			}
			const mixedTag = "retirement-customer-mixed"
			if _, err = st.SaveSbInbound(&SbInbound{ServerID: serverID, Type: "mixed", Tag: mixedTag, ListenPort: 17891, Options: `{}`, Enabled: true}); err != nil {
				t.Fatal(err)
			}
			users[mixedTag] = []singbox.User{{Name: oldName, Password: "customer-password-is-not-shared"}}
			current, err := st.BuildSingboxConfigForServer(serverID, singbox.DefaultBaseConfig, "127.0.0.1:18080", users)
			if err != nil {
				t.Fatal(err)
			}
			assertLegacyRelayConfigUser(t, st, current, serverID, inboundID, true)
			// This recreates an earlier applied config with the original shared
			// name, while preserving its exact UUID/password and inbound target.
			oldConfig := []byte(strings.ReplaceAll(string(current), `"`+newName+`"`, `"`+oldName+`"`))
			target, err := st.GetSbInbound(inboundID)
			if err != nil {
				t.Fatal(err)
			}
			wantUUID, wantPassword := relayCred(target.RelaySecret)
			inspect := func(raw []byte) (shared, mixed bool) {
				t.Helper()
				var cfg struct {
					Inbounds []struct {
						Tag   string           `json:"tag"`
						Type  string           `json:"type"`
						Users []map[string]any `json:"users"`
					} `json:"inbounds"`
				}
				if err := json.Unmarshal(raw, &cfg); err != nil {
					t.Fatal(err)
				}
				for _, ib := range cfg.Inbounds {
					for _, user := range ib.Users {
						if ib.Type == "mixed" && user["username"] == oldName {
							mixed = true
						}
						if ib.Tag == target.Tag && (user["name"] == oldName || user["name"] == newName) {
							shared = true
							if protocol == "vless" && user["uuid"] != wantUUID || protocol == "trojan" && user["password"] != wantPassword {
								t.Fatal("fixture failed to preserve the original shared wire credential")
							}
						}
					}
				}
				return
			}
			if shared, mixed := inspect(oldConfig); !shared || !mixed || strings.Contains(string(oldConfig), newName) {
				t.Fatal("old config fixture must keep old shared authentication and the separate mixed customer only")
			}
			recordDrainedPolls(t, st, serverID)
			v := RelayCredentialView{Kind: "legacy", ServerID: serverID, InboundID: inboundID}
			if err = st.ChangeRelayCredential(1, v, "retire"); err != nil {
				t.Fatal(err)
			}
			if err = st.RecordRelayConfigApplied(serverID, oldConfig); err != nil {
				t.Fatal(err)
			}
			state, err := st.LegacyRelayCompatibility(serverID, inboundID)
			if err != nil || state != "retiring" {
				t.Fatalf("old shared wire credential was acknowledged as removed: state=%s err=%v", state, err)
			}
			clean, err := st.BuildSingboxConfigForServer(serverID, singbox.DefaultBaseConfig, "127.0.0.1:18080", users)
			if err != nil {
				t.Fatal(err)
			}
			if shared, mixed := inspect(clean); shared || !mixed {
				t.Fatal("clean config must remove both shared-name forms while preserving the mixed customer")
			}
			if err = st.RecordRelayConfigApplied(serverID, clean); err != nil {
				t.Fatal(err)
			}
			state, err = st.LegacyRelayCompatibility(serverID, inboundID)
			if err != nil || state != "retired" {
				t.Fatalf("legitimate numeric mixed customer blocked retirement acknowledgement: state=%s err=%v", state, err)
			}
		})
	}
}
