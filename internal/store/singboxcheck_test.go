package store

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"qingzhou/internal/singbox"
)

// TestServerConfigPassesSingboxCheck runs the real `sing-box check` over the
// SERVER config 轻舟 pushes to a node, with every wiring shape at once: a plain
// direct-exit inbound, a relay hop into a landing (which now carries
// multiplex), and two third-party egresses — one blocking UDP, one passing it
// through — with their connect_timeout.
//
// Unit tests assert what we thought we emitted; only the binary says whether
// sing-box will start. That gap has bitten this repo before: a config that
// passed every assertion was rejected outright by 1.13 for an option removal
// nobody had noticed. Every knob added here (connect_timeout, a
// network+action rule, multiplex on a server-side outbound) is a new chance to
// emit something that parses as JSON and is not valid sing-box.
//
// Skipped unless QZ_SINGBOX_TEST_BIN points at a sing-box binary:
//
//	QZ_SINGBOX_TEST_BIN=/path/to/sing-box go test ./internal/store/ -run SingboxCheck
//
// Worth re-running against each new sing-box release — a deprecation whose
// removal has landed turns from a warning into a hard failure here first.
func TestServerConfigPassesSingboxCheck(t *testing.T) {
	bin := os.Getenv("QZ_SINGBOX_TEST_BIN")
	if bin == "" {
		t.Skip("set QZ_SINGBOX_TEST_BIN to a sing-box binary to run this")
	}
	st := newEgressTestStore(t)

	// A Reality profile, because that is what a real landing inbound uses and it
	// is the case where multiplex must be suppressed (vision) or emitted.
	//
	// The keypair is generated rather than pasted. sing-box parses the private
	// key during `check`, so it has to be a genuine x25519 key — and a genuine
	// key checked into the repo is a high-entropy blob that secret scanners
	// (rightly) cannot tell apart from a real one.
	priv, pub, err := singbox.GenerateRealityKeypair()
	if err != nil {
		t.Fatal(err)
	}
	tlsID, err := st.SaveSbTls(&SbTls{
		Name: "landing-tls",
		ServerJSON: `{"enabled":true,"server_name":"www.microsoft.com","reality":{"enabled":true,
			"handshake":{"server":"www.microsoft.com","server_port":443},
			"private_key":"` + priv + `","short_id":["0123456789abcdef"]}}`,
		ClientJSON: `{"reality":{"enabled":true,"public_key":"` + pub + `","short_id":"0123456789abcdef"},
			"utls":{"enabled":true,"fingerprint":"chrome"}}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	blockID, err := st.SaveSbEgress(&SbEgress{
		Name: "阻断UDP", Type: "http", Host: "proxy.example.com", Port: 8080,
		Username: "u", Password: "p", ConnectTimeoutMS: 3000,
	})
	if err != nil {
		t.Fatal(err)
	}
	passID, err := st.SaveSbEgress(&SbEgress{
		Name: "透传UDP", Type: "socks", Host: "203.0.113.9", Port: 1080,
		Username: "u", Password: "p", UDPMode: UDPModePassthrough,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Landing: multiplex on, no vision flow (sing-box rejects the pair), so the
	// relay hop below actually emits a multiplex block.
	landingID, err := st.SaveSbInbound(&SbInbound{
		Type: "vless", Tag: "landing", ListenPort: 443, TlsID: tlsID,
		Options: `{"flow":"none","multiplex":{"enabled":true}}`, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, ib := range []struct {
		tag      string
		port     int
		upstream int64
		egress   int64
	}{
		{"relay", 8443, landingID, 0},
		{"via-http-egress", 8444, 0, blockID},
		{"via-socks-egress", 8445, 0, passID},
		{"direct", 8446, 0, 0},
	} {
		if _, err := st.SaveSbInbound(&SbInbound{
			Type: "vless", Tag: ib.tag, ListenPort: ib.port, TlsID: tlsID,
			Options: `{"flow":"none"}`, Enabled: true,
			UpstreamInboundID: ib.upstream, EgressID: ib.egress,
		}); err != nil {
			t.Fatal(err)
		}
	}

	user := []singbox.User{{Name: "u1", UUID: "11111111-1111-1111-1111-111111111111", Password: "pw"}}
	users := map[string][]singbox.User{}
	for _, tag := range []string{"landing", "relay", "via-http-egress", "via-socks-egress", "direct"} {
		users[tag] = user
	}

	// Both shapes the panel actually pushes: a node whose sing-box carries the
	// v2ray_api plugin (per-user stats) and one without it, where the block is
	// omitted entirely.
	for _, tc := range []struct{ name, v2ray string }{
		{"with v2ray_api", "127.0.0.1:18080"},
		{"without v2ray_api", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := st.BuildSingboxConfig(singbox.DefaultBaseConfig, tc.v2ray, users)
			if err != nil {
				t.Fatal(err)
			}
			// Sanity: the things this test exists to validate must actually be
			// present, or a passing `check` would only prove an empty config is valid.
			for _, want := range []string{`"connect_timeout": "3000ms"`, `"network": "udp"`, `"action": "reject"`, `"multiplex"`} {
				if !strings.Contains(string(cfg), want) {
					t.Fatalf("generated config is missing %s — the check below would prove nothing:\n%s", want, cfg)
				}
			}

			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, cfg, 0o600); err != nil {
				t.Fatal(err)
			}
			out, err := exec.Command(bin, "check", "-c", path).CombinedOutput()
			if err != nil {
				// A binary built without the v2ray_api tag rejects the stats block
				// itself. That says nothing about the routing and outbounds this test
				// is here for, and the sibling case covers them on the same binary.
				if strings.Contains(string(out), "with_v2ray_api") {
					t.Skipf("this sing-box build lacks with_v2ray_api:\n%s", out)
				}
				t.Fatalf("sing-box check failed: %v\n%s\n--- config ---\n%s", err, out, cfg)
			}
			if len(out) > 0 {
				// check is silent on success; anything printed is a deprecation
				// warning worth seeing before it becomes a hard failure in a later
				// release.
				t.Logf("sing-box check output:\n%s", out)
			}
		})
	}
}
