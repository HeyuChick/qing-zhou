package store

import (
	"fmt"
	"testing"
)

func TestLegacyNumericCustomerNeedsVerifiedSeparatedEpoch(t *testing.T) {
	for _, mode := range []string{"reset", "cumulative"} {
		t.Run(mode, func(t *testing.T) {
			st := newRefundStore(t)
			pkg := mkPlan(t, st, "legacy", 1, 10, 30)
			sid, _ := st.CreateServer(Server{Name: "legacy", Host: "192.0.2.4", Enabled: true})
			ib, err := st.SaveSbInbound(&SbInbound{ServerID: sid, Type: "vless", Tag: "old-landing", ListenPort: 2443, Options: `{}`, Enabled: true, RelaySecret: "same-wire-secret"})
			if err != nil {
				t.Fatal(err)
			}
			name := fmt.Sprintf("relay_%d", ib)
			uid, bucket := trafficCompatCustomer(t, st, pkg, "account", name)
			old := NewTrafficPoll(sid, map[string]UsageDelta{name: {Down: 17}})
			old.Mode = mode
			old.Epoch = "old-core"
			trafficCompatRecord(t, st, old)
			trafficCompatSource(t, st, old, name, "ambiguous_identity", "relay_identity_collision", 0)
			internal, err := st.legacyRelayStatsName(sid, ib)
			if err != nil {
				t.Fatal(err)
			}
			again, err := st.legacyRelayStatsName(sid, ib)
			if err != nil || again != internal || internal == name {
				t.Fatalf("unstable label %q %q %v", internal, again, err)
			}
			target, err := st.GetSbInbound(ib)
			if err != nil || target.RelaySecret != "same-wire-secret" {
				t.Fatal("wire credential changed")
			}
			if err = st.RecordRelayNamespaceEpoch(sid, "bad", []byte(fmt.Sprintf(`{"inbounds":[{"users":[{"name":%q}]}]}`, name))); err == nil {
				t.Fatal("desired old namespace accepted")
			}
			raw := []byte(fmt.Sprintf(`{"inbounds":[{"type":"vless","users":[{"name":%q}]},{"type":"mixed","users":[{"username":%q}]}]}`, internal, name))
			if err = st.RecordRelayNamespaceEpoch(sid, "separated-core", raw); err != nil {
				t.Fatal(err)
			}
			p := NewTrafficPoll(sid, map[string]UsageDelta{name: {Down: 100}, internal: {Down: 200}})
			p.Mode = mode
			p.Epoch = "separated-core"
			trafficCompatRecord(t, st, p)
			trafficCompatRecord(t, st, p)
			trafficCompatSource(t, st, p, name, "direct_user", "", uid)
			trafficCompatSource(t, st, p, internal, "legacy_shared_relay", "", 0)
			trafficCompatTotals(t, st, uid, bucket, 0, 100)
			// Old mixed samples never get reinterpreted after the proof is installed.
			trafficCompatRecord(t, st, old)
			trafficCompatTotals(t, st, uid, bucket, 0, 100)
			hash, err := st.LatestRelayNamespaceProof(sid)
			if err != nil {
				t.Fatal(err)
			}
			if err = st.ConfirmRelayNamespaceEpoch(sid, "restarted-core", hash); err != nil {
				t.Fatal(err)
			}
			next := NewTrafficPoll(sid, map[string]UsageDelta{name: {Down: 25}})
			next.Mode = mode
			next.Epoch = "restarted-core"
			trafficCompatRecord(t, st, next)
			trafficCompatTotals(t, st, uid, bucket, 0, 125)
			// A delayed poll from a previously proven epoch remains supported.
			late := NewTrafficPoll(sid, map[string]UsageDelta{name: {Down: 110}})
			late.Mode = mode
			late.Epoch = "separated-core"
			if mode == "reset" {
				late.Traffic[name] = UsageDelta{Down: 10}
			}
			trafficCompatRecord(t, st, late)
			trafficCompatTotals(t, st, uid, bucket, 0, 135)
		})
	}
}

func TestLegacyNamespaceRegistrationDoesNotRequireEncryptionKey(t *testing.T) {
	st := newRefundStore(t)
	name, err := st.legacyRelayStatsName(4, 9)
	if err != nil || name == "" {
		t.Fatalf("default-off old relay requires a new key: %q %v", name, err)
	}
	if err = st.ConfirmRelayNamespaceEpoch(4, "unproved", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err == nil {
		t.Fatal("unseen config hash acquired epoch proof")
	}
}
