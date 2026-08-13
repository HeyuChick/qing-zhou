package store

import (
	"path/filepath"
	"testing"
)

func newAlertStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatal(err)
	}
	return st
}

// The panel's own machine is watched without a servers row, so the alert
// checker — which walks that table — would otherwise never look at it. That
// makes it the one machine that can fill its disk in silence, and it is the
// machine whose disk filling up takes the panel itself down.
func TestLocalNodeGetsAlerts(t *testing.T) {
	st := newAlertStore(t)

	// Nothing sampled yet: nothing to judge, and nothing invented.
	if n := st.localAlertNode(); n != nil {
		t.Fatalf("no local metrics yet, want no pseudo-server, got %+v", n)
	}
	if err := st.CheckProbeAlerts(); err != nil {
		t.Fatalf("alert check on an empty panel: %v", err)
	}

	// Alert on the first sample rather than waiting out the anti-flap streak.
	if err := st.SetSetting(SettingAlertStreak, "1"); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertMetrics(LocalNodeID, ServerMetrics{
		CPUPercent: 99, MemUsed: 1, MemTotal: 100, DiskUsed: 1, DiskTotal: 100,
	}); err != nil {
		t.Fatal(err)
	}
	node := st.localAlertNode()
	if node == nil || node.ID != LocalNodeID || node.Name != LocalNodeName || !node.ProbeEnabled {
		t.Fatalf("local pseudo-server = %+v, want a live, probe-enabled row", node)
	}
	// LastSeen has to come from the newest sample: with no last_seen column to
	// touch, a zero here would make the machine permanently "offline".
	if node.LastSeen == 0 {
		t.Fatal("LastSeen must be derived from the latest sample")
	}

	if err := st.CheckProbeAlerts(); err != nil {
		t.Fatal(err)
	}
	alerts, err := st.ListAlerts(false)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, al := range alerts {
		if al.ServerID == LocalNodeID && al.Type == "high_cpu" {
			found = true
		}
	}
	if !found {
		t.Fatalf("want a high_cpu alert for the panel's own machine, got %+v", alerts)
	}
}
