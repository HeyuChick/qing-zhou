package api

import (
	"testing"
	"time"

	"qingzhou/internal/store"
)

// A plan bucket for a real package shows the package's current name (renames
// propagate to holders), while the pool / welcome / admin-grant buckets keep
// their own snapshot name, and a plan whose package was deleted falls back to
// its snapshot.
func TestBuildPlanViews_LiveName(t *testing.T) {
	future := time.Now().Unix() + 86400
	buckets := []*store.Bucket{
		{ID: 1, Kind: "plan", PackageID: 5, Name: "旧名字", TrafficLimit: 10 << 30, ExpiryAt: future},          // renamed → live
		{ID: 2, Kind: "plan", PackageID: 9, Name: "已删套餐快照", TrafficLimit: 10 << 30, ExpiryAt: future},       // pkg gone → snapshot
		{ID: 3, Kind: "plan", PackageID: store.WelcomePackageID, Name: "注册赠送", TrafficLimit: 1 << 30, ExpiryAt: future}, // grant → snapshot
		{ID: 4, Kind: "pool", PackageID: 0, Name: "通用流量", TrafficLimit: 1 << 30, ExpiryAt: future},           // pool → snapshot
		{ID: 5, Kind: store.KindFree, PackageID: 0, Name: "免费流量"},                                            // excluded
	}
	names := map[int64]string{5: "新名字"}

	views := buildPlanViews(buckets, names)

	got := map[int64]string{}
	for _, v := range views {
		got[v.ID] = v.Name
	}
	if _, ok := got[5]; ok {
		t.Fatal("free bucket must be excluded from plan views")
	}
	want := map[int64]string{1: "新名字", 2: "已删套餐快照", 3: "注册赠送", 4: "通用流量"}
	for id, w := range want {
		if got[id] != w {
			t.Errorf("bucket %d: name = %q, want %q", id, got[id], w)
		}
	}
}
