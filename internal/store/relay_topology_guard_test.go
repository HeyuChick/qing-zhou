package store

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func requireTopologyRejection(t *testing.T, err error, fragment string) {
	t.Helper()
	if !errors.Is(err, ErrRelayTopology) || !strings.Contains(err.Error(), fragment) {
		t.Fatalf("want topology rejection containing %q, got %v", fragment, err)
	}
}

func TestRelayTopologySaveInboundRollback(t *testing.T) {
	for _, mode := range []string{"p0", "p1"} {
		for _, change := range []string{"disable-target", "missing-target", "same-machine", "cycle", "unsupported-target", "invalid-render", "source-protocol"} {
			t.Run(mode+"/"+change, func(t *testing.T) {
				f := newUserMeteringFixture(t, 3)
				if mode == "p0" {
					if err := f.st.ConfigureTrafficMetering(true, false, false); err != nil {
						t.Fatal(err)
					}
					if change == "source-protocol" {
						return
					} // P0 does not meter entry identities.
				}
				index := 2
				if change == "missing-target" || change == "source-protocol" {
					index = 0
				}
				before, err := f.st.GetSbInbound(f.inbounds[index])
				if err != nil {
					t.Fatal(err)
				}
				candidate := *before
				want := ""
				switch change {
				case "disable-target":
					candidate.Enabled = false
					want = "已禁用"
				case "missing-target":
					candidate.UpstreamInboundID = 987654321
					want = "不存在"
				case "same-machine":
					candidate.ServerID = f.servers[1]
					want = "同一服务器"
				case "cycle":
					candidate.UpstreamInboundID = f.inbounds[0]
					want = "环路"
				case "unsupported-target":
					candidate.Type = "mixed"
					want = "不支持"
				case "invalid-render":
					candidate.Type = "tuic"
					candidate.TlsID = 0
					candidate.Options = `{}`
					want = "requires enabled server TLS"
				case "source-protocol":
					candidate.Type = "socks"
					want = "不支持"
				}
				// Include an unrelated field and cascaded self-built node rename.
				candidate.Tag += "-rejected"
				_, err = f.st.SaveSbInbound(&candidate)
				requireTopologyRejection(t, err, want)
				after, err := f.st.GetSbInbound(before.ID)
				if err != nil || !reflect.DeepEqual(before, after) {
					t.Fatalf("partial inbound save: before=%+v after=%+v err=%v", before, after, err)
				}
				if index == 0 {
					nodes, err := f.st.ListNodes()
					if err != nil {
						t.Fatal(err)
					}
					for _, node := range nodes {
						if node.InboundTag == candidate.Tag {
							t.Fatal("rejected rename changed linked node")
						}
					}
				}
			})
		}
	}
}

