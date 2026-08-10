package api

import (
	"errors"
	"testing"
)

// TestParseEgressLink covers every shape a static-IP vendor is known to hand
// out. The cases that matter most are the ambiguous ones: which end of a
// colon-separated quadruple is the address, and what an https:// scheme means
// (TLS to the proxy) versus a plaintext proxy that merely listens on 443.
func TestParseEgressLink(t *testing.T) {
	for _, tc := range []struct {
		name    string
		in      string
		typ     string
		host    string
		port    int
		user    string
		pass    string
		tls     bool
		sni     string
		egName  string
		guessed bool
	}{
		{
			name: "socks5 url with credentials",
			in:   "socks5://user1:pw1@1.2.3.4:1080",
			typ:  "socks", host: "1.2.3.4", port: 1080, user: "user1", pass: "pw1",
		},
		{
			name: "socks5h is the same outbound",
			in:   "socks5h://user1:pw1@1.2.3.4:1080",
			typ:  "socks", host: "1.2.3.4", port: 1080, user: "user1", pass: "pw1",
		},
		{
			name: "http url",
			in:   "http://user1:pw1@proxy.example.com:8080",
			typ:  "http", host: "proxy.example.com", port: 8080, user: "user1", pass: "pw1",
		},
		{
			// The distinction the UI keeps warning about: https means the hop is
			// wrapped in TLS, which is not the same as a plaintext proxy on 443.
			name: "https url means TLS to the proxy",
			in:   "https://user1:pw1@proxy.example.com:443?sni=real.example.com",
			typ:  "http", host: "proxy.example.com", port: 443, user: "user1", pass: "pw1",
			tls: true, sni: "real.example.com",
		},
		{
			name: "plaintext proxy on 443 stays plaintext",
			in:   "http://1.2.3.4:443",
			typ:  "http", host: "1.2.3.4", port: 443,
		},
		{
			name: "fragment names the egress",
			in:   "socks5://1.2.3.4:1080#香港静态",
			typ:  "socks", host: "1.2.3.4", port: 1080, egName: "香港静态",
		},
		{
			name: "percent-encoded credentials are decoded",
			in:   "socks5://us%40er:p%3Aw@1.2.3.4:1080",
			typ:  "socks", host: "1.2.3.4", port: 1080, user: "us@er", pass: "p:w",
		},
		{
			name: "vendor csv host:port:user:pass",
			in:   "1.2.3.4:1080:user1:pw1",
			typ:  "socks", host: "1.2.3.4", port: 1080, user: "user1", pass: "pw1", guessed: true,
		},
		{
			name: "vendor csv reversed user:pass:host:port",
			in:   "user1:pw1:1.2.3.4:1080",
			typ:  "socks", host: "1.2.3.4", port: 1080, user: "user1", pass: "pw1", guessed: true,
		},
		{
			// A password containing a colon must survive; the split is bounded by
			// the address end, not by counting fields.
			name: "csv password containing a colon",
			in:   "1.2.3.4:1080:user1:pw:with:colons",
			typ:  "socks", host: "1.2.3.4", port: 1080, user: "user1", pass: "pw:with:colons", guessed: true,
		},
		{
			name: "at-form without scheme",
			in:   "user1:pw1@1.2.3.4:1080",
			typ:  "socks", host: "1.2.3.4", port: 1080, user: "user1", pass: "pw1", guessed: true,
		},
		{
			// Split at the LAST @, so an @ inside the password does not eat the host.
			name: "at-form with an at sign in the password",
			in:   "user1:pw@1@1.2.3.4:1080",
			typ:  "socks", host: "1.2.3.4", port: 1080, user: "user1", pass: "pw@1", guessed: true,
		},
		{
			name: "bare host:port, no auth",
			in:   "1.2.3.4:1080",
			typ:  "socks", host: "1.2.3.4", port: 1080, guessed: true,
		},
		{
			name: "http proxy port is guessed as http",
			in:   "1.2.3.4:3128",
			typ:  "http", host: "1.2.3.4", port: 3128, guessed: true,
		},
		{
			// Bracketed IPv6 is full of colons and must not be read as the CSV shape.
			name: "bracketed ipv6",
			in:   "[2001:db8::1]:1080",
			typ:  "socks", host: "2001:db8::1", port: 1080, guessed: true,
		},
		{
			name: "ipv6 in a url",
			in:   "socks5://user1:pw1@[2001:db8::1]:1080",
			typ:  "socks", host: "2001:db8::1", port: 1080, user: "user1", pass: "pw1",
		},
		{
			name: "spreadsheet quoting and trailing comma are stripped",
			in:   `  "1.2.3.4:1080:user1:pw1",  `,
			typ:  "socks", host: "1.2.3.4", port: 1080, user: "user1", pass: "pw1", guessed: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseEgressLink(tc.in)
			if err != nil {
				t.Fatalf("parseEgressLink(%q) = %v", tc.in, err)
			}
			e := got.Egress
			if e.Type != tc.typ || e.Host != tc.host || e.Port != tc.port {
				t.Errorf("addr: got %s %s:%d, want %s %s:%d", e.Type, e.Host, e.Port, tc.typ, tc.host, tc.port)
			}
			if e.Username != tc.user || e.Password != tc.pass {
				t.Errorf("creds: got %q/%q, want %q/%q", e.Username, e.Password, tc.user, tc.pass)
			}
			if e.TLSEnabled != tc.tls || e.SNI != tc.sni {
				t.Errorf("tls: got %v/%q, want %v/%q", e.TLSEnabled, e.SNI, tc.tls, tc.sni)
			}
			if e.Name != tc.egName {
				t.Errorf("name: got %q, want %q", e.Name, tc.egName)
			}
			if got.TypeGuessed != tc.guessed {
				t.Errorf("TypeGuessed = %v, want %v", got.TypeGuessed, tc.guessed)
			}
		})
	}
}

