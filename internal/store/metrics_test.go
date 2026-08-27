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
	if err := st.InsertMetrics(1, ServerMetrics{Ts: 200, CPUPercent: 55, ProbeVersion: "v1.2.3"}); err != nil {
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
	if latest[1].ProbeVersion != "v1.2.3" {
		t.Errorf("server 1 probe version = %q, want v1.2.3", latest[1].ProbeVersion)
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

func TestTrafficUsageForAllSinceCountsBothDirectionsAndReboots(t *testing.T) {
	st := newRefundStore(t)
	put := func(serverID, ts, rx, tx, uptime int64) {
		t.Helper()
		if err := st.InsertMetrics(serverID, ServerMetrics{
			Ts: ts, NetRxTotal: rx, NetTxTotal: tx,
			NetTotalsValid: true, Uptime: uptime,
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Normal deltas: IN 300, OUT 600.
	// Probe-supplied interval fields are ignored; only the panel may derive them.
	if err := st.InsertMetrics(1, ServerMetrics{
		Ts: 100, NetRxTotal: 1000, NetTxTotal: 2000, NetTotalsValid: true,
		NetRxBytes: 999999, NetTxBytes: 999999, Uptime: 100,
	}); err != nil {
		t.Fatal(err)
	}
	put(1, 200, 1300, 2600, 200)
	// A reboot resets both counters. Bytes accumulated since boot still belong to
	// the range and must be counted: IN 50, OUT 80.
	put(1, 300, 50, 80, 10)
	// A NIC-only reset (uptime keeps increasing) is not safe to interpret as a
	// full new counter, so that interval is conservatively ignored.
	put(1, 400, 10, 20, 110)

	usage, err := st.TrafficUsageForAllSince(100)
	if err != nil {
		t.Fatal(err)
	}
	u := usage[1]
	if u.Rx != 350 || u.Tx != 680 || u.Total != 1030 {
		t.Fatalf("usage = %+v, want rx=350 tx=680 total=1030", u)
	}
	if u.SampleCount != 4 || u.CoverageStart != 100 || u.CoverageEnd != 400 {
		t.Fatalf("coverage = %+v, want 4 samples spanning 100..400", u)
	}
}

func TestTrafficUsageIgnoresLegacyRateOnlyRows(t *testing.T) {
	st := newRefundStore(t)
	if err := st.InsertMetrics(1, ServerMetrics{Ts: 100, NetRx: 1000, NetTx: 2000}); err != nil {
		t.Fatal(err)
	}
	usage, err := st.TrafficUsageForAllSince(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := usage[1]; ok {
		t.Fatalf("legacy row without cumulative counters must not look like measured usage: %+v", usage[1])
	}
}
