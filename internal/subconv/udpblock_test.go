package subconv

import (
	"strings"
	"testing"

	"qingzhou/internal/singbox"
)

// These tests pin the no-UDP marker across the whole chain (LinkParams ->
// share link -> parse -> client configs). A node whose inbound is bound to a
// UDP-blocking proxy egress drops every UDP packet at the server; if the
// subscription still advertises it as UDP-capable, clients relay QUIC/STUN
// into a silent black hole and each application waits out its own timeout
// before falling back to TCP. The marker makes the client itself refuse UDP —
// instantly, and only for that node.

func noUDPLink(t *testing.T, p singbox.LinkParams) string {
	t.Helper()
	p.NoUDP = true
	link := singbox.BuildShareLink(p)
	if link == "" {
		t.Fatalf("BuildShareLink returned empty link for %s", p.Type)
	}
	return link
}

// TestNoUDPRoundTripVless: the common production shape (Reality + vision).
func TestNoUDPRoundTripVless(t *testing.T) {
	link := noUDPLink(t, singbox.LinkParams{
		Type: "vless", Tag: "n1", Host: "1.2.3.4", Port: 443, UUID: "u",
		SNI: "a.com", PublicKey: "PBK", ShortID: "ab", Flow: true,
	})
	if !strings.Contains(link, "qz-udp=block") {
		t.Fatalf("share link missing qz-udp: %s", link)
	}

	sb, _ := Singbox(ParseLinks([]string{link}), "")
	if !strings.Contains(sb, `"network": "tcp"`) {
		t.Errorf("singbox output should restrict to tcp:\n%s", sb)
	}

	clash, _ := Clash(ParseLinks([]string{link}), "")
	if !strings.Contains(clash, "udp: false") {
		t.Errorf("clash output should advertise udp: false:\n%s", clash)
	}
	// No point pinning a UDP packet encoding on a node that carries no UDP.
	if strings.Contains(clash, "xudp: true") {
		t.Errorf("blocked node must not also claim xudp:\n%s", clash)
	}
}

// TestNoUDPRoundTripAllLinkTypes: every protocol BuildShareLink emits carries
// the marker, and the clash renderer honors it everywhere (mihomo's udp flag
// is universal). The sing-box network restriction is asserted separately —
// see TestNoUDPSingboxWhitelist for why not all protocols get it.
func TestNoUDPRoundTripAllLinkTypes(t *testing.T) {
	params := []singbox.LinkParams{
		{Type: "vless", Tag: "a", Host: "1.2.3.4", Port: 443, UUID: "u", SNI: "a.com", PublicKey: "PBK", ShortID: "ab"},
		{Type: "trojan", Tag: "b", Host: "1.2.3.4", Port: 443, Password: "pw", SNI: "a.com"},
		{Type: "vmess", Tag: "c", Host: "1.2.3.4", Port: 443, UUID: "u", TLS: true, SNI: "a.com"},
		{Type: "shadowsocks", Tag: "d", Host: "1.2.3.4", Port: 8388, Password: "sec", Method: "2022-blake3-aes-128-gcm", ServerKey: "AAAAAAAAAAAAAAAAAAAAAA=="},
		{Type: "tuic", Tag: "e", Host: "1.2.3.4", Port: 443, UUID: "u", Password: "pw", SNI: "a.com"},
		{Type: "hysteria2", Tag: "f", Host: "1.2.3.4", Port: 443, Password: "pw", SNI: "a.com"},
		{Type: "anytls", Tag: "g", Host: "1.2.3.4", Port: 443, Password: "pw", SNI: "a.com"},
	}
	for _, lp := range params {
		link := noUDPLink(t, lp)
		ps := ParseLinks([]string{link})
		if len(ps) != 1 {
			t.Errorf("%s: link did not parse: %s", lp.Type, link)
			continue
		}
		if !ps[0].udpBlocked() {
			t.Errorf("%s: parsed proxy should report udpBlocked: %s", lp.Type, link)
		}
		clash, _ := Clash(ps, "")
		if strings.Contains(clash, "udp: true") {
			t.Errorf("%s: clash still advertises udp: true:\n%s", lp.Type, clash)
		}
	}
}

