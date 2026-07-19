package singbox

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

// LinkParams holds everything needed to render a client share-link for a
// self-built node, so 轻舟 can build subscriptions from its own data instead of
// fetching them from sing-box's sub server.
type LinkParams struct {
	Type     string // vless | tuic | hysteria2
	Tag      string // node remark (becomes the #fragment)
	Host     string // dial address advertised to clients (origin IP)
	Port     int
	UUID     string
	Password string

	SNI         string
	PublicKey   string // reality pbk
	ShortID     string // reality sid
	Fingerprint string // utls fp, default chrome
	ALPN        string // comma-joined, e.g. h3,h2,http/1.1
	Congestion  string // tuic congestion_control
	ZeroRTT     bool   // tuic 0-RTT handshake (must match the inbound to save a RTT)
	Insecure    bool
	Flow        bool // vless reality vision

	// transport (vless/vmess/trojan): Network is ws|grpc|httpupgrade ("" or tcp = none)
	Network     string
	Path        string // ws/httpupgrade path
	WSHost      string // ws/httpupgrade Host header
	ServiceName string // grpc service name

	// ws 0-RTT early data. Both must match the inbound; carried so the client
	// gets the same 1-RTT-saving handshake the server advertises.
	WSMaxEarlyData    int
	WSEarlyDataHeader string

	// shadowsocks
	Method    string // ss method (2022-blake3-*)
	ServerKey string // ss-2022 server PSK (inbound password)

	// hysteria v1 bandwidth (Mbps)
	UpMbps   int
	DownMbps int

	// hysteria2 obfs (salamander). Both must be set on the client link or the
	// handshake fails when the inbound has obfs enabled.
	Obfs         string // obfs type, e.g. "salamander"
	ObfsPassword string

	// TCP dial tuning (TCP-based protocols only). TCP Fast Open and MPTCP each
	// need BOTH ends enabled to do anything, so 轻舟 mirrors the inbound's
	// setting onto the client link.
	TCPFastOpen bool // -> tcp_fast_open / tfo
	MPTCP       bool // -> tcp_multi_path / mptcp

	// Multiplex + TCP Brutal congestion control (vless/vmess/trojan). Multiplex
	// must be enabled on BOTH ends; Brutal is a sub-option that only takes
	// effect when the client also turns it on with per-endpoint bandwidths.
	// BrutalUp/BrutalDown are the CLIENT's uplink/downlink in Mbps (the caller
	// mirrors the server's values across the link).
	Mux        bool
	BrutalUp   int
	BrutalDown int
}

// tuningQuery renders the TCP/multiplex tuning params shared by the URL-style
// TCP protocols (vless/trojan). These are custom query keys understood by
// 轻舟's own subscription renderer; unknown-key-tolerant clients ignore them.
// allowMux gates multiplex: it must be dropped when xtls-rprx-vision flow is
// active (sing-box rejects multiplex together with vision).
func (p LinkParams) tuningQuery(allowMux bool) []string {
	var q []string
	if p.TCPFastOpen {
		q = append(q, "tfo=1")
	}
	if p.MPTCP {
		q = append(q, "mptcp=1")
	}
	if allowMux && p.Mux {
		q = append(q, "mux=1")
		if p.BrutalUp > 0 && p.BrutalDown > 0 {
			q = append(q, "brutal_up="+strconv.Itoa(p.BrutalUp), "brutal_down="+strconv.Itoa(p.BrutalDown))
		}
	}
	return q
}

// transportQuery appends the transport-specific query params for a share link.
func (p LinkParams) transportQuery() []string {
	switch p.Network {
	case "ws":
		q := []string{"type=ws"}
		if p.Path != "" {
			q = append(q, "path="+url.QueryEscape(p.Path))
		}
		if p.WSHost != "" {
			q = append(q, "host="+url.QueryEscape(p.WSHost))
		}
		if p.WSMaxEarlyData > 0 {
			q = append(q, "max_early_data="+strconv.Itoa(p.WSMaxEarlyData))
			if p.WSEarlyDataHeader != "" {
				q = append(q, "early_data_header_name="+url.QueryEscape(p.WSEarlyDataHeader))
			}
		}
		return q
	case "httpupgrade":
		q := []string{"type=httpupgrade"}
		if p.Path != "" {
			q = append(q, "path="+url.QueryEscape(p.Path))
		}
		if p.WSHost != "" {
			q = append(q, "host="+url.QueryEscape(p.WSHost))
		}
		return q
	case "grpc":
		q := []string{"type=grpc"}
		if p.ServiceName != "" {
			q = append(q, "serviceName="+url.QueryEscape(p.ServiceName))
		}
		return q
	default:
		return []string{"type=tcp"}
	}
}

