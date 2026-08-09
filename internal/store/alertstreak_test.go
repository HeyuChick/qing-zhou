package store

import (
	"testing"
	"time"
)

// probeServer creates a probe-enabled server whose metrics are fresh, so
// CheckProbeAlerts evaluates the metric-based conditions rather than skipping
// them as stale.
func probeServer(t *testing.T, st *Store, cpu float64) int64 {
	t.Helper()
	id, err := st.CreateServer(Server{
		Name: "n1", Host: "203.0.113.1", Enabled: true,
		ProbeEnabled: true, ProbeToken: "tok",
	})
	if err != nil {
		t.Fatal(err)
	}
	writeCPU(t, st, id, cpu)
	return id
}

func writeCPU(t *testing.T, st *Store, id int64, cpu float64) {
	t.Helper()
	if err := st.InsertMetrics(id, ServerMetrics{
		Ts: time.Now().Unix(), CPUPercent: cpu,
		MemTotal: 1000, MemUsed: 1, DiskTotal: 1000, DiskUsed: 1,
	}); err != nil {
		t.Fatal(err)
	}
	// last_seen has to be recent or the offline branch fires instead.
	if _, err := st.db.Exec(`UPDATE servers SET last_seen=? WHERE id=?`, time.Now().Unix(), id); err != nil {
		t.Fatal(err)
	}
}

// A one-sample CPU spike — a build, a backup, a log rotation — must not alert.
func TestSingleSpikeDoesNotAlert(t *testing.T) {
	st := newRefundStore(t)
	probeServer(t, st, 99)

	if err := st.CheckProbeAlerts(); err != nil {
		t.Fatal(err)
	}
	if a := unreadOf(t, st, "high_cpu"); a != nil {
		t.Fatalf("alerted on the first sample: %+v", a)
	}
}

// Sustained load must still alert — on the second consecutive check by default.
func TestSustainedLoadAlerts(t *testing.T) {
	st := newRefundStore(t)
	id := probeServer(t, st, 99)

	if err := st.CheckProbeAlerts(); err != nil {
		t.Fatal(err)
	}
	writeCPU(t, st, id, 97)
	if err := st.CheckProbeAlerts(); err != nil {
		t.Fatal(err)
	}
	a := unreadOf(t, st, "high_cpu")
	if a == nil {
		t.Fatal("sustained high CPU never alerted")
	}
	if a.Hits != 1 {
		t.Errorf("hits = %d, want 1 — the suppressed sample must not be counted", a.Hits)
	}
}

// An on/off/on pattern must not accumulate: the streak restarts once the
// condition genuinely clears, so flapping never reaches the threshold.
func TestFlappingNeverAlerts(t *testing.T) {
	st := newRefundStore(t)
	id := probeServer(t, st, 99)

	for i := 0; i < 6; i++ {
		if i%2 == 1 {
			writeCPU(t, st, id, 5) // recovered
		} else {
			writeCPU(t, st, id, 99)
		}
		if err := st.CheckProbeAlerts(); err != nil {
			t.Fatal(err)
		}
	}
	if a := unreadOf(t, st, "high_cpu"); a != nil {
		t.Fatalf("flapping produced an alert: %+v", a)
	}
}

// The threshold is configurable, and 1 restores alert-on-first-sample for anyone
// who preferred the old behaviour.
func TestAlertStreakSettingHonoured(t *testing.T) {
	st := newRefundStore(t)
	if err := st.SetSetting(SettingAlertStreak, "1"); err != nil {
		t.Fatal(err)
	}
	probeServer(t, st, 99)
	if err := st.CheckProbeAlerts(); err != nil {
		t.Fatal(err)
	}
	if unreadOf(t, st, "high_cpu") == nil {
		t.Error("threshold of 1 should alert immediately")
	}
}

func TestAlertStreakSettingIsClamped(t *testing.T) {
	st := newRefundStore(t)
	for _, c := range []struct {
		set  string
		want int
	}{
		{"", defaultAlertStreak},
		{"0", 1},
		{"-5", 1},
		{"3", 3},
		{"999", 10},
		{"nonsense", defaultAlertStreak},
	} {
		if err := st.SetSetting(SettingAlertStreak, c.set); err != nil {
			t.Fatal(err)
		}
		if got := st.alertStreakRequired(); got != c.want {
			t.Errorf("alert_consecutive=%q → %d, want %d", c.set, got, c.want)
		}
	}
}

// A date-based condition is not sampled and must not be delayed — shaving a day
// off "expires in 3 days" to look steadier helps nobody.
func TestExpiryAlertsAreImmediate(t *testing.T) {
	st := newRefundStore(t)
	id, err := st.CreateServer(Server{
		Name: "n1", Host: "203.0.113.1", Enabled: true,
		ProbeEnabled: true, ProbeToken: "tok",
		ExpiryDate: time.Now().Add(48 * time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	writeCPU(t, st, id, 5)
	if err := st.CheckProbeAlerts(); err != nil {
		t.Fatal(err)
	}
	if unreadOf(t, st, "expiring") == nil {
		t.Error("expiry warning was delayed by the streak gate")
	}
}

// Deleting a server is the only path that removes it from every check without
// its conditions ever going through "resolved", so its counters would otherwise
// live in the map until the process restarts.
func TestStreaksArePrunedForDeletedServers(t *testing.T) {
	st := newRefundStore(t)
	id := probeServer(t, st, 99)
	if err := st.CheckProbeAlerts(); err != nil {
		t.Fatal(err)
	}
	st.streakMu.Lock()
	n := len(st.streaks)
	st.streakMu.Unlock()
	if n == 0 {
		t.Fatal("no streak was recorded to begin with")
	}

	if err := st.DeleteServer(id); err != nil {
		t.Fatalf("delete server: %v", err)
	}
	if err := st.CheckProbeAlerts(); err != nil {
		t.Fatal(err)
	}
	st.streakMu.Lock()
	defer st.streakMu.Unlock()
	for k := range st.streaks {
		if k.server == id {
			t.Errorf("counter for deleted server %d survived", id)
		}
	}
}