// TestNoUDPClashOnlyFlipsEmittedKey pins the mihomo side of the whitelist:
// `udp` is flipped where the renderer already emits it and never introduced
// where it doesn't. mihomo's hysteria/hysteria2/tuic proxies have no udp
// option, and an unparseable proxy fails the entire config for every
// subscriber holding that node.
func TestNoUDPClashOnlyFlipsEmittedKey(t *testing.T) {
	for _, c := range []struct {
		lp       singbox.LinkParams
		wantsKey bool
	}{
		{singbox.LinkParams{Type: "vless", Tag: "a", Host: "1.2.3.4", Port: 443, UUID: "u", SNI: "a.com", PublicKey: "PBK", ShortID: "ab"}, true},
		{singbox.LinkParams{Type: "trojan", Tag: "b", Host: "1.2.3.4", Port: 443, Password: "pw", SNI: "a.com"}, true},
		{singbox.LinkParams{Type: "vmess", Tag: "c", Host: "1.2.3.4", Port: 443, UUID: "u", TLS: true, SNI: "a.com"}, true},
		{singbox.LinkParams{Type: "shadowsocks", Tag: "d", Host: "1.2.3.4", Port: 8388, Password: "sec", Method: "2022-blake3-aes-128-gcm", ServerKey: "AAAAAAAAAAAAAAAAAAAAAA=="}, true},
		{singbox.LinkParams{Type: "anytls", Tag: "e", Host: "1.2.3.4", Port: 443, Password: "pw", SNI: "a.com"}, true},
		{singbox.LinkParams{Type: "hysteria2", Tag: "f", Host: "1.2.3.4", Port: 443, Password: "pw", SNI: "a.com"}, false},
		{singbox.LinkParams{Type: "tuic", Tag: "g", Host: "1.2.3.4", Port: 443, UUID: "u", Password: "pw", SNI: "a.com"}, false},
		{singbox.LinkParams{Type: "hysteria", Tag: "h", Host: "1.2.3.4", Port: 443, Password: "pw", SNI: "a.com", UpMbps: 50, DownMbps: 100}, false},
	} {
		link := noUDPLink(t, c.lp)
		clash, _ := Clash(ParseLinks([]string{link}), "")
		got := strings.Contains(clash, "udp: false")
		if got != c.wantsKey {
			t.Errorf("%s: clash udp:false = %v, want %v:\n%s", c.lp.Type, got, c.wantsKey, clash)
		}
		if strings.Contains(clash, "udp: true") {
			t.Errorf("%s: blocked node must never claim udp: true:\n%s", c.lp.Type, clash)
		}
	}
}

// TestNoUDPSingboxWhitelist pins which protocols get `"network": "tcp"` in the
// sing-box output. sing-box rejects unknown fields by refusing the whole
// config, and its anytls outbound has no network option — emitting it there
// would take down every subscriber's profile for one anytls node.
func TestNoUDPSingboxWhitelist(t *testing.T) {
	cases := []struct {
		lp          singbox.LinkParams
		wantNetwork bool
	}{
		{singbox.LinkParams{Type: "vless", Tag: "a", Host: "1.2.3.4", Port: 443, UUID: "u", SNI: "a.com", PublicKey: "PBK", ShortID: "ab"}, true},
		{singbox.LinkParams{Type: "trojan", Tag: "b", Host: "1.2.3.4", Port: 443, Password: "pw", SNI: "a.com"}, true},
		{singbox.LinkParams{Type: "vmess", Tag: "c", Host: "1.2.3.4", Port: 443, UUID: "u", TLS: true, SNI: "a.com"}, true},
		{singbox.LinkParams{Type: "shadowsocks", Tag: "d", Host: "1.2.3.4", Port: 8388, Password: "sec", Method: "2022-blake3-aes-128-gcm", ServerKey: "AAAAAAAAAAAAAAAAAAAAAA=="}, true},
		{singbox.LinkParams{Type: "tuic", Tag: "e", Host: "1.2.3.4", Port: 443, UUID: "u", Password: "pw", SNI: "a.com"}, true},
		{singbox.LinkParams{Type: "hysteria2", Tag: "f", Host: "1.2.3.4", Port: 443, Password: "pw", SNI: "a.com"}, true},
		{singbox.LinkParams{Type: "anytls", Tag: "g", Host: "1.2.3.4", Port: 443, Password: "pw", SNI: "a.com"}, false},
	}
	for _, c := range cases {
		link := noUDPLink(t, c.lp)
		sb, _ := Singbox(ParseLinks([]string{link}), "")
		got := strings.Contains(sb, `"network": "tcp"`)
		if got != c.wantNetwork {
			t.Errorf("%s: network restriction = %v, want %v:\n%s", c.lp.Type, got, c.wantNetwork, sb)
		}
	}
}