// BuildShareLink renders a vless/tuic/hysteria2 URI matching the format clients
// already use, so existing imports keep working after the cutover.
func BuildShareLink(p LinkParams) string {
	if p.Host == "" || p.Port == 0 {
		return ""
	}
	fp := p.Fingerprint
	if fp == "" {
		fp = "chrome"
	}
	// esc escapes a value interpolated into a query param or URI userinfo. The
	// values are system-generated today (UUIDs / hex secrets), but escaping
	// prevents a stray reserved char (#/?&@) from producing a malformed or
	// ambiguous link.
	esc := url.QueryEscape
	hp := p.Host + ":" + strconv.Itoa(p.Port)
	frag := "#" + url.QueryEscape(p.Tag)

	tcp := p.Network == "" || p.Network == "tcp"
	switch p.Type {
	case "vless":
		q := p.transportQuery()
		visionActive := false
		if p.PublicKey != "" { // Reality
			q = append(q, "security=reality", "pbk="+esc(p.PublicKey), "sid="+esc(p.ShortID), "fp="+esc(fp), "sni="+esc(p.SNI))
			if p.Flow && tcp { // vision only on raw-TCP Reality
				q = append(q, "flow=xtls-rprx-vision")
				visionActive = true
			}
		} else { // plain TLS
			q = append(q, "security=tls", "fp="+esc(fp), "sni="+esc(p.SNI))
			if p.Insecure {
				q = append(q, "allowInsecure=1")
			}
		}
		// VLESS is the only protocol here whose UDP needs an explicit packet
		// encoding; without it each client picks its own default and UDP fails
		// silently while TCP stays healthy (QUIC downloads hang, browsing is
		// fine). Pin xudp so both ends agree.
		q = append(q, "packetEncoding=xudp")
		q = append(q, p.tuningQuery(!visionActive)...) // mux is invalid with vision flow
		return "vless://" + esc(p.UUID) + "@" + hp + "?" + strings.Join(q, "&") + frag
	case "trojan":
		q := p.transportQuery()
		q = append(q, "security=tls", "fp="+esc(fp), "sni="+esc(p.SNI))
		if p.Insecure {
			q = append(q, "allowInsecure=1")
		}
		q = append(q, p.tuningQuery(true)...)
		return "trojan://" + esc(p.Password) + "@" + hp + "?" + strings.Join(q, "&") + frag
	case "tuic":
		q := []string{"security=tls"}
		if p.Insecure {
			q = append(q, "insecure=1")
		}
		q = append(q, "fp="+esc(fp), "sni="+esc(p.SNI))
		if p.ALPN != "" {
			q = append(q, "alpn="+esc(p.ALPN))
		}
		if p.Congestion != "" {
			q = append(q, "congestion_control="+esc(p.Congestion))
		}
		if p.ZeroRTT {
			q = append(q, "zero_rtt=1")
		}
		return "tuic://" + esc(p.UUID) + ":" + esc(p.Password) + "@" + hp + "?" + strings.Join(q, "&") + frag
	case "hysteria2":
		q := []string{"security=tls"}
		if p.Insecure {
			q = append(q, "insecure=1")
		}
		q = append(q, "fp="+esc(fp), "sni="+esc(p.SNI), "fastopen=0")
		// obfs must be advertised to clients, otherwise the handshake fails
		// when the inbound has salamander obfs enabled.
		if p.Obfs != "" {
			q = append(q, "obfs="+esc(p.Obfs))
			if p.ObfsPassword != "" {
				q = append(q, "obfs-password="+url.QueryEscape(p.ObfsPassword))
			}
		}
		return "hysteria2://" + esc(p.Password) + "@" + hp + "?" + strings.Join(q, "&") + frag
	case "vmess":
		m := map[string]interface{}{
			"v": "2", "ps": p.Tag, "add": p.Host, "port": strconv.Itoa(p.Port),
			"id": p.UUID, "aid": "0", "scy": "auto", "type": "none",
			"net": p.Network, "host": p.WSHost, "path": p.Path,
		}
		if m["net"] == "" || m["net"] == nil {
			m["net"] = "tcp"
		}
		if p.Network == "grpc" {
			m["path"] = p.ServiceName
		}
		if p.SNI != "" {
			m["tls"] = "tls"
			m["sni"] = p.SNI
		} else {
			m["tls"] = ""
		}
		// Custom tuning keys (ignored by clients that don't understand them);
		// 轻舟's own Clash/sing-box renderers read them back.
		if p.TCPFastOpen {
			m["tfo"] = "1"
		}
		if p.MPTCP {
			m["mptcp"] = "1"
		}
		if p.Mux {
			m["mux"] = "1"
			if p.BrutalUp > 0 && p.BrutalDown > 0 {
				m["brutal_up"] = strconv.Itoa(p.BrutalUp)
				m["brutal_down"] = strconv.Itoa(p.BrutalDown)
			}
		}
		b, _ := json.Marshal(m)
		return "vmess://" + base64.StdEncoding.EncodeToString(b)
	case "shadowsocks":
		// SIP002: ss://base64url(method:password)@host:port  — for 2022 multi-user
		// password = serverPSK:userPSK.
		userKey := DeriveSSKey(p.Password, p.Method)
		userinfo := base64.RawURLEncoding.EncodeToString([]byte(p.Method + ":" + p.ServerKey + ":" + userKey))
		return "ss://" + userinfo + "@" + hp + frag
	case "anytls":
		q := []string{"security=tls", "fp=" + esc(fp), "sni=" + esc(p.SNI)}
		if p.Insecure {
			q = append(q, "insecure=1")
		}
		return "anytls://" + esc(p.Password) + "@" + hp + "?" + strings.Join(q, "&") + frag
	case "hysteria":
		q := []string{"protocol=udp", "auth=" + esc(p.Password), "peer=" + esc(p.SNI)}
		if p.Insecure {
			q = append(q, "insecure=1")
		}
		if p.UpMbps > 0 {
			q = append(q, "upmbps="+strconv.Itoa(p.UpMbps))
		}
		if p.DownMbps > 0 {
			q = append(q, "downmbps="+strconv.Itoa(p.DownMbps))
		}
		if p.ALPN != "" {
			q = append(q, "alpn="+esc(p.ALPN))
		}
		return "hysteria://" + hp + "?" + strings.Join(q, "&") + frag
	}
	return ""
}
