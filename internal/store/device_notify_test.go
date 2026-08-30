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

// A provider reset day is the boundary of its billing cycle, not an annotation
// on a calendar-month total. On August 30, a day-16 machine must start at
// August 16; before that boundary it must still start at July 16.
func TestTrafficCycleStartsOnConfiguredDay(t *testing.T) {
	loc := time.FixedZone("panel", 8*3600)
	reset := 8 * 60

	before := time.Date(2026, time.August, 15, 23, 59, 0, 0, loc)
	if got := TrafficCycleStart(before, 16, reset); got != time.Date(2026, time.July, 16, 8, 0, 0, 0, loc) {
		t.Fatalf("before day-16 reset: got %v", got)
	}

	after := time.Date(2026, time.August, 30, 12, 0, 0, 0, loc)
	if got := TrafficCycleStart(after, 16, reset); got != time.Date(2026, time.August, 16, 8, 0, 0, 0, loc) {
		t.Fatalf("after day-16 reset: got %v", got)
	}
	if got := TrafficCycleNext(after, 16, reset); got != time.Date(2026, time.September, 16, 8, 0, 0, 0, loc) {
		t.Fatalf("next day-16 reset: got %v", got)
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

func TestTrafficUsageMatchesProviderAccountingMode(t *testing.T) {
	st := newRefundStore(t)
	if _, err := st.db.Exec(`INSERT INTO server_metrics
		(server_id,ts,net_totals_valid,net_rx_bytes,net_tx_bytes) VALUES (1,100,1,30,70)`); err != nil {
		t.Fatal(err)
	}
	cycles := map[int64]TrafficCycleQuery{
		1: {Start: 0, AccountingMode: TrafficAccountingSum},
		2: {Start: 0, AccountingMode: TrafficAccountingMax},
		3: {Start: 0, AccountingMode: TrafficAccountingRx},
		4: {Start: 0, AccountingMode: TrafficAccountingTx},
	}
	// Give every mode the same raw split.
	for id := int64(2); id <= 4; id++ {
		if _, err := st.db.Exec(`INSERT INTO server_metrics
			(server_id,ts,net_totals_valid,net_rx_bytes,net_tx_bytes) VALUES (?,100,1,30,70)`, id); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.TrafficUsageForBillingCycles(cycles)
	if err != nil {
		t.Fatal(err)
	}
	want := map[int64]int64{1: 100, 2: 70, 3: 30, 4: 70}
	for id, total := range want {
		if got[id].Total != total {
			t.Fatalf("mode %q total = %d, want %d", cycles[id].AccountingMode, got[id].Total, total)
		}
	}
}

func TestTrafficCalibrationIsScopedToAccountingMode(t *testing.T) {
	st := newRefundStore(t)
	if _, err := st.db.Exec(`INSERT INTO server_metrics
		(server_id,ts,net_totals_valid,net_rx_bytes,net_tx_bytes) VALUES (1,100,1,30,70)`); err != nil {
		t.Fatal(err)
	}
	if err := st.SetTrafficCalibrationForMode(1, 0, TrafficAccountingMax, 200, 110); err != nil {
		t.Fatal(err)
	}
	maxUsage, err := st.TrafficUsageForBillingCycles(map[int64]TrafficCycleQuery{
		1: {Start: 0, AccountingMode: TrafficAccountingMax},
	})
	if err != nil {
		t.Fatal(err)
	}
	if u := maxUsage[1]; u.Total != 200 || !u.Calibrated {
		t.Fatalf("max calibration = %+v", u)
	}
	sumUsage, err := st.TrafficUsageForBillingCycles(map[int64]TrafficCycleQuery{
		1: {Start: 0, AccountingMode: TrafficAccountingSum},
	})
	if err != nil {
		t.Fatal(err)
	}
	if u := sumUsage[1]; u.Total != 100 || u.Calibrated {
		t.Fatalf("sum usage reused max-mode calibration: %+v", u)
	}
}

func TestTrafficUsageForDay16ExcludesCalendarMonthPrefix(t *testing.T) {
	st := newRefundStore(t)
	loc := time.FixedZone("panel", 8*3600)
	serverID := int64(1)
	for _, row := range []struct {
		at    time.Time
		bytes int64
	}{
		{time.Date(2026, time.August, 1, 12, 0, 0, 0, loc), 10},
		{time.Date(2026, time.August, 15, 23, 59, 0, 0, loc), 20},
		{time.Date(2026, time.August, 16, 8, 0, 0, 0, loc), 30},
		{time.Date(2026, time.August, 30, 12, 0, 0, 0, loc), 40},
	} {
		if _, err := st.db.Exec(`INSERT INTO server_metrics
			(server_id,ts,net_totals_valid,net_rx_bytes,net_tx_bytes) VALUES (?,?,1,?,0)`,
			serverID, row.at.Unix(), row.bytes); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Date(2026, time.August, 30, 12, 0, 0, 0, loc)
	start := TrafficCycleStart(now, 16, 8*60).Unix()
	got, err := st.TrafficUsageForCycles(map[int64]int64{serverID: start})
	if err != nil {
		t.Fatal(err)
	}
	if u := got[serverID]; u.Total != 70 || u.SampleCount != 2 {
		t.Fatalf("day-16 cycle usage = %+v, want only Aug 16 and Aug 30 (70 bytes)", u)
	}
}

func TestTrafficCalibrationAppliesOnlyToCurrentCycle(t *testing.T) {
	st := newRefundStore(t)
	serverID := int64(1)
	for _, row := range []struct{ ts, bytes int64 }{
		{100, 10}, {200, 20},
	} {
		if _, err := st.db.Exec(`INSERT INTO server_metrics
			(server_id,ts,net_totals_valid,net_rx_bytes,net_tx_bytes) VALUES (?,?,1,?,0)`,
			serverID, row.ts, row.bytes); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetTrafficCalibration(serverID, 50, 100, 210); err != nil {
		t.Fatal(err)
	}

	got, err := st.TrafficUsageForCycles(map[int64]int64{serverID: 50})
	if err != nil {
		t.Fatal(err)
	}
	if u := got[serverID]; u.Total != 100 || !u.Calibrated || u.CalibratedAt != 210 {
		t.Fatalf("calibrated usage = %+v, want total=100 at 210", u)
	}

	// New physical-interface deltas continue from the provider-entered baseline.
	if _, err := st.db.Exec(`INSERT INTO server_metrics
		(server_id,ts,net_totals_valid,net_rx_bytes,net_tx_bytes) VALUES (?,?,1,?,0)`,
		serverID, 300, 25); err != nil {
		t.Fatal(err)
	}
	got, err = st.TrafficUsageForCycles(map[int64]int64{serverID: 50})
	if err != nil {
		t.Fatal(err)
	}
	if u := got[serverID]; u.Total != 125 || !u.Calibrated {
		t.Fatalf("usage after calibration delta = %+v, want total=125", u)
	}

	// Once the next billing cycle begins, the old offset is ignored.
	got, err = st.TrafficUsageForCycles(map[int64]int64{serverID: 250})
	if err != nil {
		t.Fatal(err)
	}
	if u := got[serverID]; u.Total != 25 || u.Calibrated {
		t.Fatalf("next-cycle usage = %+v, want raw total=25 without calibration", u)
	}
}

func TestTrafficCalibrationWorksBeforeProbeHasHistory(t *testing.T) {
	st := newRefundStore(t)
	if err := st.SetTrafficCalibration(9, 100, 12*giB, 150); err != nil {
		t.Fatal(err)
	}
	got, err := st.TrafficUsageForCycles(map[int64]int64{9: 100})
	if err != nil {
		t.Fatal(err)
	}
	if u := got[9]; u.Total != 12*giB || !u.Calibrated || u.SampleCount != 0 {
		t.Fatalf("calibration without history = %+v", u)
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
