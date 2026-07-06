package subconv

import (
	"encoding/base64"
	"encoding/json"
	"strings"
)

// RewriteHost replaces the server address (host) in a share link with newHost,
// preserving the port, credentials, query params (sni/pbk/...) and #remark.
// Used to deliver self-built nodes on a specific address (e.g. the origin IP),
// bypassing a relay/CDN. Reality stays valid: only the dial address changes; the
// SNI and reality keys live in the query string and are left untouched.
func RewriteHost(link, newHost string) string {
	if newHost == "" || link == "" {
		return link
	}
	if strings.HasPrefix(link, "vmess://") {
		return rewriteVmessHost(link, newHost)
	}
	i := strings.Index(link, "://")
	if i < 0 {
		return link
	}
	head, rest := link[:i+3], link[i+3:]
	at := strings.IndexByte(rest, '@')
	if at < 0 {
		return link // no userinfo@host form; leave as-is
	}
	hostStart := at + 1
	hostEnd := len(rest)
	if rel := strings.IndexAny(rest[hostStart:], "/?#"); rel >= 0 {
		hostEnd = hostStart + rel
	}
	hostport := rest[hostStart:hostEnd]
	newHostport := newHost
	if c := strings.LastIndexByte(hostport, ':'); c >= 0 {
		newHostport = newHost + hostport[c:] // keep :port
	}
	return head + rest[:hostStart] + newHostport + rest[hostEnd:]
}

func rewriteVmessHost(link, newHost string) string {
	dec, err := b64decode(strings.TrimPrefix(link, "vmess://"))
	if err != nil {
		return link
	}
	var m map[string]any
	if err := json.Unmarshal(dec, &m); err != nil {
		return link
	}
	m["add"] = newHost
	b, err := json.Marshal(m)
	if err != nil {
		return link
	}
	return "vmess://" + base64.StdEncoding.EncodeToString(b)
}
