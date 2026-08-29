package store

import (
	"testing"
	"time"
)

func TestTrafficCycleClampsShortMonths(t *testing.T) {
	loc := time.FixedZone("panel", 8*3600)
	before := time.Date(2028, time.February, 28, 7, 59, 0, 0, loc)
	if got := TrafficCycleStart(before, 31, 8*60); got != time.Date(2028, time.January, 31, 8, 0, 0, 0, loc) {
		t.Fatalf("before February reset: got %v", got)
	}
	after := time.Date(2028, time.February, 29, 8, 1, 0, 0, loc)
	if got := TrafficCycleStart(after, 31, 8*60); got != time.Date(2028, time.February, 29, 8, 0, 0, 0, loc) {
		t.Fatalf("leap-month reset: got %v", got)
	}
	if got := TrafficCycleNext(after, 31, 8*60); got != time.Date(2028, time.March, 31, 8, 0, 0, 0, loc) {
		t.Fatalf("next reset: got %v", got)
	}
}

func TestTrafficUsageForDifferentDeviceCycles(t *testing.T) {
	st := newRefundStore(t)
	for _, row := range []struct{ id, ts, bytes int64 }{
		{1, 100, 10}, {1, 200, 20}, {2, 100, 30}, {2, 200, 40},
	} {
		if _, err := st.db.Exec(`INSERT INTO server_metrics
			(server_id,ts,net_totals_valid,net_rx_bytes,net_tx_bytes) VALUES (?,?,1,?,0)`,
			row.id, row.ts, row.bytes); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.TrafficUsageForCycles(map[int64]int64{1: 150, 2: 50})
	if err != nil {
		t.Fatal(err)
	}
	if got[1].Total != 20 || got[1].SampleCount != 1 {
		t.Fatalf("server 1 usage = %+v", got[1])
	}
	if got[2].Total != 70 || got[2].SampleCount != 2 {
		t.Fatalf("server 2 usage = %+v", got[2])
	}
}

func TestDeviceNotifyStatePersistsCount(t *testing.T) {
	st := newRefundStore(t)
	if err := st.MarkDeviceNotifySent(8, "device_expiry", "expiry:123", "2026-08-29", 100); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkDeviceNotifySent(8, "device_expiry", "expiry:123", "2026-08-30", 200); err != nil {
		t.Fatal(err)
	}
	state, err := st.DeviceNotifyState(8, "device_expiry", "expiry:123")
	if err != nil {
		t.Fatal(err)
	}
	if state.SentCount != 2 || state.LastSentDay != "2026-08-30" {
		t.Fatalf("state = %+v", state)
	}
}
