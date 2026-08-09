package subconv

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenderedConfigPassesSingboxCheck runs the real `sing-box check` over the
// config we actually ship to clients.
//
// Hand-written assertions cannot cover this. The migration to the typed DNS
// format looked complete and passed every unit test, yet the rendered config
// was still rejected outright by sing-box 1.13 — for a *second*, unrelated
// removal (`route.default_domain_resolver`) that no amount of reading the diff
// would have surfaced. Only the real binary said so.
//
// Skipped unless QZ_SINGBOX_TEST_BIN points at a sing-box binary, so the normal
// `go test ./...` has no external dependency:
//
//	QZ_SINGBOX_TEST_BIN=/path/to/sing-box go test ./internal/subconv/ -run Singbox
//
// Worth re-running against each new sing-box release: a deprecation whose
// removal version has arrived turns from a warning into a hard failure, and
// this is what notices.
func TestRenderedConfigPassesSingboxCheck(t *testing.T) {
	bin := os.Getenv("QZ_SINGBOX_TEST_BIN")
	if bin == "" {
		t.Skip("set QZ_SINGBOX_TEST_BIN to a sing-box binary to run this")
	}

	// Both a domain-addressed and an IP-addressed node: only the former forces
	// the dialer to resolve, which is what the domain-resolver rule is about.
	links := []string{
		"trojan://pw@node.example.com:443?security=tls&sni=node.example.com#A",
		"hysteria2://pw@203.0.113.7:8443?security=tls&insecure=1&sni=node.example.com#B",
		"tuic://11111111-2222-3333-4444-555555555555:pw@203.0.113.8:8444?security=tls&sni=node.example.com&udp_relay_mode=native#C",
	}

	for _, tc := range []struct {
		name string
		tpl  string
	}{
		{"builtin default", ""},
		// The shape an install that predates this change still has stored.
		{"legacy admin template, rewritten at render time", `{
		  "dns": {
		    "servers": [
		      {"tag": "remote", "address": "https://1.1.1.1/dns-query", "detour": "proxy"},
		      {"tag": "local", "address": "https://223.5.5.5/dns-query", "detour": "direct"},
		      {"tag": "fake", "address": "fakeip"}
		    ],
		    "rules": [{"query_type": ["A","AAAA"], "server": "fake"}],
		    "fakeip": {"enabled": true, "inet4_range": "198.18.0.0/15", "inet6_range": "fc00::/18"},
		    "final": "remote"
		  },
		  "route": {"auto_detect_interface": true, "final": "proxy", "rules": [{"action": "sniff"}]}
		}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Singbox(ParseLinks(links), tc.tpl)
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(out), 0o600); err != nil {
				t.Fatal(err)
			}
			res, err := exec.Command(bin, "check", "-c", path).CombinedOutput()
			text := stripANSI(string(res))
			if err != nil {
				t.Fatalf("sing-box rejected the config we ship:\n%s", text)
			}
			// A warning today is a hard failure in the release that removes the
			// feature, so surface them rather than letting them accumulate unseen.
			for _, line := range strings.Split(text, "\n") {
				if strings.Contains(line, "WARN") && strings.Contains(line, "deprecated") {
					t.Logf("deprecation warning (not yet fatal): %s", strings.TrimSpace(line))
				}
			}
		})
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
