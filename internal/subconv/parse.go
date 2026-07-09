// Package subconv parses proxy share-links and renders subscriptions in
// base64 (link list), Clash (mihomo) YAML, and sing-box JSON, injecting an
// anti-leak template (fake-ip DNS / TUN / sniffer) into the latter two.
package subconv

import (
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// Proxy is a normalized node parsed from a share link.
type Proxy struct {
	Raw      string
	Protocol string // vless | vmess | ss | trojan | hysteria2 | tuic
	Name     string
	Server   string
	Port     int
	UUID     string
	Password string
	Method   string // ss cipher
	AlterID  int    // vmess
	Params   url.Values
	VMess    map[string]any
	Group    string // panel node-group name (for the per-group auto-select group)
}

func b64decode(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, fmt.Errorf("invalid base64")
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }

// ParseLink parses a single share URI into a Proxy.
func ParseLink(raw string) (*Proxy, error) {
	raw = strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(raw, "vless://"):
		return parseURLStyle(raw, "vless")
	case strings.HasPrefix(raw, "trojan://"):
		return parseURLStyle(raw, "trojan")
	case strings.HasPrefix(raw, "hysteria2://"):
		return parseURLStyle(raw, "hysteria2")
	case strings.HasPrefix(raw, "hy2://"):
		return parseURLStyle(strings.Replace(raw, "hy2://", "hysteria2://", 1), "hysteria2")
	case strings.HasPrefix(raw, "tuic://"):
		return parseTuic(raw)
	case strings.HasPrefix(raw, "vmess://"):
		return parseVmess(raw)
	case strings.HasPrefix(raw, "ss://"):
		return parseSS(raw)
	default:
		return nil, fmt.Errorf("unsupported scheme")
	}
}

// ParseList parses a newline/whitespace-separated list, skipping unparseable
// lines. Accepts a base64-encoded blob or raw text.
func ParseList(blob string) []*Proxy {
	blob = strings.TrimSpace(blob)
	// Clash/mihomo YAML config (proxies: …) — convert each node to a share link.
	if looksLikeClash(blob) {
		if ps := ParseClashYAML(blob); len(ps) > 0 {
			return ps
		}
	}
	if !strings.Contains(blob, "://") {
		if dec, err := b64decode(blob); err == nil {
			blob = string(dec)
		}
	}
	var out []*Proxy
	for _, line := range strings.FieldsFunc(blob, func(r rune) bool { return r == '\n' || r == '\r' }) {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, "://") {
			continue
		}
		if p, err := ParseLink(line); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func parseURLStyle(raw, proto string) (*Proxy, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	p := &Proxy{
		Raw:      raw,
		Protocol: proto,
		Name:     frag(u),
		Server:   u.Hostname(),
		Port:     atoi(u.Port()),
		Params:   u.Query(),
	}
	switch proto {
	case "vless":
		p.UUID = u.User.Username()
	case "trojan", "hysteria2":
		// password lives in the userinfo
		if pw := u.User.Username(); pw != "" {
			p.Password = pw
		}
	}
	if p.Name == "" {
		p.Name = p.Server
	}
	return p, nil
}

func parseTuic(raw string) (*Proxy, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, err
	}
	pw, _ := u.User.Password()
	p := &Proxy{
		Raw:      raw,
		Protocol: "tuic",
		Name:     frag(u),
		Server:   u.Hostname(),
		Port:     atoi(u.Port()),
		UUID:     u.User.Username(),
		Password: pw,
		Params:   u.Query(),
	}
	if p.Name == "" {
		p.Name = p.Server
	}
	return p, nil
}

func parseVmess(raw string) (*Proxy, error) {
	dec, err := b64decode(strings.TrimPrefix(raw, "vmess://"))
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(dec, &m); err != nil {
		return nil, err
	}
	p := &Proxy{
		Raw:      raw,
		Protocol: "vmess",
		Name:     str(m["ps"]),
		Server:   str(m["add"]),
		Port:     atoi(str(m["port"])),
		UUID:     str(m["id"]),
		AlterID:  atoi(str(m["aid"])),
		VMess:    m,
	}
	if p.Name == "" {
		p.Name = p.Server
	}
	return p, nil
}