func TestParseEgressLinkRejects(t *testing.T) {
	for _, tc := range []struct{ name, in string }{
		{"unsupported scheme", "ss://user:pw@1.2.3.4:1080"},
		{"url without a port", "socks5://1.2.3.4"},
		{"port out of range", "1.2.3.4:70000"},
		{"port not a number", "1.2.3.4:abcd"},
		{"three fields is not a known shape", "1.2.3.4:1080:user1"},
		{"no address at all", "just-some-words"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := parseEgressLink(tc.in); err == nil {
				t.Errorf("parseEgressLink(%q) should fail, got %+v", tc.in, got.Egress)
			}
		})
	}
}

// Blank and comment lines are skipped rather than reported, so pasting a
// vendor's mail wholesale doesn't produce a wall of errors for its prose.
func TestParseEgressLinkSkipsNoise(t *testing.T) {
	for _, in := range []string{"", "   ", "# 这是注释", "// comment"} {
		if _, err := parseEgressLink(in); !errors.Is(err, errEgressLinkEmpty) {
			t.Errorf("parseEgressLink(%q) = %v, want errEgressLinkEmpty", in, err)
		}
	}
}

func TestEgressLinkFallbackName(t *testing.T) {
	p, err := parseEgressLink("1.2.3.4:1080")
	if err != nil {
		t.Fatal(err)
	}
	if got := egressLinkFallbackName(p.Egress); got != "1.2.3.4:1080" {
		t.Errorf("fallback name = %q", got)
	}
	p6, err := parseEgressLink("[2001:db8::1]:1080")
	if err != nil {
		t.Fatal(err)
	}
	// Must stay a re-parseable address, i.e. keep the brackets.
	if got := egressLinkFallbackName(p6.Egress); got != "[2001:db8::1]:1080" {
		t.Errorf("ipv6 fallback name = %q", got)
	}
}

// TestParseEgressProbeOutput checks the summary the concurrency probe produces,
// including that identical errors are folded — sixteen copies of one timeout is
// one fact, and rendering it sixteen times buries the counts.
func TestParseEgressProbeOutput(t *testing.T) {
	res := parseEgressProbeOutput(`ok 0.412
ok 0.199
err curl: (28) Operation timed out
ok 0.850
err curl: (28) Operation timed out
err curl: (56) Recv failure`)

	if res["ok_count"] != 3 || res["fail_count"] != 3 {
		t.Errorf("counts = %v/%v, want 3/3", res["ok_count"], res["fail_count"])
	}
	if res["ok"] != false {
		t.Error("a run with failures must not report ok")
	}
	if res["latency_min_ms"] != 199 || res["latency_max_ms"] != 850 {
		t.Errorf("latency spread = %v..%v, want 199..850", res["latency_min_ms"], res["latency_max_ms"])
	}
	errs, ok := res["errors"].([]egressProbeError)
	if !ok {
		t.Fatalf("errors has unexpected type %T", res["errors"])
	}
	if len(errs) != 2 || errs[0].Count != 2 {
		t.Errorf("errors not folded most-frequent-first: %+v", errs)
	}

	clean := parseEgressProbeOutput("ok 0.100\nok 0.200")
	if clean["ok"] != true || clean["fail_count"] != 0 {
		t.Errorf("all-success run = %v", clean)
	}
	// No output at all is a failed probe, not a successful one with zero
	// connections — otherwise a script that died early reads as healthy.
	empty := parseEgressProbeOutput("")
	if empty["ok"] != false {
		t.Errorf("empty probe output must not report ok: %v", empty)
	}
}
