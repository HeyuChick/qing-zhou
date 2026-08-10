package api

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"qingzhou/internal/store"
)

// Proxy-egress link parsing.
//
// Static-IP proxy vendors hand out credentials in a handful of shapes and none
// of them is a standard. Retyping them into five form fields is where the
// transcription errors come from — a swapped user/password, a port left at the
// form default — and those surface much later as an opaque 407 or a timeout.
// So the panel accepts whatever the vendor actually sent.
//
// Supported, in the order they are tried:
//
//	socks5://user:pass@host:port      scheme decides the type (socks5h, socks,
//	http://user:pass@host:port        http, https → http + TLS); credentials are
//	https://user:pass@host:port       percent-decoded; #fragment names the egress
//	user:pass@host:port               no scheme — type is guessed from the port
//	host:port:user:pass               the common vendor CSV shape
//	user:pass:host:port               the same, reversed (some vendors do this)
//	host:port                         no authentication
//
// What is deliberately NOT done: no network access, no persistence. Parsing
// returns a candidate row for the admin to look at and save through the normal
// endpoint, which is where validation lives. A parser that also saved would be
// a second, divergent write path.

// httpProxyPorts are the ports conventionally served by HTTP proxies. Used only
// to pick a default for the link shapes that carry no scheme; the guess is
// reported to the caller so the UI can tell the admin to confirm it rather than
// presenting it as fact.
var httpProxyPorts = map[int]bool{80: true, 8080: true, 3128: true, 8888: true, 8118: true}

// parsedEgress is one parsed line: the candidate row plus whether the type had
// to be guessed.
type parsedEgress struct {
	Egress      *store.SbEgress
	TypeGuessed bool
}

var errEgressLinkEmpty = errors.New("空行")

// parseEgressLink parses a single link/credential line. The returned egress is
// NOT validated beyond what parsing itself proves (host non-empty, port in
// range) — handleAdminSaveSbEgress remains the single validation authority.
func parseEgressLink(line string) (*parsedEgress, error) {
	s := strings.TrimSpace(line)
	// Vendors often paste from a spreadsheet or a JSON array. The comma comes
	// off first: with the quotes stripped first, `"host:port",` still ends in a
	// comma and its closing quote is no longer at the end to be trimmed, so the
	// quote ends up glued to the password.
	s = strings.TrimSuffix(s, ",")
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "\"'")
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "//") {
		return nil, errEgressLinkEmpty
	}
	if strings.Contains(s, "://") {
		return parseEgressURL(s)
	}
	return parseEgressBare(s)
}

// parseEgressURL handles the scheme-prefixed forms.
func parseEgressURL(s string) (*parsedEgress, error) {
	u, err := url.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("链接格式错误：%v", err)
	}
	e := &store.SbEgress{}
	switch strings.ToLower(u.Scheme) {
	case "socks", "socks5", "socks5h":
		e.Type = "socks"
	case "http":
		e.Type = "http"
	case "https":
		// An https:// proxy URL means the hop to the proxy is itself TLS — what
		// sing-box calls an http outbound with tls.enabled. Note this is about
		// the scheme, not the port: a plaintext proxy listening on 443 (common,
		// it borrows the port to get through firewalls) is http://host:443.
		e.Type, e.TLSEnabled = "http", true
	default:
		return nil, fmt.Errorf("不支持的协议 %q（仅 socks5 / http / https）", u.Scheme)
	}
	host, port, err := splitHostPortStrict(u.Host)
	if err != nil {
		return nil, err
	}
	e.Host, e.Port = host, port
	if u.User != nil {
		e.Username = u.User.Username()
		e.Password, _ = u.User.Password()
	}
	q := u.Query()
	if v := strings.TrimSpace(q.Get("sni")); v != "" && e.TLSEnabled {
		e.SNI = v
	}
	if frag := strings.TrimSpace(u.Fragment); frag != "" {
		e.Name = frag
	}
	// A hostname address with TLS on needs no SNI (the address IS the name);
	// an IP address does, and the vendor's own hostname is the only candidate
	// we have — but we don't have it, so leave it empty and let the form's
	// existing warning ask for it.
	return &parsedEgress{Egress: e}, nil
}

