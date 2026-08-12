package store

import (
	"strings"
	"testing"
)

// The end-to-end assertion for the panel's own data path: an inbound bound to a
// proxy egress that drops UDP must advertise that in the subscription link.
//
// Without it the two halves disagree — the node rejects every UDP packet
// (Relay.RejectUDP -> a route reject on the node) while the subscription still
// tells clients the node carries UDP. Nothing refuses the traffic on the wire,
// so each QUIC/STUN attempt runs out its own timeout before the application
// falls back to TCP. That is invisible in the panel and reads to the user as
// "some sites are slow or won't load".
func TestSelfBuiltLinks_UDPBlockedEgressMarksLink(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "carol")
	pkg := mkPlan(t, st, "P", 10, 100, 30)
	buy(t, st, uid, pkg)

	blocking, err := st.SaveSbEgress(&SbEgress{
		Name: "静态IP-封UDP", Type: "socks", Host: "proxy.example.com", Port: 1080,
		Username: "u", Password: "p", UDPMode: UDPModeBlock,
	})
	if err != nil {
		t.Fatal(err)
	}
	passthrough, err := st.SaveSbEgress(&SbEgress{
		Name: "静态IP-放行UDP", Type: "socks", Host: "proxy2.example.com", Port: 1080,
		Username: "u", Password: "p", UDPMode: UDPModePassthrough,
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		tag      string
		port     int
		egressID int64
	}{
		{"blocked-in", 8443, blocking},
		{"open-in", 8444, passthrough},
		{"plain-in", 8445, 0}, // no egress at all — exits the node directly
	} {
		if _, err := st.SaveSbInbound(&SbInbound{
			Type: "vless", Tag: c.tag, Listen: "::", ListenPort: c.port,
			Options: "{}", Enabled: true, EgressID: c.egressID,
		}); err != nil {
			t.Fatal(err)
		}
	}

	u, err := st.UserByID(uid)
	if err != nil {
		t.Fatal(err)
	}
	byTag := map[string]string{}
	for _, l := range st.BuildSelfBuiltLinks(u, "example.com") {
		byTag[l.Tag] = l.Link
	}
	if len(byTag) != 3 {
		t.Fatalf("want 3 links, got %d: %v", len(byTag), byTag)
	}

	if !strings.Contains(byTag["blocked-in"], "qz-udp=block") {
		t.Errorf("inbound on a UDP-blocking egress must be marked:\n%s", byTag["blocked-in"])
	}
	// The marker is per node, not per subscription: a user holding both an
	// egress-backed node and a normal one keeps QUIC on the normal one.
	if strings.Contains(byTag["open-in"], "qz-udp") {
		t.Errorf("passthrough egress must not be marked:\n%s", byTag["open-in"])
	}
	if strings.Contains(byTag["plain-in"], "qz-udp") {
		t.Errorf("inbound with no egress must not be marked:\n%s", byTag["plain-in"])
	}
}

// An http egress cannot carry UDP at all (sing-box's http outbound has no
// packet path), so EffectiveUDPMode resolves it to block even with udp_mode
// unset. The link must be marked on that resolved value, not the stored one —
// otherwise every http-egress node in the panel silently keeps advertising UDP.
func TestSelfBuiltLinks_HTTPEgressMarkedByDefault(t *testing.T) {
	st := newRefundStore(t)
	uid := mkUser(t, st, "dave")
	pkg := mkPlan(t, st, "P", 10, 100, 30)
	buy(t, st, uid, pkg)

	egID, err := st.SaveSbEgress(&SbEgress{
		Name: "HTTP出口", Type: "http", Host: "proxy.example.com", Port: 8080,
		Username: "u", Password: "p", // UDPMode deliberately unset
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.SaveSbInbound(&SbInbound{
		Type: "vless", Tag: "http-eg-in", Listen: "::", ListenPort: 8443,
		Options: "{}", Enabled: true, EgressID: egID,
	}); err != nil {
		t.Fatal(err)
	}

	u, _ := st.UserByID(uid)
	links := st.BuildSelfBuiltLinks(u, "example.com")
	if len(links) != 1 {
		t.Fatalf("want 1 link, got %d", len(links))
	}
	if !strings.Contains(links[0].Link, "qz-udp=block") {
		t.Errorf("http egress resolves to block and must mark the link:\n%s", links[0].Link)
	}
}