func TestRelayTopologySaveInsertAndLogicalRouteRollback(t *testing.T) {
	f := newUserMeteringFixture(t, 3)
	var before int
	if err := f.st.db.QueryRow(`SELECT COUNT(*) FROM sb_inbounds`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	_, err := f.st.SaveSbInbound(&SbInbound{ServerID: f.servers[0], Type: "vless", Tag: "rejected-new", ListenPort: 2444, Enabled: true, UpstreamInboundID: 99999})
	requireTopologyRejection(t, err, "不存在")
	var after int
	if err := f.st.db.QueryRow(`SELECT COUNT(*) FROM sb_inbounds`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("invalid insert committed")
	}
	gid, err := f.st.CreateGroup(NodeGroup{Name: "atomic-groups"})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []string{f.tags[2], "missing-entry"} {
		_, err := f.st.CreateNode(Node{Type: "self_built", Name: "invalid-logical", InboundTag: entry, RouteUpstreamInboundID: f.inbounds[0], Enabled: true, GroupIDs: []int64{gid}})
		requireTopologyRejection(t, err, "")
	}
	var leftovers int
	if err := f.st.db.QueryRow(`SELECT COUNT(*) FROM node_group_members WHERE group_id=?`, gid).Scan(&leftovers); err != nil {
		t.Fatal(err)
	}
	if leftovers != 0 {
		t.Fatal("rejected logical insert left memberships")
	}
	nid, err := f.st.CreateNode(Node{Type: "self_built", Name: "valid-logical", InboundTag: f.tags[0], RouteUpstreamInboundID: f.inbounds[2], Enabled: true, GroupIDs: []int64{gid}})
	if err != nil {
		t.Fatal(err)
	}
	beforeNode, err := f.st.GetNode(nid)
	if err != nil {
		t.Fatal(err)
	}
	candidate := *beforeNode
	candidate.InboundTag = f.tags[2]
	candidate.RouteUpstreamInboundID = f.inbounds[0]
	candidate.GroupIDs = []int64{}
	requireTopologyRejection(t, f.st.UpdateNode(candidate), "环路")
	afterNode, err := f.st.GetNode(nid)
	if err != nil || !reflect.DeepEqual(beforeNode, afterNode) {
		t.Fatalf("rejected logical update changed row: %v", err)
	}
	groups, err := f.st.NodeGroupIDs(nid)
	if err != nil || !reflect.DeepEqual(groups, []int64{gid}) {
		t.Fatalf("rejected logical update changed groups: %v %v", groups, err)
	}

	// Leave only the logical edge and ensure disabling/deleting its target is
	// protected independently of the physical edge query.
	entry, _ := f.st.GetSbInbound(f.inbounds[0])
	entry.UpstreamInboundID = 0
	if _, err := f.st.SaveSbInbound(entry); err != nil {
		t.Fatal(err)
	}
	middle, _ := f.st.GetSbInbound(f.inbounds[1])
	middle.UpstreamInboundID = 0
	if _, err := f.st.SaveSbInbound(middle); err != nil {
		t.Fatal(err)
	}
	landing, _ := f.st.GetSbInbound(f.inbounds[2])
	landing.Enabled = false
	_, err = f.st.SaveSbInbound(landing)
	requireTopologyRejection(t, err, "已禁用")
	_, err = f.st.DeleteSbInbound(landing.ID)
	requireTopologyRejection(t, err, "仍被")
	if err := f.st.DeleteNode(nid); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.SaveSbInbound(landing); err != nil {
		t.Fatal(err)
	}
}

func TestRelayTopologySaveServerAndTLSRollback(t *testing.T) {
	f := newUserMeteringFixture(t, 3)
	for _, id := range f.servers {
		before, err := f.st.GetServer(id)
		if err != nil {
			t.Fatal(err)
		}
		candidate := *before
		candidate.Enabled = false
		candidate.Host = "192.0.2.99"
		candidate.Name = "rejected-server"
		requireTopologyRejection(t, f.st.UpdateServer(candidate), "已禁用")
		after, err := f.st.GetServer(id)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("partial server save: %v", err)
		}
	}
	tlsID, err := f.st.SaveSbTls(&SbTls{Name: "static-tls", Mode: "tls", ServerJSON: `{"enabled":true}`, ClientJSON: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	landing, _ := f.st.GetSbInbound(f.inbounds[2])
	landing.Type = "tuic"
	landing.TlsID = tlsID
	if _, err := f.st.SaveSbInbound(landing); err != nil {
		t.Fatal(err)
	}
	before, err := f.st.GetSbTls(tlsID)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []string{"server", "client", "quic-reality", "cert"} {
		candidate := *before
		candidate.Name = "rejected-tls"
		switch change {
		case "server":
			candidate.ServerJSON = `{"enabled":false}`
		case "client":
			candidate.ClientJSON = `{"enabled":false}`
		case "quic-reality":
			candidate.ServerJSON = `{"enabled":true,"reality":{"enabled":true}}`
		case "cert":
			candidate.CertID = 99999
		}
		_, err := f.st.SaveSbTls(&candidate)
		requireTopologyRejection(t, err, "")
		after, err := f.st.GetSbTls(tlsID)
		if err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("partial TLS save: %v", err)
		}
	}
	if err := f.st.DeleteSbTls(tlsID); !errors.Is(err, ErrInUse) {
		t.Fatalf("delete live TLS: %v", err)
	}
}

func TestRelayTopologySaveDeleteAndOrderedBatch(t *testing.T) {
	f := newUserMeteringFixture(t, 3)
	_, err := f.st.DeleteSbInbound(f.inbounds[2])
	requireTopologyRejection(t, err, "仍被")
	middle, _ := f.st.GetSbInbound(f.inbounds[1])
	if middle.UpstreamInboundID != f.inbounds[2] || middle.UpstreamBroken {
		t.Fatal("delete silently unchained route")
	}
	// The current batch UI uses the same per-inbound save as single toggles.
	// Entry-to-exit disable and exit-to-entry enable are each safe at every step.
	for _, enabled := range []bool{false, true} {
		for j := 0; j < 3; j++ {
			i := j
			if enabled {
				i = 2 - j
			}
			ib, _ := f.st.GetSbInbound(f.inbounds[i])
			ib.Enabled = enabled
			if _, err := f.st.SaveSbInbound(ib); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := f.st.DeleteSbInbound(f.inbounds[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.DeleteSbInbound(f.inbounds[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.DeleteSbInbound(f.inbounds[2]); err != nil {
		t.Fatal(err)
	}
}

func TestRelayTopologySaveRepairsAndOptOut(t *testing.T) {
	for _, change := range []string{"target-disabled", "server-disabled", "missing-target", "cycle"} {
		t.Run(change, func(t *testing.T) {
			f := newUserMeteringFixture(t, 3)
			// Simulate a pre-upgrade invalid database. Validate the candidate, never
			// the old snapshot, so fixing the actual error can still commit.
			switch change {
			case "target-disabled":
				_, err := f.st.db.Exec(`UPDATE sb_inbounds SET enabled=0 WHERE id=?`, f.inbounds[2])
				if err != nil {
					t.Fatal(err)
				}
				ib, _ := f.st.GetSbInbound(f.inbounds[2])
				ib.Enabled = true
				if _, err = f.st.SaveSbInbound(ib); err != nil {
					t.Fatal(err)
				}
			case "server-disabled":
				_, err := f.st.db.Exec(`UPDATE servers SET enabled=0 WHERE id=?`, f.servers[2])
				if err != nil {
					t.Fatal(err)
				}
				sv, _ := f.st.GetServer(f.servers[2])
				sv.Enabled = true
				if err = f.st.UpdateServer(*sv); err != nil {
					t.Fatal(err)
				}
			case "missing-target":
				_, err := f.st.db.Exec(`UPDATE sb_inbounds SET upstream_inbound_id=99999 WHERE id=?`, f.inbounds[0])
				if err != nil {
					t.Fatal(err)
				}
				ib, _ := f.st.GetSbInbound(f.inbounds[0])
				ib.UpstreamInboundID = f.inbounds[1]
				if _, err = f.st.SaveSbInbound(ib); err != nil {
					t.Fatal(err)
				}
			case "cycle":
				_, err := f.st.db.Exec(`UPDATE sb_inbounds SET upstream_inbound_id=? WHERE id=?`, f.inbounds[0], f.inbounds[2])
				if err != nil {
					t.Fatal(err)
				}
				ib, _ := f.st.GetSbInbound(f.inbounds[2])
				ib.UpstreamInboundID = 0
				if _, err = f.st.SaveSbInbound(ib); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	f := newUserMeteringFixture(t, 2)
	if err := f.st.ConfigureTrafficMetering(false, false, false); err != nil {
		t.Fatal(err)
	}
	ib, _ := f.st.GetSbInbound(f.inbounds[1])
	ib.Enabled = false
	if _, err := f.st.SaveSbInbound(ib); err != nil {
		t.Fatal("opt-out behavior changed:", err)
	}
	if _, err := f.st.DeleteSbInbound(ib.ID); err != nil {
		t.Fatal(err)
	}
	entry, _ := f.st.GetSbInbound(f.inbounds[0])
	if entry.UpstreamInboundID != 0 || !entry.UpstreamBroken {
		t.Fatal("opt-out legacy deletion changed")
	}
}

func TestRelayTopologySaveStaticOnlyAndNormalMigration(t *testing.T) {
	f := visionCapabilityFixture(t, 3, `{}`)
	recordFixedVisionCapabilities(t, f)
	if err := f.st.ConfigureTrafficMetering(true, false, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.db.Exec(`UPDATE node_singbox SET checked_at=?`, time.Now().Add(-time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	// No fresh runtime probe is needed to edit or move an otherwise valid graph.
	id, err := f.st.CreateServer(Server{Name: "migration", Host: "192.0.2.44", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ib, _ := f.st.GetSbInbound(f.inbounds[2])
	ib.ServerID = id
	ib.Tag = "moved-landing"
	if _, err := f.st.SaveSbInbound(ib); err != nil {
		t.Fatal("static save depended on stale capability:", err)
	}
	sv, _ := f.st.GetServer(id)
	sv.Host = "192.0.2.45"
	if err := f.st.UpdateServer(*sv); err != nil {
		t.Fatal(err)
	}
}

func TestRelayTopologySaveConcurrentCycle(t *testing.T) {
	f := newUserMeteringFixture(t, 2)
	// Begin with disconnected valid listeners. Each proposed edge is valid
	// alone, but exactly one can commit because the second would close a cycle.
	ib, _ := f.st.GetSbInbound(f.inbounds[0])
	ib.UpstreamInboundID = 0
	if _, err := f.st.SaveSbInbound(ib); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		candidate, _ := f.st.GetSbInbound(f.inbounds[i])
		candidate.UpstreamInboundID = f.inbounds[1-i]
		wg.Add(1)
		go func(ib *SbInbound) { defer wg.Done(); <-start; _, err := f.st.SaveSbInbound(ib); results <- err }(candidate)
	}
	close(start)
	wg.Wait()
	close(results)
	accepted, rejected := 0, 0
	for err := range results {
		if err == nil {
			accepted++
		} else if errors.Is(err, ErrRelayTopology) {
			rejected++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatal(fmt.Sprintf("concurrent candidates: accepted=%d rejected=%d", accepted, rejected))
	}
	if err := f.st.PrepareRelayMetering(); err != nil {
		t.Fatal("concurrent save left invalid graph:", err)
	}
}

func TestRelayTopologySaveProvisionRepairTLS(t *testing.T) {
	f := newUserMeteringFixture(t, 2)
	if _, err := f.st.db.Exec(`UPDATE sb_inbounds SET type='tuic',tls_id=0 WHERE id=?`, f.inbounds[1]); err != nil {
		t.Fatal(err)
	}
	// The new, as-yet-unreferenced resource is part of repairing an old graph.
	tlsID, err := f.st.SaveSbTls(&SbTls{Name: "repair-tls", ServerJSON: `{"enabled":true}`, ClientJSON: `{}`})
	if err != nil {
		t.Fatal("could not provision TLS to repair existing graph:", err)
	}
	ib, err := f.st.GetSbInbound(f.inbounds[1])
	if err != nil {
		t.Fatal(err)
	}
	ib.TlsID = tlsID
	if _, err := f.st.SaveSbInbound(ib); err != nil {
		t.Fatal(err)
	}
}

func TestRelayTopologySaveCannotTurnLogicalEntryIntoMixed(t *testing.T) {
	f := newUserMeteringFixture(t, 2)
	if _, err := f.st.CreateNode(Node{Type: "self_built", Name: "logical", InboundTag: f.tags[0], RouteUpstreamInboundID: f.inbounds[1], Enabled: true}); err != nil {
		t.Fatal(err)
	}
	ib, err := f.st.GetSbInbound(f.inbounds[0])
	if err != nil {
		t.Fatal(err)
	}
	ib.Type = "mixed"
	_, err = f.st.SaveSbInbound(ib)
	requireTopologyRejection(t, err, "Mixed")
}

func TestRelayTopologySaveConcurrentServerDisable(t *testing.T) {
	f := newUserMeteringFixture(t, 2)
	entry, _ := f.st.GetSbInbound(f.inbounds[0])
	entry.UpstreamInboundID = 0
	if _, err := f.st.SaveSbInbound(entry); err != nil {
		t.Fatal(err)
	}
	entry.UpstreamInboundID = f.inbounds[1]
	server, _ := f.st.GetServer(f.servers[1])
	server.Enabled = false
	start := make(chan struct{})
	results := make(chan error, 2)
	go func() { <-start; _, err := f.st.SaveSbInbound(entry); results <- err }()
	go func() { <-start; results <- f.st.UpdateServer(*server) }()
	close(start)
	accepted, rejected := 0, 0
	for i := 0; i < 2; i++ {
		err := <-results
		if err == nil {
			accepted++
		} else if errors.Is(err, ErrRelayTopology) {
			rejected++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 1 || rejected != 1 {
		t.Fatalf("concurrent disable/edge: accepted=%d rejected=%d", accepted, rejected)
	}
	if err := f.st.PrepareRelayMetering(); err != nil {
		t.Fatal("concurrent server edit left invalid graph:", err)
	}
}

func TestRelayTopologySaveDeleteServerRetainsObservationCleanup(t *testing.T) {
	st := newRefundStore(t)
	id, err := st.CreateServer(Server{Name: "unused", Host: "192.0.2.99", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeSingboxError(id, "fixture-only"); err != nil {
		t.Fatal(err)
	}
	if err := st.SetNodeVisionRuntimeError(id, "fixture-only"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteServer(id); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"servers", "node_singbox", "node_vision_runtime"} {
		key := "server_id"
		if table == "servers" {
			key = "id"
		}
		var count int
		if err := st.db.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE `+key+`=?`, id).Scan(&count); err != nil || count != 0 {
			t.Fatalf("deletion left %s observations: %d %v", table, count, err)
		}
	}
}

func TestRelayTopologyPreflightCandidatesAreReadOnly(t *testing.T) {
	f := newUserMeteringFixture(t, 2)
	if err := f.st.ConfigureTrafficMetering(false, false, false); err != nil {
		t.Fatal(err)
	}
	ib, err := f.st.GetSbInbound(f.inbounds[1])
	if err != nil {
		t.Fatal(err)
	}
	ib.Enabled = false
	if _, err := f.st.SaveSbInbound(ib); err != nil {
		t.Fatal(err)
	}
	tx, err := f.st.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var before, after int64
	if err := tx.QueryRow(`SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	requireTopologyRejection(t, f.st.validateRelayTopologyForSettings(tx, true, false), "已禁用")
	if err := f.st.validateRelayTopologyForSettings(tx, false, false); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(`SELECT total_changes()`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Fatalf("static preflight wrote SQL state: before=%d after=%d", before, after)
	}
	var stored string
	if err := tx.QueryRow(`SELECT value FROM settings WHERE key=?`, RelayMeteringSetting).Scan(&stored); err != nil || stored != "false" {
		t.Fatalf("static preflight changed stored switch: %s %v", stored, err)
	}
}

func TestRelayTopologyP0ActivationAndPreflightRejectLegacyGraph(t *testing.T) {
	f := newUserMeteringFixture(t, 2)
	if err := f.st.ConfigureTrafficMetering(false, false, false); err != nil {
		t.Fatal(err)
	}
	ib, err := f.st.GetSbInbound(f.inbounds[1])
	if err != nil {
		t.Fatal(err)
	}
	ib.Enabled = false
	if _, err := f.st.SaveSbInbound(ib); err != nil {
		t.Fatal(err)
	}
	before, err := f.st.RelayMeteringProgress()
	if err != nil {
		t.Fatal(err)
	}
	requireTopologyRejection(t, f.st.PreflightTrafficMetering(true, false, false), "已禁用")
	requireTopologyRejection(t, f.st.ConfigureTrafficMetering(true, false, false), "已禁用")
	for _, key := range []string{RelayMeteringSetting, RelayUserMeteringSetting, "traffic_cumulative_metering"} {
		value, err := f.st.GetSetting(key)
		if err != nil || value != "false" {
			t.Fatalf("rejected P0 activation/preflight changed %s=%q: %v", key, value, err)
		}
	}
	after, err := f.st.RelayMeteringProgress()
	if err != nil || before != after {
		t.Fatalf("rejected P0 activation/preflight changed identities/readiness: %v", err)
	}
}
