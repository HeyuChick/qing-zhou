package store

import "testing"

// GetLatestMetricsForAll must return exactly one row per server — the most recent
// one — in a single query, so callers can drop the per-server GetLatestMetrics loop.
func TestGetLatestMetricsForAll(t *testing.T) {
	st := newRefundStore(t)

	// server 1: two snapshots; the later insert (higher id) is the latest.
	if err := st.InsertMetrics(1, ServerMetrics{Ts: 100, CPUPercent: 10}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertMetrics(1, ServerMetrics{Ts: 200, CPUPercent: 55}); err != nil {
		t.Fatal(err)
	}
	// server 2: a single snapshot.
	if err := st.InsertMetrics(2, ServerMetrics{Ts: 150, CPUPercent: 33}); err != nil {
		t.Fatal(err)
	}

	latest, err := st.GetLatestMetricsForAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(latest) != 2 {
		t.Fatalf("got %d servers, want 2", len(latest))
	}
	if latest[1] == nil || latest[1].CPUPercent != 55 {
		t.Errorf("server 1 latest cpu = %v, want 55 (the newer snapshot)", latest[1])
	}
	if latest[2] == nil || latest[2].CPUPercent != 33 {
		t.Errorf("server 2 latest cpu = %v, want 33", latest[2])
	}

	// Must agree with the single-server accessor.
	single, _ := st.GetLatestMetrics(1)
	if single == nil || single.CPUPercent != latest[1].CPUPercent {
		t.Errorf("GetLatestMetricsForAll[1] disagrees with GetLatestMetrics(1)")
	}
}
