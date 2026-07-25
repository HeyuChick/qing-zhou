package api

import (
	"testing"

	"qingzhou/internal/store"
)

// The estimated LATEST activation chains from the head's expiry through each
// queued份's duration, in id order.
func TestQueueActivations_Chain(t *testing.T) {
	now := int64(1_000_000)
	e := now + 30*86400 // head expires in 30d
	buckets := []*store.Bucket{
		{ID: 1, Kind: "plan", PackageID: 1, Status: "active", ExpiryAt: e, TrafficLimit: 100, UsedUp: 10},
		{ID: 2, Kind: "plan", PackageID: 1, Status: "queued", DurationDays: 30},
		{ID: 3, Kind: "plan", PackageID: 1, Status: "queued", DurationDays: 7},
	}
	acts := queueActivations(buckets, now)
	if acts[2] != e {
		t.Errorf("q1 activate_by = %d, want head expiry %d", acts[2], e)
	}
	if want := e + 30*86400; acts[3] != want {
		t.Errorf("q2 activate_by = %d, want %d (head expiry + q1 duration)", acts[3], want)
	}
}

// An unlimited-duration head (no expiry) makes queued activation time unknown —
// only exhaustion advances it.
func TestQueueActivations_UnlimitedHead(t *testing.T) {
	now := int64(1_000_000)
	buckets := []*store.Bucket{
		{ID: 1, Kind: "plan", PackageID: 1, Status: "active", ExpiryAt: 0, TrafficLimit: 100, UsedUp: 10},
		{ID: 2, Kind: "plan", PackageID: 1, Status: "queued", DurationDays: 30},
	}
	if acts := queueActivations(buckets, now); acts[2] != 0 {
		t.Errorf("unlimited-head queued activate_by = %d, want 0 (unknown)", acts[2])
	}
}

// With the head already ended (the ~2min promotion gap), the oldest queued份 is
// treated as due now.
func TestQueueActivations_HeadEnded(t *testing.T) {
	now := int64(1_000_000)
	buckets := []*store.Bucket{
		// head expired an hour ago → not a usable head
		{ID: 1, Kind: "plan", PackageID: 1, Status: "active", ExpiryAt: now - 3600, TrafficLimit: 100, UsedUp: 10},
		{ID: 2, Kind: "plan", PackageID: 1, Status: "queued", DurationDays: 30},
	}
	if acts := queueActivations(buckets, now); acts[2] != now {
		t.Errorf("head-ended queued activate_by = %d, want now %d", acts[2], now)
	}
}
