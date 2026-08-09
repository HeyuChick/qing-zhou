package subconv

import (
	"net"
	"strconv"
	"strings"
)

// modernizeSingboxDNS rewrites a sing-box client config's `dns` block from the
// legacy address-string server format to the typed format introduced in
// sing-box 1.12.
//
// Why this exists: the legacy format is not merely deprecated, it is REMOVED in
// sing-box 1.14.0. A subscription still carrying `{"address": "https://…"}`
// does not warn on a 1.14 client — it fails to parse, and the user's whole
// profile stops loading. 1.14 is already in beta, so the cutover has to land
// before clients pick it up, not after.
//
// It runs on every render rather than only on the built-in default, because the
// template is admin-editable and stored in the DB: an install that pasted or
// hand-wrote a template years ago would otherwise keep serving the dead format
// forever. Conversion is idempotent — an entry that already has `type` is left
// alone — so re-rendering a modern template is a no-op.
//
// The floor moves to sing-box ≥1.12 as a consequence. That is unavoidable: no
// single DNS block parses on both 1.11 and 1.14, and 1.11 has been superseded
// for over a year while 1.14 is imminent.
//
// Conservative by construction: only address forms whose typed equivalent is
// unambiguous are converted. Anything else (notably `rcode://`, which became a
// DNS *rule action* rather than a server and therefore cannot be rewritten
// without also rewriting every rule that names it) is passed through untouched,
// so an admin sees sing-box's own error instead of silently getting different
// resolution behavior than they configured.
func modernizeSingboxDNS(doc map[string]any) {
	dns, ok := doc["dns"].(map[string]any)
	if !ok {
		return
	}
	servers := mapSlice(dns["servers"])
	if len(servers) == 0 {
		return
	}

	// The legacy top-level fakeip block is folded into the fakeip server itself.
	// Read it before converting so the ranges survive; drop it afterwards, but
	// only if a fakeip server actually claimed them.
	fakeip, _ := dns["fakeip"].(map[string]any)
	fakeipClaimed := false

	for _, srv := range servers {
		if _, typed := srv["type"]; typed {
			// Already the modern format. One exception: a fakeip server that was
			// hand-converted but left its ranges behind in the legacy block would
			// silently fall back to sing-box's defaults, so top them up here.
			if srv["type"] == "fakeip" && fakeip != nil {
				fakeipClaimed = adoptFakeIPRanges(srv, fakeip) || fakeipClaimed
			}
			continue
		}
		addr, _ := srv["address"].(string)
		addr = strings.TrimSpace(addr)
		if addr == "" {
			continue
		}
		if !convertLegacyDNSServer(srv, addr, fakeip, &fakeipClaimed) {
			continue
		}
		delete(srv, "address")
		// Legacy dial-side fields renamed when the server grew a real dialer.
		renameKey(srv, "address_resolver", "domain_resolver")
		renameKey(srv, "address_fallback_delay", "fallback_delay")
		// `strategy` / `address_strategy` have no per-server equivalent in the
		// typed format (answer filtering moved to DNS rule actions). Carrying
		// them over would make the very versions this rewrite targets reject the
		// config as an unknown field, so they are dropped rather than migrated.
		delete(srv, "strategy")
		delete(srv, "address_strategy")
	}

	if fakeipClaimed {
		delete(dns, "fakeip")
	}
}

// convertLegacyDNSServer turns one legacy `address` value into typed fields on
// srv, reporting whether it recognised the form. srv is left untouched when it
// returns false.
func convertLegacyDNSServer(srv map[string]any, addr string, fakeip map[string]any, fakeipClaimed *bool) bool {
	switch strings.ToLower(addr) {
	case "local":
		srv["type"] = "local"
		return true
	case "fakeip":
		srv["type"] = "fakeip"
		if adoptFakeIPRanges(srv, fakeip) {
			*fakeipClaimed = true
		} else if fakeip != nil {
			// A fakeip block that carried nothing but `enabled` is still consumed:
			// leaving it behind would trip 1.14's unknown-field check for no gain.
			*fakeipClaimed = true
		}
		return true
	case "hosts":
		srv["type"] = "hosts"
		return true
	}

	scheme, rest, hasScheme := strings.Cut(addr, "://")
	if !hasScheme {
		// A bare address was UDP in the legacy format.
		return applyDNSHostPort(srv, "udp", addr, 53)
	}
	switch strings.ToLower(scheme) {
	case "dhcp":
		srv["type"] = "dhcp"
		// "dhcp://auto" means "pick the interface yourself", which is the typed
		// format's default — expressed by omitting `interface`.
		if iface := strings.TrimSpace(rest); iface != "" && !strings.EqualFold(iface, "auto") {
			srv["interface"] = iface
		}
		return true
	case "tcp":
		return applyDNSHostPort(srv, "tcp", rest, 53)
	case "udp":
		return applyDNSHostPort(srv, "udp", rest, 53)
	case "tls":
		return applyDNSHostPort(srv, "tls", rest, 853)
	case "quic":
		return applyDNSHostPort(srv, "quic", rest, 853)
	case "https":
		return applyDNSURL(srv, "https", rest, 443)
	case "h3":
		return applyDNSURL(srv, "h3", rest, 443)
	}
	// rcode://, and anything else we do not positively recognise.
	return false
}

// adoptFakeIPRanges copies the legacy top-level fakeip ranges onto a typed
// fakeip server, reporting whether it took anything. Ranges already present on
// the server win — a hand-written value must not be clobbered by a stale block.
func adoptFakeIPRanges(srv map[string]any, fakeip map[string]any) bool {
	if fakeip == nil {
		return false
	}
	took := false
	for _, k := range []string{"inet4_range", "inet6_range"} {
		if _, exists := srv[k]; exists {
			continue
		}
		if v, ok := fakeip[k]; ok {
			srv[k] = v
			took = true
		}
	}
	return took
}

// applyDNSURL converts an address carrying an optional path ("https://1.1.1.1/dns-query").
func applyDNSURL(srv map[string]any, typ, rest string, defaultPort int) bool {
	authority, path, hasPath := strings.Cut(rest, "/")
	if !applyDNSHostPort(srv, typ, authority, defaultPort) {
		return false
	}
	// "/dns-query" is the typed format's own default; only a divergent path is
	// worth emitting.
	if hasPath && path != "" && path != "dns-query" {
		srv["path"] = "/" + path
	}
	return true
}

// applyDNSHostPort sets type/server/server_port from a "host" or "host:port"
// authority, bracketed IPv6 included. defaultPort is the typed format's own
// default for that type and is therefore left implicit.
func applyDNSHostPort(srv map[string]any, typ, authority string, defaultPort int) bool {
	authority = strings.TrimSpace(authority)
	if authority == "" {
		return false
	}
	host, portStr := authority, ""
	if h, p, err := net.SplitHostPort(authority); err == nil {
		host, portStr = h, p
	} else if strings.HasPrefix(authority, "[") && strings.HasSuffix(authority, "]") {
		// A bracketed IPv6 literal with no port: SplitHostPort rejects it.
		host = authority[1 : len(authority)-1]
	}
	if host == "" {
		return false
	}
	srv["type"] = typ
	srv["server"] = host
	if portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil && port > 0 && port != defaultPort {
			srv["server_port"] = port
		}
	}
	return true
}

func renameKey(m map[string]any, from, to string) {
	v, ok := m[from]
	if !ok {
		return
	}
	delete(m, from)
	if _, exists := m[to]; !exists {
		m[to] = v
	}
}
