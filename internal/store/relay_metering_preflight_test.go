package store

import "testing"

func TestRelayMeteringReadOnlyPreflight(t *testing.T) {
	st, _, _, _, _, _ := meteringRelayFixture(t)
	if err := st.ConfigureTrafficMetering(false, false, false); err != nil {
		t.Fatal(err)
	}
	before := scalar(t, st, `SELECT COUNT(*) FROM relay_metering_links`)
	if err := st.PreflightTrafficMetering(true, true, true); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{RelayMeteringSetting, RelayUserMeteringSetting, "traffic_cumulative_metering"} {
		if value, err := st.GetSetting(key); err != nil || value != "false" {
			t.Fatalf("preflight persisted %s=%s: %v", key, value, err)
		}
	}
	if got := scalar(t, st, `SELECT COUNT(*) FROM relay_metering_links`); got != before {
		t.Fatal("preflight created relay identities")
	}
	if got := scalar(t, st, `SELECT COUNT(*) FROM relay_metering_users`); got != 0 {
		t.Fatal("preflight created per-user identities")
	}
	if err := st.PreflightTrafficMetering(false, false, true); err == nil {
		t.Fatal("read-only preflight skipped the real save's dependency validation")
	}
	if err := st.ConfigureTrafficMetering(true, true, true); err != nil {
		t.Fatal(err)
	}
	if !st.RelayUserMeteringEnabled() {
		t.Fatal("actual save did not retain its mutation semantics")
	}
}
