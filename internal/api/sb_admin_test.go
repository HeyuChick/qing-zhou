package api

import (
	"strings"
	"testing"

	"qingzhou/internal/store"
)

// normalizeSniAddr had no coverage at all, and it sits in front of a dialer, so
// both directions matter: every legitimate host shape must reach a dialable
// address, and malformed input must be rejected here rather than surfacing later
// as a misleading "unreachable".
func TestNormalizeSniAddr(t *testing.T) {
	cases := []struct {
		name, host, port, want string
		wantErr                bool
	}{
		// no inline port -> explicit port, else 443
		{name: "domain", host: "example.com", want: "example.com:443"},
		{name: "domain explicit port", host: "example.com", port: "8443", want: "example.com:8443"},
		{name: "ipv4", host: "1.2.3.4", want: "1.2.3.4:443"},
		{name: "ipv4 explicit port", host: "1.2.3.4", port: "8443", want: "1.2.3.4:8443"},

		// inline port wins over the query param, matching the pre-existing contract
		{name: "domain with port", host: "example.com:8443", port: "9999", want: "example.com:8443"},
		{name: "ipv4 with port", host: "1.2.3.4:8443", want: "1.2.3.4:8443"},

		// IPv6: the whole reason this function exists. A bare literal contains
		// colons, so the old Contains(host, ":") check treated it as "already has
		// a port" and every IPv6 host was rejected outright.
		{name: "ipv6 bare", host: "2001:db8::1", want: "[2001:db8::1]:443"},
		{name: "ipv6 bare explicit port", host: "2001:db8::1", port: "8443", want: "[2001:db8::1]:8443"},
		{name: "ipv6 bracketed", host: "[2001:db8::1]", want: "[2001:db8::1]:443"},
		{name: "ipv6 bracketed with port", host: "[2001:db8::1]:8443", want: "[2001:db8::1]:8443"},
		{name: "ipv6 loopback", host: "::1", want: "[::1]:443"},

		// malformed input must 400 here. Bracketing any colon-bearing host would
		// have let these through to the dialer, turning "格式错误" into a much less
		// useful "unreachable".
		{name: "empty", host: "", wantErr: true},
		{name: "blank", host: "   ", wantErr: true},
		{name: "three segments", host: "host:port:extra", wantErr: true},
		{name: "letters with colons", host: "a:b:c", wantErr: true},
		{name: "unclosed bracket", host: "[2001:db8::1", wantErr: true},
		{name: "unopened bracket", host: "2001:db8::1]", wantErr: true},
		{name: "double bracketed", host: "[[2001:db8::1]]", wantErr: true},
		{name: "empty brackets", host: "[]", wantErr: true},
		{name: "port only", host: ":443", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := normalizeSniAddr(c.host, c.port)
			if c.wantErr {
				if err == nil {
					t.Errorf("normalizeSniAddr(%q, %q) = %q, want error", c.host, c.port, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeSniAddr(%q, %q) unexpected error: %v", c.host, c.port, err)
			}
			if got != c.want {
				t.Errorf("normalizeSniAddr(%q, %q) = %q, want %q", c.host, c.port, got, c.want)
			}
		})
	}
}

// egressProxyURL decides how the connectivity check dials the proxy, and a wrong
// scheme reads as "the proxy is broken" rather than "the panel asked for the
// wrong protocol" — the exact confusion this test pins down. The --resolve pair
// matters just as much: without it a TLS egress dialed by IP can never validate,
// because curl checks the certificate against whatever host is in the proxy URL.
func TestEgressProxyURL(t *testing.T) {
	cases := []struct {
		name      string
		eg        store.SbEgress
		want      string
		wantExtra []string
	}{
		{
			name: "socks resolves at the proxy",
			eg:   store.SbEgress{Type: "socks", Host: "9.9.9.9", Port: 1080, Username: "u", Password: "p"},
			want: "socks5h://u:p@9.9.9.9:1080",
		},
		{
			name: "plaintext http stays http",
			eg:   store.SbEgress{Type: "http", Host: "9.9.9.9", Port: 8080},
			want: "http://9.9.9.9:8080",
		},
		{
			name: "tls without sni keeps the address",
			eg:   store.SbEgress{Type: "http", Host: "proxy.example.com", Port: 443, TLSEnabled: true},
			want: "https://proxy.example.com:443",
		},
		{
			name:      "tls by ip carries the sni and is pointed back",
			eg:        store.SbEgress{Type: "http", Host: "9.9.9.9", Port: 443, TLSEnabled: true, SNI: "proxy.example.com"},
			want:      "https://proxy.example.com:443",
			wantExtra: []string{"--resolve", "proxy.example.com:443:9.9.9.9"},
		},
		{
			// A hostname address gives --resolve no IP to point at, so the URL
			// keeps it; curl then validates against that name.
			name: "tls by hostname ignores a differing sni",
			eg:   store.SbEgress{Type: "http", Host: "dial.example.com", Port: 443, TLSEnabled: true, SNI: "cert.example.com"},
			want: "https://dial.example.com:443",
		},
		{
			name:      "insecure is passed through",
			eg:        store.SbEgress{Type: "http", Host: "9.9.9.9", Port: 443, TLSEnabled: true, TLSInsecure: true},
			want:      "https://9.9.9.9:443",
			wantExtra: []string{"--proxy-insecure"},
		},
		{
			// The flag is meaningless on socks (sing-box has no tls there) and
			// must not silently turn the check into an https dial.
			name: "tls flag on socks is ignored",
			eg:   store.SbEgress{Type: "socks", Host: "9.9.9.9", Port: 1080, TLSEnabled: true, SNI: "proxy.example.com"},
			want: "socks5h://9.9.9.9:1080",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, extra := egressProxyURL(&c.eg)
			if got != c.want {
				t.Errorf("egressProxyURL() = %q, want %q", got, c.want)
			}
			if strings.Join(extra, " ") != strings.Join(c.wantExtra, " ") {
				t.Errorf("egressProxyURL() extra = %v, want %v", extra, c.wantExtra)
			}
		})
	}
}
