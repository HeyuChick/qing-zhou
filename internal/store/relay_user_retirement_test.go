package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func rotatedRelayUser(t *testing.T, protocols ...meteringProtocolCase) (userMeteringFixture, *RelayMeteringUser, RelayCredentialView, []byte) {
	t.Helper()
	var f userMeteringFixture
	if len(protocols) > 0 {
		f = newProtocolMeteringFixture(t, protocols[0], protocols[0])
	} else {
		f = newUserMeteringFixture(t, 2)
	}
	f.apply(t, 1)
	oldSource := f.apply(t, 0)
	users, err := f.st.RelayMeteringUsers()
	if err != nil {
		t.Fatal(err)
	}
	old := users[0]
	target, _ := f.st.GetSbInbound(f.inbounds[1])
	target.ListenPort++
	if _, err = f.st.SaveSbInbound(target); err != nil {
		t.Fatal(err)
	}
	if err = f.st.PrepareRelayMetering(); err != nil {
		t.Fatal(err)
	}
	v := RelayCredentialView{Kind: "user_generation", LinkID: old.LinkID, UserID: old.UserID, ServerID: f.servers[1], InboundID: f.inbounds[1], Generation: old.Generation}
	if err = f.st.ChangeRelayCredential(1, v, "retire"); err == nil {
		t.Fatal("retired before replacement applied")
	}
	f.apply(t, 1)
	f.apply(t, 0)
	return f, old, v, oldSource
}

