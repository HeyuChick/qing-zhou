package store

import (
	"strings"
	"testing"
	"time"

	"qingzhou/internal/sbver"
)

func TestNodeSingboxRoundTrip(t *testing.T) {
	st := newRefundStore(t)
	info := sbver.Parse("sing-box version 1.13.18\n\nTags: with_gvisor,with_v2ray_api\n")
	if err := st.SetNodeSingbox(7, info); err != nil {
		t.Fatal(err)
	}
	all, err := st.NodeSingboxAll()
	if err != nil {
		t.Fatal(err)
	}
	got := all[7]
	if got == nil {
		t.Fatal("nothing recorded")
	}
	if got.Version != "1.13.18" || !got.HasV2RayAPI || got.Raw == "" {
		t.Errorf("got %+v", got)
	}
	if got.CheckedAt == 0 {
		t.Error("checked_at not set")
	}
	if got.Error != "" {
		t.Errorf("error should be empty on success, got %q", got.Error)
	}
}

// "The node is unreachable right now" and "the node has no sing-box" are
// different answers. Overwriting the last known version with a blank would
// present the second when only the first is true.
func TestNodeSingboxErrorKeepsLastKnownVersion(t *testing.T) {
	st := newRefundStore(t)
	if err := st.SetNodeSingbox(7, sbver.Parse("sing-box version 1.13.18\nTags: with_v2ray_api")); err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeSingboxError(7, "ssh: handshake failed"); err != nil {
		t.Fatal(err)
	}
	all, _ := st.NodeSingboxAll()
	got := all[7]
	if got.Version != "1.13.18" {
		t.Errorf("version = %q, want the last known 1.13.18", got.Version)
	}
	if !got.HasV2RayAPI {
		t.Error("capability flag was cleared by an unrelated probe failure")
	}
	if !strings.Contains(got.Error, "handshake") {
		t.Errorf("error = %q", got.Error)
	}

	// And a later success clears the error rather than leaving it to haunt the UI.
	if err := st.SetNodeSingbox(7, sbver.Parse("sing-box version 1.13.19\nTags: with_v2ray_api")); err != nil {
		t.Fatal(err)
	}
	all, _ = st.NodeSingboxAll()
	if all[7].Error != "" || all[7].Version != "1.13.19" {
		t.Errorf("after recovery: %+v", all[7])
	}
}

// A node that has never been probed yet must record the failure without a prior
// row existing (the UPSERT's insert branch).
func TestNodeSingboxErrorWithoutPriorRow(t *testing.T) {
	st := newRefundStore(t)
	if err := st.SetNodeSingboxError(42, "connection refused"); err != nil {
		t.Fatal(err)
	}
	all, _ := st.NodeSingboxAll()
	if all[42] == nil || all[42].Error == "" || all[42].Version != "" {
		t.Errorf("got %+v", all[42])
	}
}

