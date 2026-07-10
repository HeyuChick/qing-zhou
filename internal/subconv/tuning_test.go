package subconv

import (
	"strings"
	"testing"

	"qingzhou/internal/singbox"
)

// TestTuningRoundTrip checks that TCP Fast Open / MPTCP / Multiplex+Brutal set on
// a node survive the link round-trip (LinkParams -> share link -> parse -> client
// config) and land as the right options in both the Clash and sing-box output.
func TestTuningRoundTrip(t *testing.T) {
	// vless Reality over ws (no vision flow): all tuning must carry through.
	link := singbox.BuildShareLink(singbox.LinkParams{
		Type: "vless", Tag: "n1", Host: "1.2.3.4", Port: 443, UUID: "uuid-1",
		SNI: "a.com", PublicKey: "PBK", ShortID: "ab", Network: "ws", Path: "/p",
		TCPFastOpen: true, MPTCP: true, Mux: true, BrutalUp: 20, BrutalDown: 100,
	})

	clash, _ := Clash(ParseLinks([]string{link}), "")
	for _, want := range []string{"tfo: true", "mptcp: true", "brutal-opts", "up: 20", "down: 100"} {
		if !strings.Contains(clash, want) {
			t.Errorf("clash output missing %q:\n%s", want, clash)
		}
	}

	sb, _ := Singbox(ParseLinks([]string{link}), "")
	for _, want := range []string{`"tcp_fast_open": true`, `"tcp_multi_path": true`, `"brutal"`, `"up_mbps": 20`, `"down_mbps": 100`} {
		if !strings.Contains(sb, want) {
			t.Errorf("singbox output missing %q:\n%s", want, sb)
		}
	}
}

// TestTuningVisionSuppressesMux verifies that a vless node using xtls-rprx-vision
// flow never emits multiplex (sing-box rejects the two together), while the flow
// itself is preserved.
func TestTuningVisionSuppressesMux(t *testing.T) {
	link := singbox.BuildShareLink(singbox.LinkParams{
		Type: "vless", Tag: "n2", Host: "1.2.3.4", Port: 443, UUID: "u", SNI: "a.com",
		PublicKey: "PBK", ShortID: "ab", Flow: true, Mux: true, BrutalUp: 20, BrutalDown: 100,
	})

	sb, _ := Singbox(ParseLinks([]string{link}), "")
	if strings.Contains(sb, "multiplex") {
		t.Errorf("vision node must not carry multiplex:\n%s", sb)
	}
	if !strings.Contains(sb, "xtls-rprx-vision") {
		t.Errorf("vision flow should be preserved:\n%s", sb)
	}

	clash, _ := Clash(ParseLinks([]string{link}), "")
	if strings.Contains(clash, "smux") {
		t.Errorf("vision node must not carry smux:\n%s", clash)
	}
}

// TestZeroRTTAndEarlyData verifies tuic 0-RTT and vless ws early-data survive the
// link round-trip into both client formats.
func TestZeroRTTAndEarlyData(t *testing.T) {
	tuic := singbox.BuildShareLink(singbox.LinkParams{
		Type: "tuic", Tag: "t", Host: "1.2.3.4", Port: 443, UUID: "u", Password: "pw",
		SNI: "a.com", Congestion: "bbr", ZeroRTT: true,
	})
	if sb, _ := Singbox(ParseLinks([]string{tuic}), ""); !strings.Contains(sb, `"zero_rtt_handshake": true`) {
		t.Errorf("singbox tuic missing zero_rtt_handshake:\n%s", sb)
	}
	if cl, _ := Clash(ParseLinks([]string{tuic}), ""); !strings.Contains(cl, "reduce-rtt: true") {
		t.Errorf("clash tuic missing reduce-rtt:\n%s", cl)
	}

	ws := singbox.BuildShareLink(singbox.LinkParams{
		Type: "vless", Tag: "w", Host: "1.2.3.4", Port: 443, UUID: "u", SNI: "a.com",
		PublicKey: "PBK", ShortID: "ab", Network: "ws", Path: "/p",
		WSMaxEarlyData: 2560, WSEarlyDataHeader: "Sec-WebSocket-Protocol",
	})
	if sb, _ := Singbox(ParseLinks([]string{ws}), ""); !strings.Contains(sb, `"max_early_data": 2560`) || !strings.Contains(sb, `"early_data_header_name": "Sec-WebSocket-Protocol"`) {
		t.Errorf("singbox ws missing early data:\n%s", sb)
	}
	if cl, _ := Clash(ParseLinks([]string{ws}), ""); !strings.Contains(cl, "max-early-data: 2560") || !strings.Contains(cl, "early-data-header-name: Sec-WebSocket-Protocol") {
		t.Errorf("clash ws missing early data:\n%s", cl)
	}
}
