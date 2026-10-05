package store

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"qingzhou/internal/sbver"
)

func visionCapabilityFixture(t *testing.T, hops int, options string) userMeteringFixture {
	t.Helper()
	f := newUserMeteringFixture(t, hops)
	if err := f.st.ConfigureTrafficMetering(true, false, false); err != nil {
		t.Fatal(err)
	}
	id, err := f.st.SaveSbTls(&SbTls{Name: "capability fixture", ServerJSON: `{"enabled":true}`, ClientJSON: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	for _, inbound := range f.inbounds {
		if _, err = f.st.db.Exec(`UPDATE sb_inbounds SET tls_id=?,options=? WHERE id=?`, id, options, inbound); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func recordFixedVisionCapabilities(t *testing.T, f userMeteringFixture) {
	t.Helper()
	info := sbver.Parse("sing-box version " + sbver.VisionFramingFixVersion + "\nTags: with_v2ray_api")
	for _, id := range f.servers {
		if err := f.st.SetNodeSingbox(id, info); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRelayVisionEnableRequiresEveryManagedNode(t *testing.T) {
	for _, badNode := range []int{0, 1, 2} {
		for _, bad := range []string{"unknown", "1.14.2", "1.14.3", "stale", "future", "error", "no-api"} {
			t.Run(fmt.Sprintf("node-%d/%s", badNode, bad), func(t *testing.T) {
				f := visionCapabilityFixture(t, 3, `{}`)
				// Cover the panel's own machine as a real source/intermediate/target.
				if _, err := f.st.db.Exec(`UPDATE sb_inbounds SET server_id=0 WHERE id=?`, f.inbounds[badNode]); err != nil {
					t.Fatal(err)
				}
				f.servers[badNode] = LocalNodeID
				recordFixedVisionCapabilities(t, f)
				switch bad {
				case "unknown":
					f.st.DeleteNodeSingbox(LocalNodeID)
				case "1.14.2", "1.14.3":
					f.st.SetNodeSingbox(LocalNodeID, sbver.Parse("sing-box version "+bad+"\nTags: with_v2ray_api"))
				case "stale":
					f.st.db.Exec(`UPDATE node_singbox SET checked_at=? WHERE server_id=0`, time.Now().Add(-16*time.Minute).Unix())
				case "future":
					f.st.db.Exec(`UPDATE node_singbox SET checked_at=? WHERE server_id=0`, time.Now().Add(time.Hour).Unix())
				case "error":
					f.st.SetNodeSingboxError(LocalNodeID, "stale running core")
				case "no-api":
					f.st.SetNodeSingbox(LocalNodeID, sbver.Parse("sing-box version "+sbver.VisionFramingFixVersion))
				}
				err := f.st.ConfigureTrafficMetering(true, true, true)
				if err == nil || !strings.Contains(err.Error(), "Vision") || !strings.Contains(err.Error(), LocalNodeName) {
					t.Fatalf("must reject unsupported %s: %v", bad, err)
				}
				if f.st.RelayUserMeteringEnabled() {
					t.Fatal("failed preflight saved P1 enabled")
				}
				value, _ := f.st.GetSetting("traffic_cumulative_metering")
				if value != "false" {
					t.Fatal("failed preflight partially committed another setting")
				}
				recordFixedVisionCapabilities(t, f)
				if err = f.st.ConfigureTrafficMetering(true, true, true); err != nil {
					t.Fatal(err)
				}
				ids, err := f.st.RelayUserVisionServerIDs()
				want := slices.Clone(f.servers)
				slices.Sort(want)
				if err != nil || !slices.Equal(ids, want) {
					t.Fatalf("Vision nodes %v want %v: %v", ids, want, err)
				}
			})
		}
	}
}

func TestRelayVisionGateLeavesPlainWSP0AndOptOutUnchanged(t *testing.T) {
	for _, options := range []string{`{"flow":"none"}`, `{"transport":{"type":"ws","path":"/fixture"}}`} {
		t.Run(options, func(t *testing.T) {
			f := visionCapabilityFixture(t, 2, options)
			if strings.Contains(options, `"ws"`) {
				// WS still does not require Vision, but independently requires the
				// reviewed transport buffering fix for P1.
				for _, id := range f.servers {
					if err := f.st.SetNodeSingbox(id, sbver.Parse("sing-box version "+sbver.TransportReadBufferFixVersion+"\nTags: with_v2ray_api")); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := f.st.ConfigureTrafficMetering(true, false, true); err != nil {
				t.Fatal(err)
			}
			ids, err := f.st.RelayUserVisionServerIDs()
			if err != nil || len(ids) != 0 {
				t.Fatalf("non-Vision nodes %v %v", ids, err)
			}
		})
	}
	f := visionCapabilityFixture(t, 2, `{}`)
	if err := f.st.ConfigureTrafficMetering(true, false, false); err != nil {
		t.Fatal("P0 must not require the Vision fix:", err)
	}
	ids, err := f.st.RelayUserVisionServerIDs()
	if err != nil || len(ids) != 0 {
		t.Fatalf("disabled P1 nodes %v %v", ids, err)
	}
	recordFixedVisionCapabilities(t, f)
	if err = f.st.ConfigureTrafficMetering(true, false, true); err != nil {
		t.Fatal(err)
	}
	f.st.SetNodeSingboxError(f.servers[0], "offline")
	if err = f.st.ConfigureTrafficMetering(true, false, false); err != nil {
		t.Fatal("opt-out must not require the Vision fix:", err)
	}
}

func TestRelayVisionDetectionMatchesListener(t *testing.T) {
	for _, tc := range []struct {
		name, options string
		tls           int64
		want          bool
	}{
		{"plain", `{"flow":"vision"}`, 0, false},
		{"tls", `{}`, 1, true},
		{"inline", `{"tls":{"enabled":true}}`, 0, true},
		{"empty-transport", `{"transport":{}}`, 1, true},
		{"ws", `{"transport":{"type":"ws"}}`, 1, false},
		{"none", `{"flow":"none"}`, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := relayInboundUsesVision(&SbInbound{Type: "vless", TlsID: tc.tls, Options: tc.options}); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestVisionRuntimeFailureCannotBeClearedByDiskProbe(t *testing.T) {
	f := visionCapabilityFixture(t, 3, `{}`)
	recordFixedVisionCapabilities(t, f)
	if err := f.st.ConfigureTrafficMetering(true, false, true); err != nil {
		t.Fatal(err)
	}
	info := sbver.Parse("sing-box version " + sbver.VisionFramingFixVersion + "\nTags: with_v2ray_api")
	for _, id := range f.servers {
		if err := f.st.SetNodeVisionRuntime(id, info); err != nil {
			t.Fatal(err)
		}
	}
	assertReady := func(want bool) {
		t.Helper()
		for _, id := range f.servers {
			ready, err := relayVisionAttributionReadyWith(f.st.db, id, time.Now().Unix())
			if err != nil || ready != want {
				t.Fatalf("node %d runtime ready=%v want=%v %v", id, ready, want, err)
			}
		}
	}
	assertReady(true)
	if err := f.st.SetNodeVisionRuntimeError(f.servers[1], "running core is stock 1.14.2"); err != nil {
		t.Fatal(err)
	}
	assertReady(false)
	recordFixedVisionCapabilities(t, f) // UI only sees a new disk binary.
	assertReady(false)
	if err := f.st.Migrate(); err != nil {
		t.Fatal(err)
	}
	assertReady(false) // restart/migration must not erase known runtime failure.
	if err := f.st.SetNodeVisionRuntime(f.servers[1], info); err != nil {
		t.Fatal(err)
	}
	assertReady(true)
	f.st.db.Exec(`UPDATE node_vision_runtime SET checked_at=? WHERE server_id=?`, time.Now().Add(-49*time.Hour).Unix(), f.servers[1])
	assertReady(false)
	if err := f.st.ConfigureTrafficMetering(true, false, false); err != nil {
		t.Fatal(err)
	}
	assertReady(true) // P0 compatibility reporting unchanged.
}

func TestRelayVisionDependenciesExcludeIndependentPlainPath(t *testing.T) {
	f := visionCapabilityFixture(t, 3, `{}`)
	// Final hop is plaintext; it depends on the preceding Vision processors
	// for the same path's readiness but does not itself need the framing fix.
	if _, err := f.st.db.Exec(`UPDATE sb_inbounds SET tls_id=0 WHERE id=?`, f.inbounds[2]); err != nil {
		t.Fatal(err)
	}
	recordFixedVisionCapabilities(t, f)
	plain := []int64{}
	for i := 0; i < 2; i++ {
		id, err := f.st.CreateServer(Server{Name: fmt.Sprintf("plain-%d", i), Host: fmt.Sprintf("192.0.2.%d", 100+i), Enabled: true})
		if err != nil {
			t.Fatal(err)
		}
		plain = append(plain, id)
	}
	landing, err := f.st.SaveSbInbound(&SbInbound{ServerID: plain[1], Type: "vless", Tag: "independent-plain-end", ListenPort: 2444, Options: `{}`, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.st.SaveSbInbound(&SbInbound{ServerID: plain[0], Type: "vless", Tag: "independent-plain-entry", ListenPort: 2444, Options: `{}`, Enabled: true, UpstreamInboundID: landing})
	if err != nil {
		t.Fatal(err)
	}
	if err = f.st.ConfigureTrafficMetering(true, false, true); err != nil {
		t.Fatal(err)
	}
	deps, err := f.st.RelayUserVisionDependencies()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range f.servers {
		if !slices.Equal(deps[id], f.servers[:2]) {
			t.Fatalf("dependencies[%d]=%v", id, deps[id])
		}
	}
	for _, id := range plain {
		if len(deps[id]) != 0 {
			t.Fatalf("plain path blocked by unrelated Vision path: %v", deps)
		}
		ready, err := relayVisionAttributionReadyWith(f.st.db, id, time.Now().Unix())
		if err != nil || !ready {
			t.Fatalf("plain node readiness %v %v", ready, err)
		}
	}
}

func TestRelayVisionLogicalRouteAlsoRequiresBothEndpoints(t *testing.T) {
	f := visionCapabilityFixture(t, 2, `{}`)
	if _, err := f.st.db.Exec(`UPDATE sb_inbounds SET upstream_inbound_id=0 WHERE id=?`, f.inbounds[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.db.Exec(`UPDATE nodes SET route_upstream_inbound_id=? WHERE inbound_tag=?`, f.inbounds[1], f.tags[0]); err != nil {
		t.Fatal(err)
	}
	if err := f.st.ConfigureTrafficMetering(true, false, true); err == nil || !strings.Contains(err.Error(), "Vision") {
		t.Fatalf("logical route bypassed capability gate: %v", err)
	}
	recordFixedVisionCapabilities(t, f)
	if err := f.st.ConfigureTrafficMetering(true, false, true); err != nil {
		t.Fatal(err)
	}
	ids, err := f.st.RelayUserVisionServerIDs()
	if err != nil || !slices.Equal(ids, f.servers) {
		t.Fatalf("logical Vision nodes %v %v", ids, err)
	}
}
