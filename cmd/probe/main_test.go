//go:build linux

package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"qingzhou/internal/sysmetrics"
)

func TestReportReadsPanelInterval(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"code":0,"msg":"","data":{"ok":true,"probe_interval_seconds":120}}`))
	}))
	defer srv.Close()

	got, err := report(srv.Client(), srv.URL, "secret", sysmetrics.Metrics{CPUPercent: 1})
	if err != nil {
		t.Fatal(err)
	}
	if got != 120 {
		t.Fatalf("interval = %d", got)
	}
	if auth != "Bearer secret" {
		t.Fatalf("authorization = %q", auth)
	}
}

func TestReportKeepsIntervalWithOlderPanel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":0,"msg":"","data":{"ok":true}}`))
	}))
	defer srv.Close()

	got, err := report(srv.Client(), srv.URL, "secret", sysmetrics.Metrics{})
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("old panel interval = %d, want 0", got)
	}
}
