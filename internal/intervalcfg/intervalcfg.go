// Package intervalcfg owns the live collection and node-maintenance intervals.
//
// The values live in settings so an administrator can change them without
// restarting the panel. Host environment variables remain the highest-priority
// override for operators who deliberately pin deployment configuration.
package intervalcfg

import (
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	SettingProbeSeconds     = "monitor_probe_interval_seconds"
	SettingStatsMinutes     = "singbox_stats_interval_minutes"
	SettingReconcileMinutes = "singbox_reconcile_interval_minutes"

	EnvProbeInterval     = "QZ_MONITOR_PROBE_INTERVAL"
	EnvStatsInterval     = "QZ_SINGBOX_STATS_INTERVAL"
	EnvReconcileInterval = "QZ_SINGBOX_RECONCILE_INTERVAL"

	DefaultProbeSeconds     int64 = 60
	DefaultStatsMinutes     int64 = 10
	DefaultReconcileMinutes int64 = 60

	MinProbeSeconds     int64 = 30
	MaxProbeSeconds     int64 = 3600
	MinStatsMinutes     int64 = 1
	MaxStatsMinutes     int64 = 60
	MinReconcileMinutes int64 = 10
	MaxReconcileMinutes int64 = 1440
)

var RuntimeSettingKeys = []string{
	SettingProbeSeconds,
	SettingStatsMinutes,
	SettingReconcileMinutes,
}

// Getter is the small part of store.Store needed by this package.
type Getter interface {
	GetSetting(key string) (string, error)
}

func clamp(n, min, max int64) int64 {
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

func settingInt(g Getter, key string, def, min, max int64) int64 {
	if g == nil {
		return def
	}
	raw, err := g.GetSetting(key)
	if err != nil {
		return def
	}
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return def
	}
	return clamp(n, min, max)
}

func envDuration(key string, min, max, step time.Duration) (time.Duration, bool) {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return 0, false
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, false
	}
	if d < min {
		d = min
	}
	// The settings API stores integer seconds/minutes. Round an environment
	// duration up to the same unit so the read-only value shown in the UI is the
	// exact cadence the runtime uses ("90s" becomes 2 minutes, not a displayed
	// 1 minute while the loop secretly waits 1.5).
	if rem := d % step; rem != 0 {
		d += step - rem
	}
	if d > max {
		d = max
	}
	return d, true
}

func Probe(g Getter) time.Duration {
	if d, ok := envDuration(EnvProbeInterval, time.Duration(MinProbeSeconds)*time.Second, time.Duration(MaxProbeSeconds)*time.Second, time.Second); ok {
		return d
	}
	return time.Duration(settingInt(g, SettingProbeSeconds, DefaultProbeSeconds, MinProbeSeconds, MaxProbeSeconds)) * time.Second
}

func Stats(g Getter) time.Duration {
	if d, ok := envDuration(EnvStatsInterval, time.Duration(MinStatsMinutes)*time.Minute, time.Duration(MaxStatsMinutes)*time.Minute, time.Minute); ok {
		return d
	}
	return time.Duration(settingInt(g, SettingStatsMinutes, DefaultStatsMinutes, MinStatsMinutes, MaxStatsMinutes)) * time.Minute
}

func Reconcile(g Getter) time.Duration {
	if d, ok := envDuration(EnvReconcileInterval, time.Duration(MinReconcileMinutes)*time.Minute, time.Duration(MaxReconcileMinutes)*time.Minute, time.Minute); ok {
		return d
	}
	return time.Duration(settingInt(g, SettingReconcileMinutes, DefaultReconcileMinutes, MinReconcileMinutes, MaxReconcileMinutes)) * time.Minute
}

// Controller returns a coherent pair. A full reconciliation faster than the
// usage pass only adds SSH churn, so treat the stats interval as its floor.
func Controller(g Getter) (stats, reconcile time.Duration) {
	stats = Stats(g)
	reconcile = Reconcile(g)
	if reconcile < stats {
		reconcile = stats
	}
	return
}

// OnlineWindow allows two missed reports plus scheduling/network jitter. It
// grows with an administrator-selected probe interval so a deliberately slow
// probe is not immediately painted offline by the old fixed two-minute rule.
func OnlineWindow(g Getter) time.Duration {
	w := 2*Probe(g) + 30*time.Second
	if w < 2*time.Minute {
		return 2 * time.Minute
	}
	return w
}

// UserOnlineWindow is how recently a user must have transferred traffic to
// count as "online". last_online_at is only bumped by the stats poll, so this
// must track Stats() — a fixed five-minute window with the default ten-minute
// poll painted every live user offline between ticks.
func UserOnlineWindow(g Getter) time.Duration {
	w := 2*Stats(g) + 30*time.Second
	if w < 2*time.Minute {
		return 2 * time.Minute
	}
	return w
}

// EnvSettingValue returns the effective numeric value exposed in the settings
// form when a host environment variable pins a runtime interval.
func EnvSettingValue(setting string) (string, bool) {
	var d time.Duration
	var ok bool
	var unit time.Duration
	switch setting {
	case SettingProbeSeconds:
		d, ok = envDuration(EnvProbeInterval, time.Duration(MinProbeSeconds)*time.Second, time.Duration(MaxProbeSeconds)*time.Second, time.Second)
		unit = time.Second
	case SettingStatsMinutes:
		d, ok = envDuration(EnvStatsInterval, time.Duration(MinStatsMinutes)*time.Minute, time.Duration(MaxStatsMinutes)*time.Minute, time.Minute)
		unit = time.Minute
	case SettingReconcileMinutes:
		d, ok = envDuration(EnvReconcileInterval, time.Duration(MinReconcileMinutes)*time.Minute, time.Duration(MaxReconcileMinutes)*time.Minute, time.Minute)
		unit = time.Minute
	default:
		return "", false
	}
	if !ok {
		return "", false
	}
	return strconv.FormatInt(int64(d/unit), 10), true
}