// TestNoUDPAbsentMeansUnchanged: links without the marker (every node not on a
// blocking egress, and all pre-existing links) render exactly as before.
func TestNoUDPAbsentMeansUnchanged(t *testing.T) {
	link := singbox.BuildShareLink(singbox.LinkParams{
		Type: "vless", Tag: "n", Host: "1.2.3.4", Port: 443, UUID: "u",
		SNI: "a.com", PublicKey: "PBK", ShortID: "ab",
	})
	if strings.Contains(link, "qz-udp") {
		t.Fatalf("marker leaked into a normal link: %s", link)
	}
	sb, _ := Singbox(ParseLinks([]string{link}), "")
	if strings.Contains(sb, `"network"`) {
		t.Errorf("normal node must not be network-restricted:\n%s", sb)
	}
	clash, _ := Clash(ParseLinks([]string{link}), "")
	if !strings.Contains(clash, "udp: true") {
		t.Errorf("normal node should keep udp: true:\n%s", clash)
	}
}

// TestNoUDPSurge: surge output flips udp-relay for ss and trojan.
func TestNoUDPSurge(t *testing.T) {
	blocked := noUDPLink(t, singbox.LinkParams{
		Type: "trojan", Tag: "t", Host: "1.2.3.4", Port: 443, Password: "pw", SNI: "a.com",
	})
	normal := singbox.BuildShareLink(singbox.LinkParams{
		Type: "trojan", Tag: "t2", Host: "1.2.3.5", Port: 443, Password: "pw", SNI: "a.com",
	})
	out := Surge(ParseLinks([]string{blocked, normal}), "")
	if !strings.Contains(out, "udp-relay=false") {
		t.Errorf("blocked trojan should carry udp-relay=false:\n%s", out)
	}
	if !strings.Contains(out, "udp-relay=true") {
		t.Errorf("normal trojan should keep udp-relay=true:\n%s", out)
	}
}

// TestNoUDPKeepsNodeKeyStable: flipping an egress's udp_mode (or unbinding the
// egress) toggles the marker on the link; the user's per-node blocklist is
// keyed by NodeKey and must survive that.
func TestNoUDPKeepsNodeKeyStable(t *testing.T) {
	base := singbox.LinkParams{
		Type: "vless", Tag: "n", Host: "1.2.3.4", Port: 443, UUID: "u",
		SNI: "a.com", PublicKey: "PBK", ShortID: "ab",
	}
	with := noUDPLink(t, base)
	without := singbox.BuildShareLink(base)
	if NodeKey(with) != NodeKey(without) {
		t.Errorf("NodeKey must not change with qz-udp:\n  with:    %s\n  without: %s", with, without)
	}

	// ss gains a query string only when blocked; the canonical form must drop
	// it entirely so the key matches the historic no-query link.
	ssBase := singbox.LinkParams{
		Type: "shadowsocks", Tag: "s", Host: "1.2.3.4", Port: 8388,
		Password: "sec", Method: "2022-blake3-aes-128-gcm", ServerKey: "AAAAAAAAAAAAAAAAAAAAAA==",
	}
	ssWith := noUDPLink(t, ssBase)
	ssWithout := singbox.BuildShareLink(ssBase)
	if NodeKey(ssWith) != NodeKey(ssWithout) {
		t.Errorf("ss NodeKey must not change with qz-udp:\n  with:    %s\n  without: %s", ssWith, ssWithout)
	}
}
