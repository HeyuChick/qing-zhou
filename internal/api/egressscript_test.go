package api

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestEgressCheckScriptsAgainstLiveProxy runs the two shell scripts the egress
// checker pushes to a node — the single connectivity check and the concurrency
// probe — against a real proxy, with a real /bin/sh and a real curl.
//
// Everything else about these scripts is verified by reading them. That is not
// enough: they are text assembled in Go, and the failure modes are shell ones —
// a second `trap ... EXIT` silently replacing the first, a glob that matches the
// CA file along with the results, an unquoted expansion. None of those show up
// until a node runs it, and by then the symptom is "测试连通 says the egress is
// down" for an egress that is fine.
//
// Skipped unless QZ_EGRESS_TEST_PROXY names a proxy the machine can reach, in
// any of the formats the link parser accepts:
//
//	QZ_EGRESS_TEST_PROXY=socks5://user:pass@127.0.0.1:11080 \
//	  go test ./internal/api/ -run EgressCheckScripts -v
//
// Needs outbound internet: the scripts fetch an IP-echo service through the
// proxy, exactly as they do in production. QZ_EGRESS_TEST_SH overrides the
// shell (Git Bash's sh.exe on Windows).
//
// Don't read the subtest timings as script cost. On Windows whichever subtest
// runs first absorbs ~30s of one-time process-spawn overhead (Defender scanning
// sh.exe/curl.exe); the scripts themselves finish in a second or two, which is
// what the handler's 25s deadline is sized against.
func TestEgressCheckScriptsAgainstLiveProxy(t *testing.T) {
	spec := os.Getenv("QZ_EGRESS_TEST_PROXY")
	if spec == "" {
		t.Skip("set QZ_EGRESS_TEST_PROXY to a reachable proxy to run this")
	}
	parsed, err := parseEgressLink(spec)
	if err != nil {
		t.Fatalf("QZ_EGRESS_TEST_PROXY is not a recognised proxy spec: %v", err)
	}
	sh := os.Getenv("QZ_EGRESS_TEST_SH")
	if sh == "" {
		sh = "sh"
	}

	proxy, extra := egressProxyURL(parsed.Egress)
	run := func(t *testing.T, script string) (string, error) {
		t.Helper()
		path := filepath.Join(t.TempDir(), "check.sh")
		if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
		out, err := exec.Command(sh, path).CombinedOutput()
		return string(out), err
	}

	t.Run("single check reports an exit IP and a latency", func(t *testing.T) {
		script := egressCheckPrelude(proxy, extra, "", false) + egressSingleCheckScript()
		out, err := run(t, script)
		if err != nil {
			t.Fatalf("script failed: %v\n%s\n--- script ---\n%s", err, out, script)
		}
		// Same parsing the handler does: last line is time_total, the rest is the IP.
		out = strings.TrimSpace(out)
		nl := strings.LastIndexByte(out, '\n')
		if nl < 0 {
			t.Fatalf("output has no latency line: %q", out)
		}
		ip := strings.TrimSpace(out[:nl])
		if ip == "" || strings.ContainsAny(ip, " \t") {
			t.Errorf("first part should be a bare IP, got %q", ip)
		}
		if secs := parseFloat(strings.TrimSpace(out[nl+1:])); secs <= 0 {
			t.Errorf("latency did not parse from %q", out[nl+1:])
		}
	})

	t.Run("probe reports one line per connection", func(t *testing.T) {
		const n = 8
		script := egressCheckPrelude(proxy, extra, "", true) + egressProbeScript(n)
		out, err := run(t, script)
		if err != nil {
			t.Fatalf("script failed: %v\n%s\n--- script ---\n%s", err, out, script)
		}
		res := parseEgressProbeOutput(out)
		got := res["ok_count"].(int) + res["fail_count"].(int)
		if got != n {
			t.Fatalf("summarised %d of %d connections; raw output:\n%s", got, n, out)
		}
		t.Logf("probe: ok=%v fail=%v latency=%v/%v/%vms errors=%v",
			res["ok_count"], res["fail_count"],
			res["latency_min_ms"], res["latency_p50_ms"], res["latency_max_ms"], res["errors"])
	})

	// The CA file lives in the same scratch dir as the probe results, so a glob
	// that forgot to scope itself would fold the PEM into the summary.
	t.Run("a pinned trust anchor does not leak into probe results", func(t *testing.T) {
		const pem = "-----BEGIN CERTIFICATE-----\nQ0FGSUxFTUFSS0VS\n-----END CERTIFICATE-----"
		// Trust anchor without --proxy-insecure would make every connection fail
		// against this plain proxy; what matters here is only that the summary
		// counts stay at n and no PEM line is mistaken for a result.
		script := egressCheckPrelude(proxy, extra, pem, true) + egressProbeScript(4)
		out, _ := run(t, script)
		if strings.Contains(out, "CERTIFICATE") {
			t.Errorf("the CA file was swept into the probe output:\n%s", out)
		}
	})
}
