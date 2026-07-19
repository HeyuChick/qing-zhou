package subconv

import (
	"strings"
	"testing"

	"qingzhou/internal/singbox"
)

// TestVlessPacketEncodingRoundTrip pins the UDP packet encoding on VLESS across
// the whole chain (LinkParams -> share link -> parse -> client config). Without
// it each client falls back to its own default; when the two ends disagree UDP
// fails silently while TCP keeps working, which shows up as QUIC downloads
// hanging (Play Store "download pending") on an otherwise healthy node.
func TestVlessPacketEncodingRoundTrip(t *testing.T) {
	// Reality + vision — the common production shape.
	link := singbox.BuildShareLink(singbox.LinkParams{
		Type: "vless", Tag: "n1", Host: "1.2.3.4", Port: 443, UUID: "u",
		SNI: "a.com", PublicKey: "PBK", ShortID: "ab", Flow: true,
	})
	if !strings.Contains(link, "packetEncoding=xudp") {
		t.Errorf("share link missing packetEncoding: %s", link)
	}

	sb, _ := Singbox(ParseLinks([]string{link}), "")
	if !strings.Contains(sb, `"packet_encoding": "xudp"`) {
		t.Errorf("singbox output missing packet_encoding:\n%s", sb)
	}

	clash, _ := Clash(ParseLinks([]string{link}), "")
	if !strings.Contains(clash, "xudp: true") {
		t.Errorf("clash output missing xudp:\n%s", clash)
	}
}

// TestVlessPacketEncodingDefaultsWhenAbsent covers links minted before this was
// emitted (and hand-pasted ones): they must still get an explicit encoding
// rather than inheriting the client default.
func TestVlessPacketEncodingDefaultsWhenAbsent(t *testing.T) {
	legacy := "vless://u@1.2.3.4:443?security=reality&pbk=PBK&sid=ab&sni=a.com#legacy"

	sb, _ := Singbox(ParseLinks([]string{legacy}), "")
	if !strings.Contains(sb, `"packet_encoding": "xudp"`) {
		t.Errorf("legacy link should default to xudp:\n%s", sb)
	}

	clash, _ := Clash(ParseLinks([]string{legacy}), "")
	if !strings.Contains(clash, "xudp: true") {
		t.Errorf("legacy link should default to xudp in clash:\n%s", clash)
	}
}

// TestVlessPacketEncodingExplicitOverride verifies an encoding carried by the
// link wins over the xudp default, so a packetaddr-only upstream stays usable.
func TestVlessPacketEncodingExplicitOverride(t *testing.T) {
	link := "vless://u@1.2.3.4:443?security=tls&sni=a.com&packetEncoding=packetaddr#pa"

	sb, _ := Singbox(ParseLinks([]string{link}), "")
	if !strings.Contains(sb, `"packet_encoding": "packetaddr"`) {
		t.Errorf("explicit packetaddr should be preserved:\n%s", sb)
	}

	clash, _ := Clash(ParseLinks([]string{link}), "")
	if !strings.Contains(clash, "packet-addr: true") {
		t.Errorf("explicit packetaddr should map to packet-addr:\n%s", clash)
	}
	if strings.Contains(clash, "xudp: true") {
		t.Errorf("packetaddr node must not also claim xudp:\n%s", clash)
	}
}

// TestPacketEncodingIsVlessOnly guards against leaking the option into
// protocols that carry UDP natively and would reject it.
func TestPacketEncodingIsVlessOnly(t *testing.T) {
	links := []string{
		singbox.BuildShareLink(singbox.LinkParams{
			Type: "trojan", Tag: "t", Host: "1.2.3.4", Port: 443, Password: "pw", SNI: "a.com",
		}),
		singbox.BuildShareLink(singbox.LinkParams{
			Type: "hysteria2", Tag: "h", Host: "1.2.3.4", Port: 443, Password: "pw", SNI: "a.com",
		}),
	}
	for _, link := range links {
		if link == "" {
			t.Fatal("BuildShareLink returned empty link")
		}
		sb, _ := Singbox(ParseLinks([]string{link}), "")
		if strings.Contains(sb, "packet_encoding") {
			t.Errorf("non-vless node must not carry packet_encoding:\n%s", sb)
		}
	}
}