func quietRelayUser(t *testing.T, f userMeteringFixture, old *RelayMeteringUser) {
	t.Helper()
	now := time.Now().Unix()
	boundary := now - 1300
	if _, err := f.st.db.Exec(`UPDATE relay_user_retirements SET source_clear_at=? WHERE relay_user_id=?`, boundary, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.db.Exec(`UPDATE relay_metering_users SET activated_at=? WHERE link_id=? AND generation>?`, boundary, old.LinkID, old.Generation); err != nil {
		t.Fatal(err)
	}
	for _, sid := range f.servers {
		for i, at := range []int64{now - 600, now - 1} {
			p := NewTrafficPoll(sid, map[string]UsageDelta{})
			p.ID = fmt.Sprintf("quiet-%d-%d", sid, i)
			p.ObservedAt = at
			if _, err := f.st.RecordTrafficPoll(p); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func userRetirementState(t *testing.T, st *Store, id int64) string {
	t.Helper()
	var state string
	if err := st.db.QueryRow(`SELECT state FROM relay_user_retirements WHERE relay_user_id=?`, id).Scan(&state); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestRelayUserRetirementAppliedLifecycleAndHistoricalOwner(t *testing.T) {
	f, old, v, _ := rotatedRelayUser(t)
	if err := f.st.ChangeRelayCredential(1, v, "retire"); err == nil {
		t.Fatal("retired without quiet window")
	}
	quietRelayUser(t, f, old)
	f.apply(t, 0) // Same cleared config must not restart the quiet window forever
	views, err := f.st.RelayCredentialViews()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, view := range views {
		if view.Kind == v.Kind && view.LinkID == v.LinkID && view.UserID == v.UserID && view.Generation == v.Generation {
			found = view.CanRetire
			if !found {
				t.Fatal(view.Reason)
			}
		}
	}
	if !found {
		t.Fatal("P1 retirement unavailable to UI")
	}
	oldLanding, err := f.config(t, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.st.ChangeRelayCredential(1, v, "retire"); err != nil {
		t.Fatal(err)
	}
	if got := userRetirementState(t, f.st, old.ID); got != "retiring" {
		t.Fatal(got)
	}
	if err = f.st.RecordRelayConfigApplied(f.servers[1], oldLanding); err != nil {
		t.Fatal(err)
	}
	if got := userRetirementState(t, f.st, old.ID); got != "retiring" {
		t.Fatal("old applied config falsely proved removal", got)
	}
	clean := f.apply(t, 1)
	if strings.Contains(string(clean), old.IdentityName) {
		t.Fatal("retired identity still rendered")
	}
	if got := userRetirementState(t, f.st, old.ID); got != "retired" {
		t.Fatal(got)
	}
	if err = f.st.RecordRelayConfigApplied(f.servers[1], oldLanding); err != nil {
		t.Fatal(err)
	}
	if got := userRetirementState(t, f.st, old.ID); got != "retiring" {
		t.Fatal("reintroduced runtime credential still reported retired", got)
	}
	f.apply(t, 1)
	if err = f.st.PrepareRelayMetering(); err != nil {
		t.Fatal(err)
	}
	clean = f.apply(t, 1)
	if strings.Contains(string(clean), old.IdentityName) {
		t.Fatal("prepare revived retired identity")
	}
	p := NewTrafficPoll(f.servers[1], map[string]UsageDelta{old.IdentityName: {Down: 37}})
	if _, err = f.st.RecordTrafficPoll(p); err != nil {
		t.Fatal(err)
	}
	var owner int64
	var kind string
	if err = f.st.db.QueryRow(`SELECT source_kind,user_id FROM traffic_observations WHERE poll_id=?`, p.ID).Scan(&kind, &owner); err != nil || kind != "relay_user" || owner != old.UserID {
		t.Fatalf("historical owner lost: %s/%d %v", kind, owner, err)
	}
	if err = f.st.ChangeRelayCredential(1, v, "restore"); err != nil {
		t.Fatal(err)
	}
	restored, err := f.config(t, 1)
	if err != nil {
		t.Fatal(err)
	}
	credential, err := f.st.relayMeteringUserCredential(old)
	if err != nil {
		t.Fatal(err)
	}
	bad := []byte(strings.ReplaceAll(string(restored), credential.UUID, "00000000-0000-4000-8000-000000000099"))
	if err = f.st.RecordRelayConfigApplied(f.servers[1], bad); err != nil {
		t.Fatal(err)
	}
	if got := userRetirementState(t, f.st, old.ID); got != "restoring" {
		t.Fatal("wrong credential acknowledged", got)
	}
	if err = f.st.RecordRelayConfigApplied(f.servers[1], restored); err != nil {
		t.Fatal(err)
	}
	if got := userRetirementState(t, f.st, old.ID); got != "active" {
		t.Fatal(got)
	}
	if err = f.st.RecordRelayConfigApplied(f.servers[1], bad); err != nil {
		t.Fatal(err)
	}
	if got := userRetirementState(t, f.st, old.ID); got != "restoring" {
		t.Fatal("runtime restoration regression hidden", got)
	}
	if err = f.st.RecordRelayConfigApplied(f.servers[1], restored); err != nil {
		t.Fatal(err)
	}
	if n := scalar(t, f.st, `SELECT COUNT(*) FROM relay_credential_audit WHERE user_id=?`, old.UserID); n != 2 {
		t.Fatalf("audit %d", n)
	}
	current := v
	current.Generation++
	if err = f.st.ChangeRelayCredential(1, current, "retire"); err == nil {
		t.Fatal("current identity retired")
	}
	if err = f.st.ChangeRelayCredential(1, current, "restore"); err == nil {
		t.Fatal("current identity restored")
	}
}

func TestRelayUserRetirementRejectsLiveAliasPendingAndLateTraffic(t *testing.T) {
	for _, test := range []string{"source_alias", "pending_source", "pending_target", "positive_source", "positive_target", "unconfirmed_source", "optout", "gap", "failure", "collecting", "optout_reenable"} {
		t.Run(test, func(t *testing.T) {
			f, old, v, oldSource := rotatedRelayUser(t)
			quietRelayUser(t, f, old)
			switch test {
			case "source_alias":
				current, err := f.config(t, 0)
				if err != nil {
					t.Fatal(err)
				}
				var cfg, previous map[string]interface{}
				json.Unmarshal(current, &cfg)
				json.Unmarshal(oldSource, &previous)
				for _, o := range previous["outbounds"].([]interface{}) {
					out := o.(map[string]interface{})
					if out["tag"] == old.outboundTag() {
						out["tag"] = "renamed-live-consumer"
						cfg["outbounds"] = append(cfg["outbounds"].([]interface{}), out)
					}
				}
				raw, _ := json.Marshal(cfg)
				if err = f.st.RecordRelayConfigApplied(f.servers[0], raw); err != nil {
					t.Fatal(err)
				}
				if n := scalar(t, f.st, `SELECT source_clear_at FROM relay_user_retirements WHERE relay_user_id=?`, old.ID); n != 0 {
					t.Fatal("live wire credential alias ignored")
				}
			case "pending_source", "pending_target":
				sid := f.servers[0]
				if test == "pending_target" {
					sid = f.servers[1]
				}
				if _, err := f.st.db.Exec(`UPDATE traffic_polls SET state='pending' WHERE id=?`, fmt.Sprintf("quiet-%d-0", sid)); err != nil {
					t.Fatal(err)
				}
			case "positive_source", "positive_target":
				sid, name := f.servers[1], old.IdentityName
				if test == "positive_source" {
					sid = f.servers[0]
					name = "outbound:" + old.outboundTag()
				}
				if _, err := f.st.RecordTrafficPoll(NewTrafficPoll(sid, map[string]UsageDelta{name: {Down: 1}})); err != nil {
					t.Fatal(err)
				}
			case "unconfirmed_source":
				if _, err := f.st.db.Exec(`UPDATE relay_metering_applies SET config_hash='another-config' WHERE server_id=?`, f.servers[0]); err != nil {
					t.Fatal(err)
				}
			case "optout":
				if err := f.st.ConfigureTrafficMetering(true, false, false); err != nil {
					t.Fatal(err)
				}
			case "optout_reenable":
				if err := f.st.ConfigureTrafficMetering(true, false, false); err != nil {
					t.Fatal(err)
				}
				f.apply(t, 0)
				if err := f.st.ConfigureTrafficMetering(true, false, true); err != nil {
					t.Fatal(err)
				}
			case "collecting":
				if ok, err := f.st.AcquireTrafficLease(f.servers[0], "in-flight-test"); err != nil || !ok {
					t.Fatal(err)
				}
			case "failure":
				if err := f.st.RecordTrafficCollectionFailure(f.servers[0], "unavailable"); err != nil {
					t.Fatal(err)
				}
			case "gap":
				if err := f.st.RecordTrafficBoundaryGap(f.servers[0], "process_changed_during_read"); err != nil {
					t.Fatal(err)
				}
			}
			if err := f.st.ChangeRelayCredential(1, v, "retire"); err == nil {
				t.Fatal("unsafe retirement allowed")
			}
			if n := scalar(t, f.st, `SELECT COUNT(*) FROM relay_credential_audit`); n != 0 {
				t.Fatal("blocked operation wrote audit")
			}
		})
	}
}

func TestRelayUserRetirementMigrationPreservesEvidenceAndRollsBack(t *testing.T) {
	st := openUnmigrated(t)
	chain := st.migrations()
	if err := st.runMigrations(chain[:8]); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`INSERT INTO traffic_polls(id,sequence,server_id,observed_at,mode,payload,digest,created_at,state) VALUES('retained',1,1,1,'reset','immutable','dedupe',1,'done'); INSERT INTO traffic_poll_bindings(poll_id,counter_name,source_kind,user_id) VALUES('retained','late-owner','direct_user',7); INSERT INTO traffic_observations(poll_id,counter_name,server_id,ts,source_kind,user_id,up,down) VALUES('retained','late-owner',1,1,'direct_user',7,0,0); CREATE INDEX idx_traffic_polls_server_state_time ON traffic_polls(id)`); err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(); err == nil {
		t.Fatal("expected new-index migration failure")
	}
	if n := scalar(t, st, `SELECT COUNT(*) FROM sqlite_schema WHERE name='relay_user_retirements'`); n != 0 {
		t.Fatal("partial retirement schema leaked")
	}
	if n := scalar(t, st, `SELECT COUNT(*) FROM pragma_table_info('relay_credential_audit') WHERE name='user_id'`); n != 0 {
		t.Fatal("partial audit column leaked")
	}
	if _, err := st.db.Exec(`DROP INDEX idx_traffic_polls_server_state_time`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := st.Migrate(); err != nil {
			t.Fatal(err)
		}
	}
	if n := scalar(t, st, `SELECT COUNT(*) FROM traffic_poll_bindings WHERE user_id=7`); n != 1 {
		t.Fatal("owner binding lost")
	}
	if n := scalar(t, st, `SELECT COUNT(*) FROM traffic_observations WHERE up=0 AND down=0`); n != 1 {
		t.Fatal("zero observation lost")
	}
	if n := scalar(t, st, `SELECT COUNT(*) FROM traffic_polls WHERE payload='immutable' AND digest='dedupe'`); n != 1 {
		t.Fatal("immutable payload/tombstone lost")
	}
	if n := scalar(t, st, `SELECT COUNT(*) FROM relay_user_retirements`); n != 0 {
		t.Fatal("upgrade automatically retired credentials")
	}
}

func TestRelayUserRetirementProtocolRestoreMatrix(t *testing.T) {
	for _, protocol := range meteringProtocolCases() {
		t.Run(protocol.name, func(t *testing.T) {
			f, old, v, _ := rotatedRelayUser(t, protocol)
			quietRelayUser(t, f, old)
			if err := f.st.ChangeRelayCredential(1, v, "retire"); err != nil {
				t.Fatal(err)
			}
			f.apply(t, 1)
			if got := userRetirementState(t, f.st, old.ID); got != "retired" {
				t.Fatal(got)
			}
			if err := f.st.ChangeRelayCredential(1, v, "restore"); err != nil {
				t.Fatal(err)
			}
			restored, err := f.config(t, 1)
			if err != nil {
				t.Fatal(err)
			}
			cfg := protocolConfigObject(t, restored)
			inbound := protocolObjectByTag(t, cfg, "inbounds", f.tags[1])
			user := protocolUserByName(t, inbound, old.IdentityName)
			user[protocol.authFields()[0]] = "wrong-old-auth-object"
			wrong, _ := json.Marshal(cfg)
			if err = f.st.RecordRelayConfigApplied(f.servers[1], wrong); err != nil {
				t.Fatal(err)
			}
			if got := userRetirementState(t, f.st, old.ID); got != "restoring" {
				t.Fatal("wrong credential acknowledged", got)
			}
			if err = f.st.RecordRelayConfigApplied(f.servers[1], restored); err != nil {
				t.Fatal(err)
			}
			if got := userRetirementState(t, f.st, old.ID); got != "active" {
				t.Fatal("restore not acknowledged", got)
			}
		})
	}
}
