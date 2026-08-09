package store

import (
	"strings"
	"testing"

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