// parseEgressBare handles the scheme-less shapes.
func parseEgressBare(s string) (*parsedEgress, error) {
	e := &store.SbEgress{}
	switch {
	case strings.Contains(s, "@"):
		// user:pass@host:port — split at the LAST @ so a password containing one
		// survives (vendors do generate those).
		at := strings.LastIndex(s, "@")
		cred, addr := s[:at], s[at+1:]
		host, port, err := splitHostPortStrict(addr)
		if err != nil {
			return nil, err
		}
		e.Host, e.Port = host, port
		if i := strings.Index(cred, ":"); i >= 0 {
			e.Username, e.Password = cred[:i], cred[i+1:]
		} else {
			e.Username = cred
		}
	case strings.HasPrefix(s, "["):
		// A bracketed IPv6 literal is full of colons, so it must be resolved as an
		// address before the colon-separated vendor shapes below get a look at it.
		// Those shapes are an IPv4/hostname convention; no vendor emits
		// "[::1]:1080:user:pass", and trying to read one that way lands on "1]".
		host, port, err := splitHostPortStrict(s)
		if err != nil {
			return nil, err
		}
		e.Host, e.Port = host, port
	default:
		parts := strings.Split(s, ":")
		switch {
		case len(parts) == 2:
			host, port, err := splitHostPortStrict(s)
			if err != nil {
				return nil, err
			}
			e.Host, e.Port = host, port
		case len(parts) >= 4:
			// Two vendor conventions with the same separator. Decide by which end
			// holds the port: a bare integer in 1-65535 that the other end can't
			// also claim. host:port:user:pass is the common one, so it is tried
			// first and user:pass:host:port only wins when the leading pair
			// cannot be an address.
			if p, err := strconv.Atoi(parts[1]); err == nil && p > 0 && p <= 65535 {
				e.Host, e.Port = parts[0], p
				e.Username = parts[2]
				e.Password = strings.Join(parts[3:], ":") // password may contain ':'
			} else if p, err := strconv.Atoi(parts[len(parts)-1]); err == nil && p > 0 && p <= 65535 {
				e.Host, e.Port = parts[len(parts)-2], p
				e.Username = parts[0]
				e.Password = strings.Join(parts[1:len(parts)-2], ":")
			} else {
				return nil, errors.New("无法识别：既不是 host:port:user:pass 也不是 user:pass:host:port")
			}
		default:
			return nil, errors.New("无法识别的格式（支持 socks5://… / user:pass@host:port / host:port:user:pass / host:port）")
		}
	}
	if e.Host == "" {
		return nil, errors.New("缺少服务器地址")
	}
	// No scheme was given, so the protocol is a guess. Ports that are HTTP proxy
	// conventions get http; everything else gets socks, which is what static-IP
	// vendors overwhelmingly sell. Flagged either way — picking wrong shows up as
	// an immediate RST, and the admin should be told to check.
	if httpProxyPorts[e.Port] {
		e.Type = "http"
	} else {
		e.Type = "socks"
	}
	return &parsedEgress{Egress: e, TypeGuessed: true}, nil
}

// splitHostPortStrict splits "host:port" (or "[v6]:port") and requires a port in
// range. net.SplitHostPort alone accepts an empty or non-numeric port.
func splitHostPortStrict(hp string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(hp)
	if err != nil {
		return "", 0, fmt.Errorf("地址需为 host:port 形式：%s", hp)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return "", 0, errors.New("缺少服务器地址")
	}
	port, err := strconv.Atoi(strings.TrimSpace(portStr))
	if err != nil || port <= 0 || port > 65535 {
		return "", 0, fmt.Errorf("端口无效：%s", portStr)
	}
	return host, port, nil
}

// egressLinkFallbackName is the name given to a parsed egress that carried none.
// Batch import needs every row to have one (the save endpoint requires it) and
// the address is the only thing that distinguishes them at that point.
func egressLinkFallbackName(e *store.SbEgress) string {
	return net.JoinHostPort(e.Host, strconv.Itoa(e.Port))
}
