package store

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"qingzhou/internal/sbver"
)

func recordFixedTransportCapabilities(t *testing.T, f userMeteringFixture) {
	t.Helper()
	info := sbver.Parse("sing-box version " + sbver.TransportReadBufferFixVersion + "\nTags: with_v2ray_api")
	for _, id := range f.servers {
		if err := f.st.SetNodeSingbox(id, info); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRelayTransportActivationChecksEveryProcessor(t *testing.T) {
	for _, transport := range []string{"ws", "httpupgrade"} {
		for _, badNode := range []int{0, 1, 2} {
			for _, bad := range []string{"old-vision", "stock", "future-version", "unknown", "stale", "future-probe", "error", "no-api"} {
				t.Run(fmt.Sprintf("%s/node-%d/%s", transport, badNode, bad), func(t *testing.T) {
					f := visionCapabilityFixture(t, 3, fmt.Sprintf(`{"transport":{"type":%q,"path":"/fixture"}}`, transport))
					if _, err := f.st.db.Exec(`UPDATE sb_inbounds SET server_id=0 WHERE id=?`, f.inbounds[badNode]); err != nil {
						t.Fatal(err)
					}
					f.servers[badNode] = LocalNodeID
					recordFixedTransportCapabilities(t, f)
					switch bad {
					case "old-vision":
						f.st.SetNodeSingbox(0, sbver.Parse("sing-box version "+sbver.VisionFramingFixVersion+"\nTags: with_v2ray_api"))
					case "stock":
						f.st.SetNodeSingbox(0, sbver.Parse("sing-box version 1.14.2\nTags: with_v2ray_api"))
					case "future-version":
						f.st.SetNodeSingbox(0, sbver.Parse("sing-box version 1.99.0\nTags: with_v2ray_api"))
					case "unknown":
						f.st.DeleteNodeSingbox(0)
					case "stale":
						f.st.db.Exec(`UPDATE node_singbox SET checked_at=? WHERE server_id=0`, time.Now().Add(-16*time.Minute).Unix())
					case "future-probe":
						f.st.db.Exec(`UPDATE node_singbox SET checked_at=? WHERE server_id=0`, time.Now().Add(time.Hour).Unix())
					case "error":
						f.st.SetNodeSingboxError(0, "fixture probe failed")
					case "no-api":
						f.st.SetNodeSingbox(0, sbver.Parse("sing-box version "+sbver.TransportReadBufferFixVersion))
					}
					err := f.st.ConfigureTrafficMetering(true, true, true)
					if err == nil || !strings.Contains(err.Error(), "WebSocket/HTTPUpgrade") || !strings.Contains(err.Error(), LocalNodeName) {
						t.Fatalf("accepted %s: %v", bad, err)
					}
					if f.st.RelayUserMeteringEnabled() {
						t.Fatal("failed preflight committed P1")
					}
					if v, _ := f.st.GetSetting("traffic_cumulative_metering"); v != "false" {
						t.Fatal("failed preflight committed cumulative")
					}
					recordFixedTransportCapabilities(t, f)
					if err := f.st.ConfigureTrafficMetering(true, false, true); err != nil {
						t.Fatal(err)
					}
					required, deps, err := f.st.RelayUserCoreTopology()
					if err != nil {
						t.Fatal(err)
					}
					for _, id := range f.servers {
						if !required[id].TransportReadBuffer || required[id].VisionFraming || len(deps[id]) != 3 {
							t.Fatalf("bad core requirements/dependencies: %+v %+v", required, deps)
						}
					}
				})
			}
		}
	}
}

func TestRelayTransportDetectionAndMixedCoreRequirements(t *testing.T) {
	for _, protocol := range []string{"vless", "vmess", "trojan", "anytls", "hysteria2", "mixed"} {
		for _, transport := range []string{"ws", "httpupgrade", "grpc", "http", "quic", ""} {
			ib := &SbInbound{Type: protocol, Options: fmt.Sprintf(`{"transport":{"type":%q}}`, transport)}
			want := (protocol == "vless" || protocol == "vmess" || protocol == "trojan") && (transport == "ws" || transport == "httpupgrade")
			if got := relayInboundUsesBufferedTransport(ib); got != want {
				t.Fatalf("detector %s/%s=%v want=%v", protocol, transport, got, want)
			}
		}
	}
	f := visionCapabilityFixture(t, 3, `{}`)
	if _, err := f.st.db.Exec(`UPDATE sb_inbounds SET options='{"transport":{"type":"ws"}}' WHERE id=?`, f.inbounds[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.db.Exec(`UPDATE sb_inbounds SET tls_id=0 WHERE id=?`, f.inbounds[2]); err != nil {
		t.Fatal(err)
	}
	recordFixedTransportCapabilities(t, f)
	preview, err := f.st.PreviewRelayUserCoreRequirements()
	if err != nil {
		t.Fatal(err)
	}
	if len(preview) != 2 || !preview[f.servers[0]].VisionFraming || !preview[f.servers[0]].TransportReadBuffer || preview[f.servers[1]].VisionFraming || !preview[f.servers[1]].TransportReadBuffer {
		t.Fatalf("union requirements=%+v", preview)
	}
	if active, _, err := f.st.RelayUserCoreTopology(); err != nil || len(active) != 0 {
		t.Fatalf("disabled P1 has active requirements: %+v %v", active, err)
	}
	if err := f.st.ConfigureTrafficMetering(true, false, true); err != nil {
		t.Fatal(err)
	}
	_, deps, err := f.st.RelayUserCoreTopology()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range f.servers {
		if !slices.Equal(deps[id], f.servers[:2]) {
			t.Fatalf("wrong connected dependencies: %v", deps)
		}
	}
	// Clearing the physical edge and adding a logical route retains both sides.
	if _, err := f.st.db.Exec(`UPDATE sb_inbounds SET upstream_inbound_id=0 WHERE id=?`, f.inbounds[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.db.Exec(`UPDATE nodes SET route_upstream_inbound_id=? WHERE inbound_tag=?`, f.inbounds[1], f.tags[0]); err != nil {
		t.Fatal(err)
	}
	logical, err := f.st.PreviewRelayUserCoreRequirements()
	if err != nil || logical[f.servers[0]] != preview[f.servers[0]] {
		t.Fatalf("logical requirements=%v %v", logical, err)
	}
}

func TestRelayTransportRuntimeProofAndAttributionSourceBoundary(t *testing.T) {
	f := visionCapabilityFixture(t, 3, `{"transport":{"type":"httpupgrade"}}`)
	recordFixedTransportCapabilities(t, f)
	if err := f.st.ConfigureTrafficMetering(true, false, true); err != nil {
		t.Fatal(err)
	}
	fixed := sbver.Parse("sing-box version " + sbver.TransportReadBufferFixVersion + "\nTags: with_v2ray_api")
	assertReady := func(want bool) {
		t.Helper()
		for _, id := range f.servers {
			ready, err := relayTransportAttributionReadyWith(f.st.db, id, time.Now().Unix())
			if err != nil || ready != want {
				t.Fatalf("runtime %d ready=%v want=%v: %v", id, ready, want, err)
			}
		}
	}
	assertReady(false) // Installed observations alone cannot authorize readiness.
	for _, id := range f.servers {
		if err := f.st.SetNodeVisionRuntime(id, fixed); err != nil {
			t.Fatal(err)
		}
	}
	assertReady(true)
	for _, bad := range []string{"old-vision", "stale", "future", "error"} {
		switch bad {
		case "old-vision":
			f.st.SetNodeVisionRuntime(f.servers[1], sbver.Parse("sing-box version "+sbver.VisionFramingFixVersion+"\nTags: with_v2ray_api"))
		case "stale":
			f.st.db.Exec(`UPDATE node_vision_runtime SET checked_at=? WHERE server_id=?`, time.Now().Add(-49*time.Hour).Unix(), f.servers[1])
		case "future":
			f.st.db.Exec(`UPDATE node_vision_runtime SET checked_at=? WHERE server_id=?`, time.Now().Add(time.Hour).Unix(), f.servers[1])
		case "error":
			f.st.SetNodeVisionRuntimeError(f.servers[1], "process inactive")
		}
		assertReady(false)
		recordFixedTransportCapabilities(t, f)
		assertReady(false)
		_, _, reasons, err := serviceTrafficAttributionReady(f.st.db, f.servers[1])
		if err != nil || !slices.Contains(reasons, "transport_core_unverified") || slices.Contains(reasons, "vision_core_unverified") {
			t.Fatalf("wrong readiness reason for %s: %v %v", bad, reasons, err)
		}
		if err := f.st.SetNodeVisionRuntime(f.servers[1], fixed); err != nil {
			t.Fatal(err)
		}
		assertReady(true)
	}
	if err := f.st.ConfigureTrafficMetering(true, false, false); err != nil {
		t.Fatal(err)
	}
	assertReady(true)
}

func TestRelayCoreRequirementsDoNotTrustUnrelatedCapabilityFlags(t *testing.T) {
	needs := RelayCoreRequirements{VisionFraming: true, TransportReadBuffer: true}
	spoofed := sbver.Info{Version: sbver.VisionFramingFixVersion, HasV2RayAPI: true, HasVisionFramingFix: true, HasTransportReadBufferFix: true}
	if needs.SupportedBy(spoofed) {
		t.Fatal("boolean transport flag substituted for exact reviewed version marker")
	}
	if !needs.SupportedBy(sbver.Parse("sing-box version " + sbver.TransportReadBufferFixVersion + "\nTags: with_v2ray_api")) {
		t.Fatal("combined reviewed core rejected")
	}
}