// The version string comes off someone else's machine; it must not be able to
// push an unbounded blob into the panel's database.
func TestNodeSingboxBoundsUntrustedText(t *testing.T) {
	st := newRefundStore(t)
	huge := strings.Repeat("A", 10_000)
	if err := st.SetNodeSingbox(1, sbver.Info{Version: "1.13.18", Raw: huge}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeSingboxError(2, huge); err != nil {
		t.Fatal(err)
	}
	all, _ := st.NodeSingboxAll()
	if len(all[1].Raw) > maxRawLen {
		t.Errorf("raw kept %d bytes", len(all[1].Raw))
	}
	if len(all[2].Error) > maxRawLen {
		t.Errorf("error kept %d bytes", len(all[2].Error))
	}
}

// Deleting a server must take its observation with it, or the node list keeps
// showing a machine that no longer exists.
func TestDeleteServerDropsSingboxRow(t *testing.T) {
	st := newRefundStore(t)
	id, err := st.CreateServer(Server{Name: "n1", Host: "203.0.113.1", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeSingbox(id, sbver.Info{Version: "1.13.18"}); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteServer(id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	all, _ := st.NodeSingboxAll()
	if _, still := all[id]; still {
		t.Error("observation outlived the server it described")
	}
}

// The local machine is server_id 0 and has no servers row; it must coexist with
// real servers rather than collide with one.
func TestLocalNodeCoexistsWithServers(t *testing.T) {
	st := newRefundStore(t)
	if err := st.SetNodeSingbox(LocalNodeID, sbver.Info{Version: "1.12.25"}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeSingbox(1, sbver.Info{Version: "1.13.18"}); err != nil {
		t.Fatal(err)
	}
	all, _ := st.NodeSingboxAll()
	if all[LocalNodeID].Version != "1.12.25" || all[1].Version != "1.13.18" {
		t.Errorf("local=%+v remote=%+v", all[LocalNodeID], all[1])
	}
}

func TestNodeSingboxDerivesVisionMarkerWithoutTrustingFlag(t *testing.T) {
	st := newRefundStore(t)
	for id, info := range map[int64]sbver.Info{
		LocalNodeID: sbver.Parse("sing-box version " + sbver.VisionFramingFixVersion + "\nTags: with_v2ray_api"),
		1:           {Version: "1.14.2", HasVisionFramingFix: true},
		2:           {Version: "1.14.3", HasVisionFramingFix: true},
	} {
		if err := st.SetNodeSingbox(id, info); err != nil {
			t.Fatal(err)
		}
	}
	all, err := st.NodeSingboxAll()
	if err != nil {
		t.Fatal(err)
	}
	if !all[LocalNodeID].HasVisionFramingFix || all[1].HasVisionFramingFix || all[2].HasVisionFramingFix {
		t.Fatalf("exact persisted capability: %+v", all)
	}
	if err = st.SetNodeSingboxError(LocalNodeID, "unreachable"); err != nil {
		t.Fatal(err)
	}
	all, _ = st.NodeSingboxAll()
	if !all[LocalNodeID].HasVisionFramingFix || all[LocalNodeID].Error == "" {
		t.Fatal("failed probe must retain the historical marker alongside the explicit error")
	}
}

func TestNodeVisionRuntimeRequiresFreshExactActualProof(t *testing.T) {
	st := newRefundStore(t)
	fixed := sbver.Parse("sing-box version " + sbver.VisionFramingFixVersion + "\nTags: with_v2ray_api")
	if err := st.SetNodeSingbox(LocalNodeID, fixed); err != nil {
		t.Fatal(err)
	}
	ready := func(want bool) {
		t.Helper()
		got, err := nodeVisionRuntimeReadyWith(st.db, LocalNodeID, time.Now().Unix())
		if err != nil || got != want {
			t.Fatalf("runtime ready=%v want=%v %v", got, want, err)
		}
	}
	ready(false) // installed-only evidence cannot endorse the running process.
	for _, info := range []sbver.Info{
		{Version: "1.14.2", HasV2RayAPI: true, HasVisionFramingFix: true},
		{Version: "1.14.3", HasV2RayAPI: true, HasVisionFramingFix: true},
		{Version: sbver.VisionFramingFixVersion},
		{},
	} {
		if err := st.SetNodeVisionRuntime(LocalNodeID, info); err != nil {
			t.Fatal(err)
		}
		ready(false)
	}
	if err := st.SetNodeVisionRuntime(LocalNodeID, fixed); err != nil {
		t.Fatal(err)
	}
	ready(true)
	if _, err := st.db.Exec(`UPDATE node_vision_runtime SET checked_at=? WHERE server_id=0`, time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	ready(false)
	if err := st.SetNodeVisionRuntime(LocalNodeID, fixed); err != nil {
		t.Fatal(err)
	}
	ready(true)
	if err := st.DeleteNodeSingbox(LocalNodeID); err != nil {
		t.Fatal(err)
	}
	ready(false)
}
