package api

import (
	"math"
	"strings"
	"testing"
	"time"

	"qingzhou/internal/store"
)

func TestDeviceTrafficThresholdDoesNotOverflow(t *testing.T) {
	if !deviceTrafficThresholdReached(math.MaxInt64, math.MaxInt64, 100) {
		t.Fatal("max-int usage should reach a 100% max-int limit")
	}
	if deviceTrafficThresholdReached(math.MaxInt64-1, math.MaxInt64, 100) {
		t.Fatal("one byte below max-int limit must not reach 100%")
	}
	if !deviceTrafficThresholdReached(80, 100, 80) || deviceTrafficThresholdReached(79, 100, 80) {
		t.Fatal("ordinary 80% threshold comparison is wrong")
	}
}

func TestDeviceExpiryCountPolicyAndDailyDedup(t *testing.T) {
	a, st, inbox := newTelegramAPI(t)
	bindOps(t, st, "expiry_ops", "admin", 7101, true)
	now := time.Date(2026, 8, 1, 9, 0, 0, 0, time.Local)
	_, err := st.CreateServer(store.Server{
		Name: "edge-1", Host: "203.0.113.8", Enabled: true,
		ExpiryDate: now.AddDate(0, 0, 8).Unix(), ExpiryNotifyEnabled: true,
		ExpiryNotifyDays: 10, ExpiryNotifyMode: "count", ExpiryNotifyCount: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	a.sweepDeviceNotifications(now)
	a.sweepDeviceNotifications(now.Add(3 * time.Hour)) // same day: deduplicated
	a.sweepDeviceNotifications(now.AddDate(0, 0, 1))   // second and final send
	a.sweepDeviceNotifications(now.AddDate(0, 0, 2))
	if len(*inbox) != 2 {
		t.Fatalf("sent %d expiry messages, want 2: %#v", len(*inbox), *inbox)
	}
	if !strings.Contains((*inbox)[0].html, "edge-1") || !strings.Contains((*inbox)[0].html, "设备即将到期") {
		t.Fatalf("unexpected message: %q", (*inbox)[0].html)
	}
}

func TestDeviceTrafficAlertsOncePerBillingCycle(t *testing.T) {
	a, st, inbox := newTelegramAPI(t)
	bindOps(t, st, "traffic_ops", "admin", 7201, true)
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	id, err := st.CreateServer(store.Server{
		Name: "traffic-1", Host: "203.0.113.9", Enabled: true,
		TrafficLimitBytes: 1000, TrafficResetDay: 1, TrafficAlertPercent: 80,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.InsertMetrics(id, store.ServerMetrics{Ts: now.Add(-time.Hour).Unix(), NetTotalsValid: true, NetRxTotal: 100}); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertMetrics(id, store.ServerMetrics{Ts: now.Unix(), NetTotalsValid: true, NetRxTotal: 950}); err != nil {
		t.Fatal(err)
	}
	a.sweepDeviceNotifications(now)
	a.sweepDeviceNotifications(now.Add(time.Hour))
	if len(*inbox) != 1 {
		t.Fatalf("sent %d traffic messages, want 1: %#v", len(*inbox), *inbox)
	}
	if !strings.Contains((*inbox)[0].html, "设备月流量告警") || !strings.Contains((*inbox)[0].html, "traffic-1") {
		t.Fatalf("unexpected message: %q", (*inbox)[0].html)
	}
	if alert := unreadOfAPI(t, st, "traffic_threshold"); alert == nil {
		t.Fatal("traffic threshold did not appear in panel alerts")
	}
}

func TestDeviceTrafficAlertUsesManualCalibration(t *testing.T) {
	a, st, inbox := newTelegramAPI(t)
	bindOps(t, st, "calibration_ops", "admin", 7301, true)
	now := time.Date(2026, 8, 20, 12, 0, 0, 0, time.Local)
	id, err := st.CreateServer(store.Server{
		Name: "calibrated-traffic", Host: "203.0.113.10", Enabled: true,
		TrafficLimitBytes: 1000, TrafficResetDay: 16, TrafficAlertPercent: 80,
	})
	if err != nil {
		t.Fatal(err)
	}
	cycleStart := store.TrafficCycleStart(now, 16, 0).Unix()
	if err := st.SetTrafficCalibration(id, cycleStart, 900, now.Unix()); err != nil {
		t.Fatal(err)
	}

	a.sweepDeviceNotifications(now)
	if len(*inbox) != 1 {
		t.Fatalf("sent %d calibrated traffic messages, want 1: %#v", len(*inbox), *inbox)
	}
	if !strings.Contains((*inbox)[0].html, "calibrated-traffic") || !strings.Contains((*inbox)[0].html, "0.88 KB") {
		t.Fatalf("unexpected calibrated traffic message: %q", (*inbox)[0].html)
	}
}

func unreadOfAPI(t *testing.T, st *store.Store, kind string) *store.ServerAlert {
	t.Helper()
	alerts, err := st.ListAlerts(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, alert := range alerts {
		if alert.Type == kind {
			return alert
		}
	}
	return nil
}
