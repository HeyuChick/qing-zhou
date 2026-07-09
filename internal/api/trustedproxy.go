package api

import (
	"net"
	"net/http"
	"os"
	"strings"
)

// trustedProxyNets are the peer addresses whose forwarded headers
// (X-Forwarded-For / X-Real-IP / X-Forwarded-Host / X-Forwarded-Proto) we are
// willing to trust. Loopback is always trusted because the documented
// deployment runs nginx/Caddy on the same host; add more via
// QZ_TRUSTED_PROXIES (comma-separated IPs or CIDRs) when the reverse proxy is
// on another host.
//
// When the direct TCP peer is NOT trusted, forwarded headers are ignored — so a
// client cannot spoof its source IP to bypass the per-IP rate limiters, nor
// poison the Host used to build password-reset / verify / subscription links.
var trustedProxyNets = loadTrustedProxies()

func loadTrustedProxies() []*net.IPNet {
	nets := []*net.IPNet{mustCIDR("127.0.0.0/8"), mustCIDR("::1/128")}
	for _, tok := range strings.Split(os.Getenv("QZ_TRUSTED_PROXIES"), ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if !strings.Contains(tok, "/") {
			if strings.Contains(tok, ":") {
				tok += "/128"
			} else {
				tok += "/32"
			}
		}
		if _, n, err := net.ParseCIDR(tok); err == nil {
			nets = append(nets, n)
		}
	}
	return nets
}

func mustCIDR(s string) *net.IPNet {
	_, n, _ := net.ParseCIDR(s)
	return n
}

// peerTrusted reports whether the request's direct TCP peer is a trusted proxy.
func peerTrusted(r *http.Request) bool {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, n := range trustedProxyNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