func parseSS(raw string) (*Proxy, error) {
	body := strings.TrimPrefix(raw, "ss://")
	name := ""
	if i := strings.Index(body, "#"); i >= 0 {
		name, _ = url.QueryUnescape(body[i+1:])
		body = body[:i]
	}
	if i := strings.Index(body, "?"); i >= 0 {
		body = body[:i]
	}
	var method, password, server string
	var port int
	if at := strings.LastIndex(body, "@"); at >= 0 {
		// ss://base64(method:password)@host:port
		userPart := body[:at]
		hostPart := body[at+1:]
		if dec, err := b64decode(userPart); err == nil {
			userPart = string(dec)
		}
		method, password = splitColon(userPart)
		server, port = splitHostPort(hostPart)
	} else {
		// ss://base64(method:password@host:port)
		dec, err := b64decode(body)
		if err != nil {
			return nil, err
		}
		s := string(dec)
		at2 := strings.LastIndex(s, "@")
		if at2 < 0 {
			return nil, fmt.Errorf("bad ss")
		}
		method, password = splitColon(s[:at2])
		server, port = splitHostPort(s[at2+1:])
	}
	if name == "" {
		name = server
	}
	return &Proxy{Raw: raw, Protocol: "ss", Name: name, Server: server, Port: port, Method: method, Password: password}, nil
}

// DecodeLinks returns the raw share-link lines from a subscription blob
// (base64-encoded or plain text).
func DecodeLinks(blob string) []string {
	blob = strings.TrimSpace(blob)
	if !strings.Contains(blob, "://") {
		if dec, err := b64decode(blob); err == nil {
			blob = string(dec)
		}
	}
	var out []string
	for _, line := range strings.FieldsFunc(blob, func(r rune) bool { return r == '\n' || r == '\r' }) {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "://") {
			out = append(out, line)
		}
	}
	return out
}

// NodeKey returns a stable per-node identifier derived from a share link,
// ignoring the #remark (which carries volatile bits like an airport's live
// speed or the user's remaining-traffic suffix). Used so a user can disable
// specific nodes without storing the credential-bearing link a second time.
func NodeKey(link string) string {
	if i := strings.LastIndex(link, "#"); i >= 0 {
		link = link[:i]
	}
	sum := sha1.Sum([]byte(strings.TrimSpace(link)))
	return hex.EncodeToString(sum[:8]) // 16 hex chars
}

// LinkRemark returns the (url-decoded) #fragment remark of a share link.
func LinkRemark(link string) string {
	if i := strings.LastIndex(link, "#"); i >= 0 {
		if s, err := url.QueryUnescape(link[i+1:]); err == nil {
			return s
		}
		return link[i+1:]
	}
	return ""
}

func frag(u *url.URL) string {
	s, err := url.QueryUnescape(u.Fragment)
	if err != nil {
		return u.Fragment
	}
	return s
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", x)
	}
}

func splitColon(s string) (string, string) {
	if i := strings.Index(s, ":"); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

func splitHostPort(s string) (string, int) {
	// net.SplitHostPort handles bracketed IPv6 ([::1]:8388) and strips the
	// brackets; a naive LastIndex(":") would split a bare IPv6 at the wrong colon.
	if host, port, err := net.SplitHostPort(s); err == nil {
		return host, atoi(port)
	}
	// No port (or malformed): strip brackets from a bare IPv6 literal if present.
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		return s[1 : len(s)-1], 0
	}
	return s, 0
}

func (p *Proxy) param(keys ...string) string {
	for _, k := range keys {
		if v := p.Params.Get(k); v != "" {
			return v
		}
	}
	return ""
}
