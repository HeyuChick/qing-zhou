package intervalcfg

import (
	"testing"
	"time"
)

type fakeSettings map[string]string

func (f fakeSettings) GetSetting(key string) (string, error) { return f[key], nil }

func TestDefaults(t *testing.T) {
	t.Setenv(EnvProbeInterval, "")
	t.Setenv(EnvStatsInterval, "")
	t.Setenv(EnvReconcileInterval, "")
	var f fakeSettings
	if got := Probe(f); got != 60*time.Second {
		t.Fatalf("probe = %v", got)
	}
	stats, reconcile := Controller(f)
	if stats != 10*time.Minute || reconcile != 60*time.Minute {
		t.Fatalf("controller = %v/%v", stats, reconcile)
	}
}

func TestEnvironmentOverridesAndClamps(t *testing.T) {
	t.Setenv(EnvProbeInterval, "5s")
	t.Setenv(EnvStatsInterval, "30s")
	t.Setenv(EnvReconcileInterval, "2h")
	f := fakeSettings{
		SettingProbeSeconds:     "300",
		SettingStatsMinutes:     "20",
		SettingReconcileMinutes: "30",
	}
	if got := Probe(f); got != 30*time.Second {
		t.Fatalf("probe clamp = %v", got)
	}
	stats, reconcile := Controller(f)
	if stats != time.Minute || reconcile != 2*time.Hour {
		t.Fatalf("controller env = %v/%v", stats, reconcile)
	}
	if got, ok := EnvSettingValue(SettingStatsMinutes); !ok || got != "1" {
		t.Fatalf("stats form value = %q, %v", got, ok)
	}
}

func TestEnvironmentDurationMatchesIntegerFormValue(t *testing.T) {
	t.Setenv(EnvStatsInterval, "90s")
	if got := Stats(nil); got != 2*time.Minute {
		t.Fatalf("stats = %v", got)
	}
	if got, ok := EnvSettingValue(SettingStatsMinutes); !ok || got != "2" {
		t.Fatalf("form value = %q, %v", got, ok)
	}
}

func TestReconcileCannotRunFasterThanStats(t *testing.T) {
	t.Setenv(EnvStatsInterval, "")
	t.Setenv(EnvReconcileInterval, "")
	f := fakeSettings{SettingStatsMinutes: "60", SettingReconcileMinutes: "10"}
	stats, reconcile := Controller(f)
	if stats != time.Hour || reconcile != time.Hour {
		t.Fatalf("controller = %v/%v", stats, reconcile)
	}
}

func TestOnlineWindowTracksProbeInterval(t *testing.T) {
	t.Setenv(EnvProbeInterval, "")
	f := fakeSettings{SettingProbeSeconds: "300"}
	if got := OnlineWindow(f); got != 10*time.Minute+30*time.Second {
		t.Fatalf("online window = %v", got)
	}
}
