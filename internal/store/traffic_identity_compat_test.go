package store

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"qingzhou/internal/singbox"
)

// These SQL writes deliberately model already-provisioned, pre-upgrade names.
// New-name validation must not be mistaken for permission to drop old usage.
func trafficCompatSetName(t *testing.T, st *Store, scope string, uid, bucketID int64, name string) {
	t.Helper()
	var err error
	switch scope {
	case "account":
		_, err = st.db.Exec(`UPDATE users SET proxy_username=?,proxy_password='compat-fixture' WHERE id=?`, name, uid)
	case "bucket":
		_, err = st.db.Exec(`UPDATE user_plans SET proxy_username=?,proxy_password='compat-fixture' WHERE id=?`, name, bucketID)
	case "line":
		_, err = st.db.Exec(`UPDATE plan_identities SET proxy_username=?,proxy_password='compat-fixture' WHERE user_id=?`, name, uid)
	default:
		t.Fatalf("unknown fixture scope %s", scope)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func trafficCompatCustomer(t *testing.T, st *Store, pkg *Package, scope, name string) (int64, int64) {
	t.Helper()
	uid := mkUser(t, st, name+"-owner")
	buy(t, st, uid, pkg)
	var bucketID int64
	if err := st.db.QueryRow(`SELECT id FROM user_plans WHERE user_id=? AND kind='plan' AND status='active'`, uid).Scan(&bucketID); err != nil {
		t.Fatal(err)
	}
	trafficCompatSetName(t, st, scope, uid, bucketID, name)
	return uid, bucketID
}

func trafficCompatTotals(t *testing.T, st *Store, uid, bucketID, up, down int64) {
	t.Helper()
	for _, q := range []struct {
		label string
		query string
		id    int64
	}{
		{"user", `SELECT used_up,used_down FROM users WHERE id=?`, uid},
		{"bucket", `SELECT used_up,used_down FROM user_plans WHERE id=?`, bucketID},
		{"daily", `SELECT COALESCE(SUM(up),0),COALESCE(SUM(down),0) FROM traffic_daily WHERE user_id=?`, uid},
		{"samples", `SELECT COALESCE(SUM(up),0),COALESCE(SUM(down),0) FROM server_user_traffic_samples WHERE user_id=?`, uid},
	} {
		var gotUp, gotDown int64
		if err := st.db.QueryRow(q.query, q.id).Scan(&gotUp, &gotDown); err != nil {
			t.Fatal(err)
		}
		if gotUp != up || gotDown != down {
			t.Fatalf("%s user=%d got %d/%d, want %d/%d", q.label, uid, gotUp, gotDown, up, down)
		}
	}
}

func trafficCompatRecord(t *testing.T, st *Store, p TrafficPoll) {
	t.Helper()
	if _, err := st.RecordTrafficPoll(p); err != nil {
		t.Fatal(err)
	}
}

func TestTrafficIdentityCompatibilityMixedAccountsFlagMatrix(t *testing.T) {
	for _, links := range []bool{false, true} {
		for _, cumulative := range []bool{false, true} {
			for _, scope := range []string{"account", "bucket", "line"} {
				t.Run(fmt.Sprintf("links=%t/cumulative=%t/%s", links, cumulative, scope), func(t *testing.T) {
					st := newRefundStore(t)
					st.SetSecretKey([]byte("compat-fixture-key"))
					if err := st.ConfigureTrafficMetering(links, cumulative); err != nil {
						t.Fatal(err)
					}
					pkg := mkPlan(t, st, "compat", 10, 10, 30)
					if _, err := st.SaveSbInbound(&SbInbound{Type: "mixed", Tag: "compat-mixed", ListenPort: 17890, Options: `{}`, Enabled: true}); err != nil {
						t.Fatal(err)
					}
					bindPlanToInbound(t, st, pkg.ID, "compat-mixed")
					names := []string{"qzr_customer", "qzr_l_0123456789abcdef01234567", "relay_customer", "relay_042", "relay_-1", "ordinary_customer"}
					type owner struct{ uid, bucketID int64 }
					owners := make(map[string]owner)
					traffic := make(map[string]UsageDelta)
					for _, name := range names {
						uid, bucketID := trafficCompatCustomer(t, st, pkg, scope, name)
						owners[name] = owner{uid, bucketID}
						traffic[name] = UsageDelta{Up: 10, Down: 20}
						traffic[routeIdentityName(name, 12)] = UsageDelta{Up: 7, Down: 11}
						traffic[routeIdentityName(credentialAliasStatsName(name, 3), 12)] = UsageDelta{Up: 3, Down: 5}
					}
					users, err := st.BuildUsersByTag(time.Now().Unix())
					if err != nil {
						t.Fatal(err)
					}
					raw, err := st.BuildSingboxConfig(singbox.DefaultBaseConfig, "127.0.0.1:18080", users)
					if err != nil {
						t.Fatal(err)
					}
					var config struct {
						Inbounds []struct {
							Tag   string `json:"tag"`
							Users []struct {
								Username string `json:"username"`
								Password string `json:"password"`
							} `json:"users"`
						} `json:"inbounds"`
					}
					if err = json.Unmarshal(raw, &config); err != nil {
						t.Fatal(err)
					}
					emitted := map[string]string{}
					for _, ib := range config.Inbounds {
						if ib.Tag == "compat-mixed" {
							for _, u := range ib.Users {
								emitted[u.Username] = u.Password
							}
						}
					}
					p := NewTrafficPoll(0, traffic)
					if cumulative {
						p.Mode, p.Epoch = "cumulative", "compat-process"
					}
					trafficCompatRecord(t, st, p)
					trafficCompatRecord(t, st, p)
					for name, o := range owners {
						if emitted[name] != "compat-fixture" {
							t.Fatalf("existing mixed credential %q was omitted or changed", name)
						}
						trafficCompatTotals(t, st, o.uid, o.bucketID, 20, 36)
					}
					report, err := st.ServerServiceTraffic(0, 0)
					if err != nil || report.Total != 336 || report.BillableTotal != 336 || !report.UserCoverageComplete || len(report.Sources) != len(names) {
						t.Fatalf("ordinary legacy customer coverage: %+v %v", report, err)
					}
				})
			}
		}
	}
}

func TestTrafficIdentityCompatibilityCumulativeBoundaries(t *testing.T) {
	for _, scope := range []string{"account", "bucket", "line"} {
		for _, name := range []string{"qzr_customer", "relay_customer"} {
			t.Run(scope+"/"+name, func(t *testing.T) {
				st := newRefundStore(t)
				pkg := mkPlan(t, st, "compat", 10, 10, 30)
				uid, bucketID := trafficCompatCustomer(t, st, pkg, scope, name)
				counter := routeIdentityName(name, 9)
				makePoll := func(epoch string, up, down int64, baseline bool) TrafficPoll {
					p := NewTrafficPoll(0, map[string]UsageDelta{counter: {Up: up, Down: down}})
					p.Mode, p.Epoch, p.Baseline = "cumulative", epoch, baseline
					var err error
					p.Sequence, err = st.NextTrafficSequence()
					if err != nil {
						t.Fatal(err)
					}
					return p
				}
				trafficCompatRecord(t, st, makePoll("first", 100, 200, true))
				first := makePoll("first", 110, 220, false)
				trafficCompatRecord(t, st, first)
				trafficCompatRecord(t, st, first)
				late := makePoll("first", 115, 225, false)
				trafficCompatRecord(t, st, makePoll("first", 130, 240, false))
				trafficCompatRecord(t, st, late)
				trafficCompatRecord(t, st, makePoll("first", 2, 3, false))
				trafficCompatRecord(t, st, makePoll("first", 131, 242, false))
				last := makePoll("second", 5, 7, false)
				trafficCompatRecord(t, st, last)
				trafficCompatRecord(t, st, last)
				trafficCompatTotals(t, st, uid, bucketID, 36, 49)
				report, err := st.ServerServiceTraffic(0, 0)
				if err != nil || report.Total != 85 || report.BillableTotal != 85 || report.Quality.Gaps != 4 || report.UserCoverageComplete {
					t.Fatalf("cumulative boundary coverage: %+v %v", report, err)
				}
			})
		}
	}
}

func TestTrafficIdentityCompatibilityPendingIntervalKeepsOriginalOwner(t *testing.T) {
	for _, scope := range []string{"account", "bucket", "line"} {
		t.Run(scope, func(t *testing.T) {
			st := newRefundStore(t)
			pkg := mkPlan(t, st, "compat", 10, 10, 30)
			const name = "qzr_customer"
			a, aBucket := trafficCompatCustomer(t, st, pkg, scope, name)
			b, bBucket := trafficCompatCustomer(t, st, pkg, scope, "replacement_customer")
			if _, err := st.db.Exec(`CREATE TRIGGER compat_fail BEFORE UPDATE OF used_up ON user_plans WHEN OLD.id=` + fmt.Sprint(aBucket) + ` BEGIN SELECT RAISE(FAIL,'fixture write failure'); END`); err != nil {
				t.Fatal(err)
			}
			p1 := NewTrafficPoll(0, map[string]UsageDelta{name: {Down: 100}})
			p1.Mode, p1.Epoch = "cumulative", "same-process"
			if _, err := st.RecordTrafficPoll(p1); err == nil {
				t.Fatal("injected debit failure was swallowed")
			}
			trafficCompatSetName(t, st, scope, a, aBucket, "renamed_customer")
			trafficCompatSetName(t, st, scope, b, bBucket, name)
			p2 := NewTrafficPoll(0, map[string]UsageDelta{name: {Down: 150}})
			p2.Mode, p2.Epoch = "cumulative", "same-process"
			if _, err := st.RecordTrafficPoll(p2); err == nil {
				t.Fatal("later cumulative stock overtook an unprocessed owner interval")
			}
			trafficCompatTotals(t, st, a, aBucket, 0, 0)
			trafficCompatTotals(t, st, b, bBucket, 0, 0)
			for _, check := range []struct {
				poll string
				uid  int64
			}{{p1.ID, a}, {p2.ID, b}} {
				var owner int64
				var state string
				if err := st.db.QueryRow(`SELECT b.user_id,p.state FROM traffic_poll_bindings b JOIN traffic_polls p ON p.id=b.poll_id WHERE b.poll_id=? AND b.counter_name=?`, check.poll, name).Scan(&owner, &state); err != nil || owner != check.uid || state != "pending" {
					t.Fatalf("immutable pending owner: owner=%d state=%s err=%v", owner, state, err)
				}
			}
			if _, err := st.db.Exec(`DROP TRIGGER compat_fail`); err != nil {
				t.Fatal(err)
			}
			if _, err := st.RetryPendingTrafficPolls(); err != nil {
				t.Fatal(err)
			}
			trafficCompatRecord(t, st, p1)
			trafficCompatRecord(t, st, p2)
			trafficCompatTotals(t, st, a, aBucket, 0, 100)
			trafficCompatTotals(t, st, b, bBucket, 0, 50)
		})
	}
}

func trafficCompatSource(t *testing.T, st *Store, p TrafficPoll, name, kind, gap string, uid int64) {
	t.Helper()
	var gotKind string
	var gotUser, billable int64
	if err := st.db.QueryRow(`SELECT source_kind,user_id,billable FROM traffic_observations WHERE poll_id=? AND counter_name=?`, p.ID, name).Scan(&gotKind, &gotUser, &billable); err != nil {
		t.Fatal(err)
	}
	if gotKind != kind || gotUser != uid || (kind != "direct_user" && billable != 0) {
		t.Fatalf("counter=%s source=%s user=%d billable=%d, want %s user=%d", name, gotKind, gotUser, billable, kind, uid)
	}
	if gap != "" {
		var count int
		if err := st.db.QueryRow(`SELECT COUNT(*) FROM traffic_metering_gaps WHERE poll_id=? AND reason=?`, p.ID, gap).Scan(&count); err != nil || count != 1 {
			t.Fatalf("missing explicit attribution gap %s: count=%d err=%v", gap, count, err)
		}
	}
}

// An unresolved historical relay is observable but cannot be claimed as
// complete per-customer coverage, even if its raw name resembles an account.
func TestTrafficIdentityCompatibilityLegacyRelayBoundaries(t *testing.T) {
	for _, scope := range []string{"account", "bucket", "line"} {
		t.Run(scope, func(t *testing.T) {
			st := newRefundStore(t)
			server, err := st.CreateServer(Server{Name: "landing", Host: "192.0.2.1", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			inbound, err := st.SaveSbInbound(&SbInbound{ServerID: server, Type: "trojan", Tag: "compat-landing", ListenPort: 18443, Options: `{}`, RelaySecret: "fixture-only", Enabled: true})
			if err != nil {
				t.Fatal(err)
			}
			pkg := mkPlan(t, st, "compat", 10, 10, 30)
			name := fmt.Sprintf("relay_%d", inbound)
			uid, bucketID := trafficCompatCustomer(t, st, pkg, scope, name)
			normal, normalBucket := trafficCompatCustomer(t, st, pkg, scope, "relay_customer")
			p := NewTrafficPoll(server, map[string]UsageDelta{name: {Down: 100}, "relay_customer": {Up: 3, Down: 7}, "qzr_unknown": {Down: 9}})
			trafficCompatRecord(t, st, p)
			trafficCompatRecord(t, st, p)
			trafficCompatSource(t, st, p, name, "ambiguous_identity", "relay_identity_collision", 0)
			trafficCompatSource(t, st, p, "relay_customer", "direct_user", "", normal)
			trafficCompatSource(t, st, p, "qzr_unknown", "unknown", "", 0)
			trafficCompatTotals(t, st, uid, bucketID, 0, 0)
			trafficCompatTotals(t, st, normal, normalBucket, 3, 7)
			wrong := NewTrafficPoll(0, map[string]UsageDelta{name: {Down: 200}})
			trafficCompatRecord(t, st, wrong)
			trafficCompatSource(t, st, wrong, name, "unknown", "legacy_relay_provenance_unknown", 0)
			if _, err = st.DeleteSbInbound(inbound); err != nil {
				t.Fatal(err)
			}
			late := NewTrafficPoll(server, map[string]UsageDelta{name: {Down: 300}})
			trafficCompatRecord(t, st, late)
			trafficCompatSource(t, st, late, name, "unknown", "legacy_relay_provenance_unknown", 0)
			trafficCompatTotals(t, st, uid, bucketID, 0, 0)
			report, err := st.ServerServiceTraffic(server, 0)
			if err != nil || report.Total != 419 || report.BillableTotal != 10 || report.UserCoverageComplete || report.Quality.Gaps != 2 {
				t.Fatalf("unresolved historical relay falsely claimed coverage: %+v %v", report, err)
			}
		})
	}
}

func TestTrafficIdentityCompatibilityMappedGenerationWinsOverCustomerName(t *testing.T) {
	st, entry, landing, _, _, _ := meteringRelayFixture(t)
	if err := st.PrepareRelayMetering(); err != nil {
		t.Fatal(err)
	}
	links, err := st.RelayMeteringLinks()
	if err != nil || len(links) != 1 {
		t.Fatalf("links=%+v err=%v", links, err)
	}
	name := links[0].IdentityName
	pkg := mkPlan(t, st, "compat", 10, 10, 30)
	uid, bucketID := trafficCompatCustomer(t, st, pkg, "account", name)
	for _, retired := range []int64{0, time.Now().Unix()} {
		if _, err := st.db.Exec(`UPDATE relay_metering_generations SET retired_at=? WHERE identity_name=?`, retired, name); err != nil {
			t.Fatal(err)
		}
		p := NewTrafficPoll(landing, map[string]UsageDelta{name: {Down: 41}})
		trafficCompatRecord(t, st, p)
		trafficCompatSource(t, st, p, name, "relay_link", "", 0)
		wrong := NewTrafficPoll(entry, map[string]UsageDelta{name: {Down: 43}})
		trafficCompatRecord(t, st, wrong)
		trafficCompatRecord(t, st, wrong)
		trafficCompatSource(t, st, wrong, name, "unknown", "relay_source_mismatch", 0)
	}
	trafficCompatTotals(t, st, uid, bucketID, 0, 0)
}

func TestTrafficIdentityCompatibilityRelayUserRetainsDeletedOwner(t *testing.T) {
	st, entry, landing, _, _, _ := meteringRelayFixture(t)
	if err := st.PrepareRelayMetering(); err != nil {
		t.Fatal(err)
	}
	links, err := st.RelayMeteringLinks()
	if err != nil || len(links) != 1 {
		t.Fatalf("links=%+v err=%v", links, err)
	}
	pkg := mkPlan(t, st, "compat", 10, 10, 30)
	a, aBucket := trafficCompatCustomer(t, st, pkg, "account", "original_customer")
	b, bBucket := trafficCompatCustomer(t, st, pkg, "account", "replacement_customer")
	const oldName = "qzr_user_fixture_old"
	for generation, name := range []string{oldName, "qzr_user_fixture_new"} {
		if _, err := st.db.Exec(`INSERT INTO relay_metering_users(link_id,user_id,generation,identity_name,credential,enabled,state,created_at) VALUES(?,?,?,?,?,1,'active',?)`, links[0].ID, a, generation+1, name, "fixture-only", time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.db.Exec(`CREATE TRIGGER compat_relay_fail BEFORE INSERT ON traffic_observations WHEN NEW.counter_name='qzr_user_fixture_old' BEGIN SELECT RAISE(FAIL,'fixture observation failure'); END`); err != nil {
		t.Fatal(err)
	}
	p := NewTrafficPoll(landing, map[string]UsageDelta{oldName: {Up: 17, Down: 29}})
	if _, err := st.RecordTrafficPoll(p); err == nil {
		t.Fatal("injected observation failure was swallowed")
	}
	var frozenUser, frozenBucket int64
	var frozenKind string
	if err := st.db.QueryRow(`SELECT user_id,bucket_id,source_kind FROM traffic_poll_bindings WHERE poll_id=? AND counter_name=?`, p.ID, oldName).Scan(&frozenUser, &frozenBucket, &frozenKind); err != nil || frozenUser != a || frozenBucket != 0 || frozenKind != "relay_user" {
		t.Fatalf("relay binding must retain owner without a debit bucket: owner=%d bucket=%d kind=%s err=%v", frozenUser, frozenBucket, frozenKind, err)
	}
	// Account renaming, deletion, retirement and a new customer reusing the
	// text cannot transfer already-measured relay traffic to another owner.
	trafficCompatSetName(t, st, "account", a, aBucket, "renamed_customer")
	trafficCompatSetName(t, st, "account", b, bBucket, oldName)
	if _, err := st.db.Exec(`DELETE FROM users WHERE id=?`, a); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE relay_metering_users SET enabled=0,state='retired' WHERE identity_name=?`, oldName); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`DROP TRIGGER compat_relay_fail`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RetryPendingTrafficPolls(); err != nil {
		t.Fatal(err)
	}
	trafficCompatRecord(t, st, p)
	trafficCompatSource(t, st, p, oldName, "relay_user", "", a)
	late := NewTrafficPoll(landing, map[string]UsageDelta{oldName: {Up: 3, Down: 5}})
	trafficCompatRecord(t, st, late)
	trafficCompatSource(t, st, late, oldName, "relay_user", "", a)
	wrong := NewTrafficPoll(entry, map[string]UsageDelta{oldName: {Down: 100}})
	trafficCompatRecord(t, st, wrong)
	trafficCompatSource(t, st, wrong, oldName, "unknown", "relay_source_mismatch", 0)
	trafficCompatTotals(t, st, b, bBucket, 0, 0)
	var oldBucketUsed int64
	if err := st.db.QueryRow(`SELECT used_up+used_down FROM user_plans WHERE id=?`, aBucket).Scan(&oldBucketUsed); err != nil || oldBucketUsed != 0 {
		t.Fatalf("relay traffic debited old bucket: %d %v", oldBucketUsed, err)
	}
	report, err := st.ServerServiceTraffic(landing, 0)
	if err != nil || report.Total != 54 || report.BillableTotal != 0 || len(report.Sources) != 1 || report.Sources[0].UserID != a || !report.ObservedUserCoverageComplete {
		t.Fatalf("deleted owner attribution was lost: %+v %v", report, err)
	}
}
