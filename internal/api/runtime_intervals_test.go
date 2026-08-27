package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"qingzhou/internal/intervalcfg"
	"qingzhou/internal/store"
)

func putSettings(a *API, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	a.handlePutSettings(w, httptest.NewRequest(http.MethodPut, "/api/admin/settings", strings.NewReader(body)))
	return w
}

func TestRuntimeIntervalsRoundTrip(t *testing.T) {
	t.Setenv(intervalcfg.EnvProbeInterval, "")
	t.Setenv(intervalcfg.EnvStatsInterval, "")
	t.Setenv(intervalcfg.EnvReconcileInterval, "")
	a, st := newUserEditAPI(t)

	w := putSettings(a, `{
		"monitor_probe_interval_seconds":"60",
		"singbox_stats_interval_minutes":"10",
		"singbox_reconcile_interval_minutes":"60"
	}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	for key, want := range map[string]string{
		intervalcfg.SettingProbeSeconds:     "60",
		intervalcfg.SettingStatsMinutes:     "10",
		intervalcfg.SettingReconcileMinutes: "60",
	} {
		if got, _ := st.GetSetting(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if !strings.Contains(w.Body.String(), `"_user_online_window_seconds":"1230"`) {
		t.Fatalf("settings response missing derived online window: %s", w.Body.String())
	}
	if got, _ := st.GetSetting("_user_online_window_seconds"); got != "" {
		t.Fatalf("derived online window was persisted: %q", got)
	}
}

func TestUserOnlineWindowFollowsLiveStatsSetting(t *testing.T) {
	t.Setenv(intervalcfg.EnvProbeInterval, "")
	t.Setenv(intervalcfg.EnvStatsInterval, "")
	t.Setenv(intervalcfg.EnvReconcileInterval, "")
	a, st := newUserEditAPI(t)
	if err := st.SetSetting(intervalcfg.SettingStatsMinutes, "10"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	uid, err := st.CreateUser(store.NewUser{Username: "alice", PasswordHash: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`UPDATE users SET last_online_at=? WHERE id=?`, now-8*60, uid); err != nil {
		t.Fatal(err)
	}

	onlineAt := func() bool {
		u, err := st.UserByID(uid)
		if err != nil {
			t.Fatal(err)
		}
		v := a.adminUserViewLoadGroups(u)
		on, _ := v["online"].(bool)
		return on
	}
	if !onlineAt() {
		t.Fatal("8 minutes ago must count as online while stats interval is 10 minutes")
	}

	w := putSettings(a, `{
		"monitor_probe_interval_seconds":"60",
		"singbox_stats_interval_minutes":"1",
		"singbox_reconcile_interval_minutes":"10"
	}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"_user_online_window_seconds":"150"`) {
		t.Fatalf("saved settings did not recompute online window: %s", w.Body.String())
	}
	if onlineAt() {
		t.Fatal("shortening the stats interval must drop the same user out of the online window without a restart")
	}
	n, err := st.OnlineCount(st.UserOnlineWindowSec())
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("OnlineCount = %d, want 0 after live interval change", n)
	}
}

func TestUnchangedRuntimeIntervalsDoNotPostponeTimers(t *testing.T) {
	t.Setenv(intervalcfg.EnvProbeInterval, "")
	t.Setenv(intervalcfg.EnvStatsInterval, "")
	t.Setenv(intervalcfg.EnvReconcileInterval, "")
	a, st := newUserEditAPI(t)
	_ = st.SetSetting(intervalcfg.SettingProbeSeconds, "60")
	_ = st.SetSetting(intervalcfg.SettingStatsMinutes, "10")
	_ = st.SetSetting(intervalcfg.SettingReconcileMinutes, "60")

	w := putSettings(a, `{
		"monitor_probe_interval_seconds":"60",
		"singbox_stats_interval_minutes":"10",
		"singbox_reconcile_interval_minutes":"60"
	}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	select {
	case <-a.monitorIntervalCh:
		t.Fatal("an unrelated settings save would reset and postpone the live sampler")
	default:
	}
}

func TestChangedRuntimeIntervalsWakeLiveSchedulers(t *testing.T) {
	t.Setenv(intervalcfg.EnvProbeInterval, "")
	t.Setenv(intervalcfg.EnvStatsInterval, "")
	t.Setenv(intervalcfg.EnvReconcileInterval, "")
	a, st := newUserEditAPI(t)
	_ = st.SetSetting(intervalcfg.SettingProbeSeconds, "30")
	_ = st.SetSetting(intervalcfg.SettingStatsMinutes, "1")
	_ = st.SetSetting(intervalcfg.SettingReconcileMinutes, "10")

	w := putSettings(a, `{
		"monitor_probe_interval_seconds":"60",
		"singbox_stats_interval_minutes":"10",
		"singbox_reconcile_interval_minutes":"60"
	}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	select {
	case <-a.monitorIntervalCh:
	default:
		t.Fatal("changed intervals did not wake the live sampler")
	}
}

func TestRuntimeIntervalsRejectBeforeWriting(t *testing.T) {
	t.Setenv(intervalcfg.EnvProbeInterval, "")
	t.Setenv(intervalcfg.EnvStatsInterval, "")
	t.Setenv(intervalcfg.EnvReconcileInterval, "")
	a, st := newUserEditAPI(t)
	_ = st.SetSetting(intervalcfg.SettingProbeSeconds, "60")
	_ = st.SetSetting(intervalcfg.SettingStatsMinutes, "10")
	_ = st.SetSetting(intervalcfg.SettingReconcileMinutes, "60")

	w := putSettings(a, `{
		"monitor_probe_interval_seconds":"120",
		"singbox_stats_interval_minutes":"20",
		"singbox_reconcile_interval_minutes":"10"
	}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400: %s", w.Code, w.Body.String())
	}
	if got, _ := st.GetSetting(intervalcfg.SettingProbeSeconds); got != "60" {
		t.Fatalf("probe interval was partially written: %q", got)
	}
}

func TestRuntimeIntervalEnvironmentOverrideIsReadOnly(t *testing.T) {
	t.Setenv(intervalcfg.EnvProbeInterval, "5m")
	t.Setenv(intervalcfg.EnvStatsInterval, "")
	t.Setenv(intervalcfg.EnvReconcileInterval, "")
	a, st := newUserEditAPI(t)
	_ = st.SetSetting(intervalcfg.SettingProbeSeconds, "60")

	w := putSettings(a, `{"monitor_probe_interval_seconds":"600"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	if got, _ := st.GetSetting(intervalcfg.SettingProbeSeconds); got != "60" {
		t.Fatalf("env-locked setting overwrote DB fallback: %q", got)
	}
	if !strings.Contains(w.Body.String(), `"monitor_probe_interval_seconds":"300"`) {
		t.Fatalf("response does not expose effective env value: %s", w.Body.String())
	}
}

func TestMonitorReportReturnsLiveProbeInterval(t *testing.T) {
	t.Setenv(intervalcfg.EnvProbeInterval, "")
	a, st := newUserEditAPI(t)
	if err := st.SetSetting(intervalcfg.SettingProbeSeconds, "120"); err != nil {
		t.Fatal(err)
	}
	_, err := st.CreateServer(store.Server{
		Name: "node-1", Host: "192.0.2.1", Enabled: true,
		ProbeEnabled: true, ProbeToken: "probe-secret",
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/monitor/report", strings.NewReader(`{"cpu_percent":1}`))
	req.Header.Set("Authorization", "Bearer probe-secret")
	w := httptest.NewRecorder()
	a.handleMonitorReport(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var response struct {
		Data struct {
			ProbeIntervalSeconds int `json:"probe_interval_seconds"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.ProbeIntervalSeconds != 120 {
		t.Fatalf("probe interval = %d", response.Data.ProbeIntervalSeconds)
	}
}
