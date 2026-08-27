package sbctl

import (
	"testing"
	"time"
)

func TestNormalizedIntervalsDefaultsAndReconcileFloor(t *testing.T) {
	stats, reconcile := normalizedIntervals(nil)
	if stats != 10*time.Minute || reconcile != 60*time.Minute {
		t.Fatalf("defaults = %v/%v", stats, reconcile)
	}
	stats, reconcile = normalizedIntervals(func() (time.Duration, time.Duration) {
		return time.Hour, 10 * time.Minute
	})
	if stats != time.Hour || reconcile != time.Hour {
		t.Fatalf("reconcile floor = %v/%v", stats, reconcile)
	}
}
