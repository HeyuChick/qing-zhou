package api

import (
	"strings"
	"testing"
	"time"

	"qingzhou/internal/store"
)

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
