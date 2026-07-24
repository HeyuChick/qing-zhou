package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"qingzhou/internal/singbox"
	"qingzhou/internal/subconv"
)

// Relay chaining lets an inbound (线路机/relay) forward its traffic to another
// inbound (落地机/landing) instead of exiting to the internet directly:
// 客户端 → 线路机入站 → [upstream outbound] → 落地机入站 → 互联网.
//
// A relay inbound serves many users but its single upstream outbound presents
// one identity, so per-user accounting stays at the relay entry. That identity
// is a dedicated relay credential derived from the landing inbound's own
// relay_secret; the same secret is used to inject a matching user into the
// landing inbound's users[], so relay and landing always agree without the admin
// hand-configuring a tunnel.

// relayCred derives the deterministic relay credential from a landing inbound's
// relay_secret: a UUID (vless/vmess/tuic) and a password (the rest). Both the
// relay's upstream outbound and the injected landing user derive from the same
// secret, so they always match.
func relayCred(secret string) (uuid, password string) {
	h := sha256.Sum256([]byte("qz-relay:" + secret))
	uuid = formatUUID(h[:16])
	password = hex.EncodeToString(h[16:32])
	return
}

// formatUUID renders 16 bytes as a canonical UUID string.
func formatUUID(b []byte) string {
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// ensureRelaySecret returns the landing inbound's relay_secret, lazily
// generating and persisting a random one the first time a relay needs it.
func (s *Store) ensureRelaySecret(ib *SbInbound) (string, error) {
	if ib.RelaySecret != "" {
		return ib.RelaySecret, nil
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	sec := hex.EncodeToString(raw)
	if _, err := s.db.Exec(`UPDATE sb_inbounds SET relay_secret=? WHERE id=?`, sec, ib.ID); err != nil {
		return "", err
	}
	ib.RelaySecret = sec
	return sec, nil
}

// mergeRelayUser appends the landing inbound's relay user (if this tag is a
// relay target) to its entitled user list, so the relay can authenticate.
func mergeRelayUser(users []singbox.User, landingUsers map[string]singbox.User, tag string) []singbox.User {
	if ru, ok := landingUsers[tag]; ok {
		return append(append([]singbox.User(nil), users...), ru)
	}
	return users
}

// buildRelayWiring computes, for the inbounds being built on one server:
//   - relays: the upstream outbounds + route rules for this server's relay
//     inbounds (those with a non-zero, valid UpstreamInboundID);
//   - landingUsers: the relay user to inject (by inbound tag) into this server's
//     landing inbounds that are targeted by a relay anywhere.
//
// allInbounds spans every server so a relay can target a landing on a different
// machine. Relay inbounds whose upstream is missing/disabled or whose landing
// protocol has no outbound renderer are skipped (their traffic falls through to
// route.final), never failing the whole build.
func (s *Store) buildRelayWiring(serverInbounds, allInbounds []*SbInbound) ([]singbox.Relay, map[string]singbox.User, error) {
	byID := make(map[int64]*SbInbound, len(allInbounds))
	targeted := map[int64]bool{}
	for _, ib := range allInbounds {
		byID[ib.ID] = ib
		if ib.Enabled && ib.UpstreamInboundID != 0 {
			targeted[ib.UpstreamInboundID] = true
		}
	}

	// Relay users to inject into this server's targeted landing inbounds.
	landingUsers := map[string]singbox.User{}
	for _, ib := range serverInbounds {
		if !ib.Enabled || !targeted[ib.ID] {
			continue
		}
		sec, err := s.ensureRelaySecret(ib)
		if err != nil {
			return nil, nil, err
		}
		uuid, pw := relayCred(sec)
		landingUsers[ib.Tag] = singbox.User{Name: fmt.Sprintf("relay_%d", ib.ID), UUID: uuid, Password: pw}
	}

	// Upstream outbounds for this server's relay inbounds, grouped by landing so
	// several relay inbounds pointing at the same landing share one outbound.
	serverCache := map[int64]*Server{}
	tlsCache := map[int64]*SbTls{}
	byLanding := map[int64]*singbox.Relay{}
	var order []int64
	for _, r := range serverInbounds {
		if !r.Enabled || r.UpstreamInboundID == 0 {
			continue
		}
		landing := byID[r.UpstreamInboundID]
		if landing == nil || !landing.Enabled {
			continue // dangling/disabled upstream — traffic falls through to final
		}
		if existing, ok := byLanding[landing.ID]; ok {
			existing.InboundTags = append(existing.InboundTags, r.Tag)
			continue
		}
		ob, err := s.relayOutbound(landing, serverCache, tlsCache)
		if err != nil || ob == nil {
			continue // unsupported landing protocol / bad config — skip this relay
		}
		byLanding[landing.ID] = &singbox.Relay{Outbound: ob, InboundTags: []string{r.Tag}}
		order = append(order, landing.ID)
	}
	relays := make([]singbox.Relay, 0, len(order))
	for _, id := range order {
		relays = append(relays, *byLanding[id])
	}

	// Third-party proxy egresses: inbounds with EgressID exit through a purchased
	// SOCKS5/HTTP proxy (e.g. a static IP). Same wiring shape as a relay — one
	// outbound per egress shared by all its inbounds, plus a route rule. A
	// dangling egress id is skipped (traffic falls through to route.final); the
	// admin API refuses to delete an egress still in use, so that shouldn't occur.
	egCache := map[int64]*SbEgress{}
	byEgress := map[int64]*singbox.Relay{}
	var egOrder []int64
	for _, r := range serverInbounds {
		if !r.Enabled || r.EgressID == 0 {
			continue
		}
		if existing, ok := byEgress[r.EgressID]; ok {
			existing.InboundTags = append(existing.InboundTags, r.Tag)
			continue
		}
		eg, ok := egCache[r.EgressID]
		if !ok {
			eg, _ = s.GetSbEgress(r.EgressID)
			egCache[r.EgressID] = eg
		}
		if eg == nil {
			continue
		}
		byEgress[r.EgressID] = &singbox.Relay{Outbound: egressOutbound(eg), InboundTags: []string{r.Tag}}
		egOrder = append(egOrder, r.EgressID)
	}
	for _, id := range egOrder {
		relays = append(relays, *byEgress[id])
	}
	return relays, landingUsers, nil
}

// egressOutbound renders a proxy egress as a sing-box socks/http outbound. On a
// DecryptFailed egress the password is empty, so traffic fails closed at the
// proxy instead of silently exiting with the wrong (direct) IP.
func egressOutbound(e *SbEgress) map[string]interface{} {
	ob := map[string]interface{}{
		"type":        e.Type,
		"tag":         fmt.Sprintf("egress-%d", e.ID),
		"server":      e.Host,
		"server_port": e.Port,
	}
	if e.Type == "socks" {
		ob["version"] = "5"
	}
	if e.Username != "" {
		ob["username"] = e.Username
	}
	if e.Password != "" || e.DecryptFailed {
		ob["password"] = e.Password
	}
	return ob
}

// relayOutbound builds the sing-box outbound that dials a landing inbound, using
// the derived relay credential. It reconstructs a client LinkParams from the
// landing inbound's server/TLS/options (mirroring BuildSelfBuiltLinks) and
// renders it through the subscription outbound renderer.
func (s *Store) relayOutbound(landing *SbInbound, serverCache map[int64]*Server, tlsCache map[int64]*SbTls) (map[string]interface{}, error) {
	// Dial host: the landing server's own host; a local (server_id 0) landing is
	// reached over loopback on the same machine.
	host := "127.0.0.1"
	if landing.ServerID != 0 {
		sv, ok := serverCache[landing.ServerID]
		if !ok {
			sv, _ = s.GetServer(landing.ServerID)
			serverCache[landing.ServerID] = sv
		}
		if sv != nil && sv.Host != "" {
			host = sv.Host
		}
	}

	secret, err := s.ensureRelaySecret(landing)
	if err != nil {
		return nil, err
	}
	uuid, pw := relayCred(secret)

	var server, client, opts map[string]interface{}
	if landing.TlsID != 0 {
		t, ok := tlsCache[landing.TlsID]
		if !ok {
			t, _ = s.GetSbTls(landing.TlsID)
			tlsCache[landing.TlsID] = t
		}
		if t != nil {
			_ = json.Unmarshal([]byte(t.ServerJSON), &server)
			_ = json.Unmarshal([]byte(t.ClientJSON), &client)
		}
	}
	_ = json.Unmarshal([]byte(landing.Options), &opts)

	lp := singbox.LinkParams{
		Type: landing.Type, Tag: "relay", Host: host, Port: landing.ListenPort,
		UUID: uuid, Password: pw,
		TLS:         landing.TlsID != 0,
		SNI:         mapStr(server, "server_name"),
		Fingerprint: nestedStr(client, "utls", "fingerprint"),
		Insecure:    mapBool(client, "insecure"),
		Congestion:  mapStr(opts, "congestion_control"),
		ZeroRTT:     mapBool(opts, "zero_rtt_handshake"),
		Method:      mapStr(opts, "method"),
		ServerKey:   mapStr(opts, "password"),
		TCPFastOpen: mapBool(opts, "tcp_fast_open"),
		MPTCP:       mapBool(opts, "tcp_multi_path"),
	}
	if obfs, ok := opts["obfs"].(map[string]interface{}); ok {
		lp.Obfs = mapStr(obfs, "type")
		lp.ObfsPassword = mapStr(obfs, "password")
	}
	if tr, ok := opts["transport"].(map[string]interface{}); ok {
		lp.Network = mapStr(tr, "type")
		lp.Path = mapStr(tr, "path")
		lp.ServiceName = mapStr(tr, "service_name")
		if h := mapStr(tr, "host"); h != "" {
			lp.WSHost = h
		} else if hdr, ok := tr["headers"].(map[string]interface{}); ok {
			lp.WSHost = mapStr(hdr, "Host")
		}
		if lp.WSHost == "" && (lp.Network == "ws" || lp.Network == "httpupgrade") {
			lp.WSHost = lp.SNI
		}
		lp.WSMaxEarlyData = mapInt(tr, "max_early_data")
		lp.WSEarlyDataHeader = mapStr(tr, "early_data_header_name")
	}
	if r, ok := server["reality"].(map[string]interface{}); ok {
		lp.PublicKey = nestedStr(client, "reality", "public_key")
		lp.ShortID = firstShortID(r["short_id"])
		if landing.Type == "vless" && mapStr(opts, "flow") != "none" {
			lp.Flow = true
		}
	}
	if alpn, ok := server["alpn"].([]interface{}); ok {
		parts := make([]string, 0, len(alpn))
		for _, a := range alpn {
			if str, ok := a.(string); ok {
				parts = append(parts, str)
			}
		}
		lp.ALPN = strings.Join(parts, ",")
	}

	link := singbox.BuildShareLink(lp)
	if link == "" {
		return nil, fmt.Errorf("relay: cannot build dial link for landing inbound %d (%s)", landing.ID, landing.Tag)
	}
	ob, err := subconv.SingboxOutboundFromLink(link)
	if err != nil {
		return nil, err
	}
	ob["tag"] = fmt.Sprintf("relay-to-%d", landing.ID)
	return ob, nil
}
