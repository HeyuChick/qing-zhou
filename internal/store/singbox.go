package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"qingzhou/internal/singbox"
)

// SbTls is a TLS/Reality profile for native sing-box inbounds (B2). ServerJSON
// (the sing-box "tls" block, including the Reality private_key) is stored
// encrypted at rest; it is returned decrypted from these methods.
type SbTls struct {
	ID         int64  `json:"id"`
	ServerID   int64  `json:"server_id"`
	Name       string `json:"name"`
	Mode       string `json:"mode"` // reality | tls
	ServerJSON string `json:"server_json"`
	ClientJSON string `json:"client_json"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
	// DecryptFailed is set when ServerJSON was stored encrypted but could not be
	// decrypted (typically a changed/wrong QZ_SECRET_KEY). The config builder MUST
	// refuse to emit such an inbound rather than downgrade it to a plaintext one.
	DecryptFailed bool `json:"-"`
}

// SbInbound is a native sing-box server inbound owned by 轻舟.
type SbInbound struct {
	ID         int64  `json:"id"`
	ServerID   int64  `json:"server_id"`
	Type       string `json:"type"`
	Tag        string `json:"tag"`
	Listen     string `json:"listen"`
	ListenPort int    `json:"listen_port"`
	TlsID      int64  `json:"tls_id"`
	Options    string `json:"options"` // JSON object of extra inbound fields
	Enabled    bool   `json:"enabled"`
	SortOrder  int    `json:"sort_order"`
	// UpstreamInboundID makes this inbound a relay: instead of exiting to the
	// internet, its traffic is forwarded to the landing inbound with this id
	// (0 = direct exit / landing). See BuildSingboxConfigForServer relay wiring.
	UpstreamInboundID int64 `json:"upstream_inbound_id"`
	// RelaySecret is a landing inbound's own auth secret, generated lazily when a
	// relay first targets it. Both the relay's upstream outbound and the relay
	// user injected into this inbound derive their credential from it.
	RelaySecret string `json:"-"`
	CreatedAt   int64  `json:"created_at"`
	UpdatedAt   int64  `json:"updated_at"`
}

// ---- sb_tls ----

func (s *Store) ListSbTls() ([]*SbTls, error) {
	rows, err := s.db.Query(`SELECT id, server_id, name, mode, server_json, client_json, created_at, updated_at
		FROM sb_tls ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SbTls{}
	for rows.Next() {
		var t SbTls
		if err := rows.Scan(&t.ID, &t.ServerID, &t.Name, &t.Mode, &t.ServerJSON, &t.ClientJSON, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		var ok bool
		t.ServerJSON, ok = s.decryptOK(t.ServerJSON)
		t.DecryptFailed = !ok
		out = append(out, &t)
	}
	return out, rows.Err()
}

func (s *Store) GetSbTls(id int64) (*SbTls, error) {
	var t SbTls
	err := s.db.QueryRow(`SELECT id, server_id, name, mode, server_json, client_json, created_at, updated_at
		FROM sb_tls WHERE id=?`, id).Scan(&t.ID, &t.ServerID, &t.Name, &t.Mode, &t.ServerJSON, &t.ClientJSON, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		// Don't mask a real DB error as "not found" — the config builder would
		// otherwise silently drop this inbound's TLS block and emit it plaintext.
		return nil, err
	}
	var ok bool
	t.ServerJSON, ok = s.decryptOK(t.ServerJSON)
	t.DecryptFailed = !ok
	return &t, nil
}

// SaveSbTls inserts (id==0) or updates a TLS profile. ServerJSON is encrypted.
func (s *Store) SaveSbTls(t *SbTls) (int64, error) {
	now := time.Now().Unix()
	enc := s.encrypt(t.ServerJSON)
	if t.ID == 0 {
		res, err := s.db.Exec(`INSERT INTO sb_tls (server_id, name, mode, server_json, client_json, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?)`, t.ServerID, t.Name, t.Mode, enc, t.ClientJSON, now, now)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	_, err := s.db.Exec(`UPDATE sb_tls SET server_id=?, name=?, mode=?, server_json=?, client_json=?, updated_at=? WHERE id=?`,
		t.ServerID, t.Name, t.Mode, enc, t.ClientJSON, now, t.ID)
	return t.ID, err
}

// ErrInUse is returned when a delete is refused because other rows still
// reference the target. Handlers surface its message to the client.
var ErrInUse = errors.New("仍被引用，无法删除")

func (s *Store) DeleteSbTls(id int64) error {
	// Refuse deletion while an inbound still references this TLS: nulling it out
	// would silently strip encryption from a live inbound (e.g. a VLESS Reality
	// node), which is worse than a clear error.
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM sb_inbounds WHERE tls_id=?`, id).Scan(&n)
	if n > 0 {
		return fmt.Errorf("%w：仍有 %d 个入站在使用此 TLS", ErrInUse, n)
	}
	_, err := s.db.Exec(`DELETE FROM sb_tls WHERE id=?`, id)
	return err
}

// ---- sb_inbounds ----

func (s *Store) ListSbInbounds() ([]*SbInbound, error) {
	rows, err := s.db.Query(`SELECT id, server_id, type, tag, listen, listen_port, tls_id, options, enabled, sort_order, upstream_inbound_id, relay_secret, created_at, updated_at
		FROM sb_inbounds ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SbInbound{}
	for rows.Next() {
		var n SbInbound
		var enabled int
		if err := rows.Scan(&n.ID, &n.ServerID, &n.Type, &n.Tag, &n.Listen, &n.ListenPort, &n.TlsID, &n.Options, &enabled, &n.SortOrder, &n.UpstreamInboundID, &n.RelaySecret, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		n.Enabled = enabled == 1
		out = append(out, &n)
	}
	return out, rows.Err()
}

func (s *Store) GetSbInbound(id int64) (*SbInbound, error) {
	var n SbInbound
	var enabled int
	err := s.db.QueryRow(`SELECT id, server_id, type, tag, listen, listen_port, tls_id, options, enabled, sort_order, upstream_inbound_id, relay_secret, created_at, updated_at
		FROM sb_inbounds WHERE id=?`, id).Scan(&n.ID, &n.ServerID, &n.Type, &n.Tag, &n.Listen, &n.ListenPort, &n.TlsID, &n.Options, &enabled, &n.SortOrder, &n.UpstreamInboundID, &n.RelaySecret, &n.CreatedAt, &n.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	n.Enabled = enabled == 1
	return &n, nil
}

func (s *Store) SaveSbInbound(n *SbInbound) (int64, error) {
	now := time.Now().Unix()
	if n.Options == "" {
		n.Options = "{}"
	}
	if n.Listen == "" {
		n.Listen = "::"
	}
	if n.ID == 0 {
		res, err := s.db.Exec(`INSERT INTO sb_inbounds (server_id, type, tag, listen, listen_port, tls_id, options, enabled, sort_order, upstream_inbound_id, relay_secret, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			n.ServerID, n.Type, n.Tag, n.Listen, n.ListenPort, n.TlsID, n.Options, b2i(n.Enabled), n.SortOrder, n.UpstreamInboundID, n.RelaySecret, now, now)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	// Self-built nodes link to an inbound by its tag (inbound_tag is a copy
	// of the value). If the tag changes, that linkage — and the group/subscription
	// matching built on it — silently breaks. Cascade the rename atomically.
	var oldTag string
	_ = s.db.QueryRow(`SELECT tag FROM sb_inbounds WHERE id=?`, n.ID).Scan(&oldTag)
	tagChanged := oldTag != "" && oldTag != n.Tag

	tx, err := s.db.Begin()
	if err != nil {
		return n.ID, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE sb_inbounds SET server_id=?, type=?, tag=?, listen=?, listen_port=?, tls_id=?, options=?, enabled=?, sort_order=?, upstream_inbound_id=?, relay_secret=?, updated_at=? WHERE id=?`,
		n.ServerID, n.Type, n.Tag, n.Listen, n.ListenPort, n.TlsID, n.Options, b2i(n.Enabled), n.SortOrder, n.UpstreamInboundID, n.RelaySecret, now, n.ID); err != nil {
		return n.ID, err
	}
	if tagChanged {
		// Re-point linked self-built nodes to the new tag, and keep the node's
		// display name in sync when it was the auto-derived default (== old tag);
		// leave custom names alone.
		if _, err := tx.Exec(`UPDATE nodes SET name=? WHERE type='self_built' AND inbound_tag=? AND name=?`, n.Tag, oldTag, oldTag); err != nil {
			return n.ID, err
		}
		if _, err := tx.Exec(`UPDATE nodes SET inbound_tag=? WHERE type='self_built' AND inbound_tag=?`, n.Tag, oldTag); err != nil {
			return n.ID, err
		}
	}
	return n.ID, tx.Commit()
}

func (s *Store) DeleteSbInbound(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// A self-built node is a 1:1 mirror of an inbound (linked by tag). Without
	// its inbound the node is non-functional and produces no subscription link,
	// so remove the mirror node(s) and their group memberships too — otherwise
	// they linger as zombies and silently revive if a same-tag inbound is recreated.
	var tag string
	_ = tx.QueryRow(`SELECT tag FROM sb_inbounds WHERE id=?`, id).Scan(&tag)
	if tag != "" {
		if _, err := tx.Exec(`DELETE FROM node_group_members WHERE node_id IN (SELECT id FROM nodes WHERE type='self_built' AND inbound_tag=?)`, tag); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM nodes WHERE type='self_built' AND inbound_tag=?`, tag); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DELETE FROM sb_inbounds WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ListSbInboundsByServer(serverID int64) ([]*SbInbound, error) {
	rows, err := s.db.Query(`SELECT id, server_id, type, tag, listen, listen_port, tls_id, options, enabled, sort_order, upstream_inbound_id, relay_secret, created_at, updated_at
		FROM sb_inbounds WHERE server_id=? ORDER BY sort_order, id`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*SbInbound{}
	for rows.Next() {
		var n SbInbound
		var enabled int
		if err := rows.Scan(&n.ID, &n.ServerID, &n.Type, &n.Tag, &n.Listen, &n.ListenPort, &n.TlsID, &n.Options, &enabled, &n.SortOrder, &n.UpstreamInboundID, &n.RelaySecret, &n.CreatedAt, &n.UpdatedAt); err != nil {
			return nil, err
		}
		n.Enabled = enabled == 1
		out = append(out, &n)
	}
	return out, rows.Err()
}

// transportBits is the set of L4 protocols an inbound binds on its port.
type transportBits uint8

const (
	transportTCP transportBits = 1 << iota
	transportUDP
)

// inboundTransports reports which L4 protocols an inbound type binds. Sharing a
// port is only a conflict when two inbounds want the same L4 — e.g. hysteria2
// (QUIC/UDP) and vless (TCP) both on 443 is a legitimate, common pairing.
func inboundTransports(typ, options string) transportBits {
	switch typ {
	case "tuic", "hysteria2", "hysteria":
		return transportUDP // QUIC-based: UDP only
	case "vless", "vmess", "trojan", "anytls", "mixed":
		return transportTCP
	case "shadowsocks":
		// sing-box's shadowsocks inbound serves both unless network narrows it.
		var opts map[string]interface{}
		if options != "" {
			_ = json.Unmarshal([]byte(options), &opts)
		}
		var bits transportBits
		if n, _ := opts["network"].(string); n != "" {
			for _, part := range strings.Split(n, ",") {
				switch strings.TrimSpace(part) {
				case "tcp":
					bits |= transportTCP
				case "udp":
					bits |= transportUDP
				}
			}
		}
		if bits == 0 {
			bits = transportTCP | transportUDP
		}
		return bits
	default:
		// Unknown/new type: assume it may bind both, so a clash is reported
		// rather than silently allowed — a double bind fails at sing-box start,
		// which is worse than a false rejection the admin can see and reason about.
		return transportTCP | transportUDP
	}
}

// listenIsAny reports whether a listen address is a wildcard (binds every
// interface, so it overlaps any other address on the same port).
func listenIsAny(s string) bool {
	return s == "" || s == "::" || s == "::0" || s == "0.0.0.0"
}

func listenOverlaps(a, b string) bool {
	if listenIsAny(a) || listenIsAny(b) {
		return true
	}
	return a == b
}

// SbInboundPortConflict 检测入站的 listen+端口是否与同服务器的其他入站真正冲突。
// 仅当「监听地址重叠」且「L4 协议(TCP/UDP)有交集」时才算冲突 —— 同端口的
// hysteria2(UDP)+ vless(TCP)是合法搭配,不应拦截。n.ID 非 0 时排除自身。
// 返回 (conflict, existingTag, error)。
func (s *Store) SbInboundPortConflict(n *SbInbound) (bool, string, error) {
	rows, err := s.db.Query(`SELECT tag, type, listen, options FROM sb_inbounds
		WHERE server_id=? AND listen_port=? AND id!=?`, n.ServerID, n.ListenPort, n.ID)
	if err != nil {
		return false, "", err
	}
	defer rows.Close()
	newBits := inboundTransports(n.Type, n.Options)
	for rows.Next() {
		var tag, typ, listen, options string
		if err := rows.Scan(&tag, &typ, &listen, &options); err != nil {
			return false, "", err
		}
		if !listenOverlaps(listen, n.Listen) {
			continue
		}
		if inboundTransports(typ, options)&newBits == 0 {
			continue // different L4 — both can bind this port
		}
		return true, tag, nil
	}
	return false, "", rows.Err()
}

// SetUserPlan sets a user's current plan (for entitlement); planID 0 clears it.
func (s *Store) SetUserPlan(userID, planID int64) error {
	if planID == 0 {
		_, err := s.db.Exec(`UPDATE users SET current_plan_id=NULL, updated_at=? WHERE id=?`, time.Now().Unix(), userID)
		return err
	}
	_, err := s.db.Exec(`UPDATE users SET current_plan_id=?, updated_at=? WHERE id=?`, planID, time.Now().Unix(), userID)
	return err
}

// AddUsageByClientName accumulates a traffic delta from the sing-box v2ray stats
// poll. Identities are now per-bucket (plan / pool), so the delta is routed to
// the matching bucket, which also mirrors it onto the owning user (aggregate
// counters + last_online + the per-user time-series). Counters in sing-box reset
// each poll, so this is called with deltas.
func (s *Store) AddUsageByClientName(name string, up, down int64) error {
	return s.AddBucketUsage(name, up, down)
}

// ---- config assembly ----

// BuildSingboxConfig assembles a full sing-box config from the enabled inbounds
// (each merged with its TLS/Reality block and extra options) plus the users
// entitled to each inbound tag (usersByTag, computed by the caller's
// entitlement logic). base is the log/dns/route/outbounds template JSON.
func (s *Store) BuildSingboxConfig(base, v2rayListen string, usersByTag map[string][]singbox.User) ([]byte, error) {
	inbounds, err := s.ListSbInboundsByServer(0)
	if err != nil {
		return nil, err
	}
	allInbounds, err := s.ListSbInbounds()
	if err != nil {
		return nil, err
	}
	relays, landingUsers, err := s.buildRelayWiring(inbounds, allInbounds)
	if err != nil {
		return nil, err
	}
	tlsCache := map[int64]*SbTls{}
	var ibs []singbox.Inbound
	for _, ib := range inbounds {
		if !ib.Enabled {
			continue
		}
		baseMap := map[string]interface{}{
			"type":        ib.Type,
			"tag":         ib.Tag,
			"listen":      ib.Listen,
			"listen_port": ib.ListenPort,
		}
		if ib.Options != "" && ib.Options != "{}" {
			var opts map[string]interface{}
			if err := json.Unmarshal([]byte(ib.Options), &opts); err == nil {
				for k, v := range opts {
					baseMap[k] = v
				}
			}
		}
		if ib.TlsID != 0 {
			tls := tlsCache[ib.TlsID]
			if tls == nil {
				tls, _ = s.GetSbTls(ib.TlsID)
				tlsCache[ib.TlsID] = tls
			}
			if tls != nil {
				if tls.DecryptFailed {
					// A wrong/changed QZ_SECRET_KEY left this TLS profile
					// undecryptable. Refuse to build rather than emit the inbound
					// without its TLS block, which would downgrade a Reality/TLS
					// node to plaintext and leak all its traffic in the clear.
					return nil, fmt.Errorf("入站 %s 的 TLS 配置(id=%d)无法解密，已拒绝生成配置以避免降级为明文入站——请确认 QZ_SECRET_KEY 与加密时一致", ib.Tag, ib.TlsID)
				}
				if tls.ServerJSON != "" {
					var tj map[string]interface{}
					if err := json.Unmarshal([]byte(tls.ServerJSON), &tj); err == nil {
						baseMap["tls"] = tj
					}
				}
			}
		}
		ibs = append(ibs, singbox.Inbound{Type: ib.Type, Base: baseMap, Users: mergeRelayUser(usersByTag[ib.Tag], landingUsers, ib.Tag)})
	}
	return singbox.GenerateConfigWithRelays([]byte(base), ibs, v2rayListen, relays)
}

// BuildSingboxConfigForServer is like BuildSingboxConfig but filters inbounds
// to those belonging to the given server. serverID 0 means "no server" (legacy).
func (s *Store) BuildSingboxConfigForServer(serverID int64, base, v2rayListen string, usersByTag map[string][]singbox.User) ([]byte, error) {
	var inbounds []*SbInbound
	var err error
	if serverID == 0 {
		inbounds, err = s.ListSbInbounds()
	} else {
		inbounds, err = s.ListSbInboundsByServer(serverID)
	}
	if err != nil {
		return nil, err
	}
	allInbounds, err := s.ListSbInbounds()
	if err != nil {
		return nil, err
	}
	relays, landingUsers, err := s.buildRelayWiring(inbounds, allInbounds)
	if err != nil {
		return nil, err
	}
	tlsCache := map[int64]*SbTls{}
	var ibs []singbox.Inbound
	for _, ib := range inbounds {
		if !ib.Enabled {
			continue
		}
		baseMap := map[string]interface{}{
			"type":        ib.Type,
			"tag":         ib.Tag,
			"listen":      ib.Listen,
			"listen_port": ib.ListenPort,
		}
		if ib.Options != "" && ib.Options != "{}" {
			var opts map[string]interface{}
			if err := json.Unmarshal([]byte(ib.Options), &opts); err == nil {
				for k, v := range opts {
					baseMap[k] = v
				}
			}
		}
		if ib.TlsID != 0 {
			tls := tlsCache[ib.TlsID]
			if tls == nil {
				tls, _ = s.GetSbTls(ib.TlsID)
				tlsCache[ib.TlsID] = tls
			}
			if tls != nil {
				if tls.DecryptFailed {
					// A wrong/changed QZ_SECRET_KEY left this TLS profile
					// undecryptable. Refuse to build rather than emit the inbound
					// without its TLS block, which would downgrade a Reality/TLS
					// node to plaintext and leak all its traffic in the clear.
					return nil, fmt.Errorf("入站 %s 的 TLS 配置(id=%d)无法解密，已拒绝生成配置以避免降级为明文入站——请确认 QZ_SECRET_KEY 与加密时一致", ib.Tag, ib.TlsID)
				}
				if tls.ServerJSON != "" {
					var tj map[string]interface{}
					if err := json.Unmarshal([]byte(tls.ServerJSON), &tj); err == nil {
						baseMap["tls"] = tj
					}
				}
			}
		}
		ibs = append(ibs, singbox.Inbound{Type: ib.Type, Base: baseMap, Users: mergeRelayUser(usersByTag[ib.Tag], landingUsers, ib.Tag)})
	}
	// Remote servers may not have v2ray_api compiled in; pass empty to skip.
	if serverID == 0 {
		return singbox.GenerateConfigWithRelays([]byte(base), ibs, v2rayListen, relays)
	}
	return singbox.GenerateConfigWithRelays([]byte(base), ibs, "", relays)
}

// SelfBuiltLink is one generated share-link plus the inbound tag it came from.
// The tag is carried alongside rather than read back out of the link's remark,
// because the remark shows the node's admin-configured display name.
type SelfBuiltLink struct {
	Tag  string // inbound tag — the group filter's join key
	Link string
}

// BuildSelfBuiltLinks generates client share-links for every enabled native
// inbound using the user's own credentials — replacing the sing-box sub fetch so
// subscriptions survive the cutover. host is the dial address advertised to
// clients (node_host_override / origin IP). Each link's remark is the node's
// display name from the 节点 page, falling back to the inbound tag when no node
// is bound to it. Each link uses the credentials of the bucket that owns the
// inbound (see UserOwnedInbounds), so a
// user with an active plan bucket gets links even if their legacy users.*
// identity is empty — e.g. an admin account, which is never provisioned a
// client_uuid. Returns nil only when no address is configured or no bucket
// owns any inbound.
func (s *Store) BuildSelfBuiltLinks(u *User, host string) []SelfBuiltLink {
	if host == "" {
		return nil // no advertised address configured
	}
	inbounds, err := s.ListSbInbounds()
	if err != nil {
		return nil
	}
	// Cache server and TLS lookups so we don't query per-inbound.
	serverCache := make(map[int64]*Server)
	tlsCache := make(map[int64]*SbTls)
	getTls := func(id int64) *SbTls {
		if t, ok := tlsCache[id]; ok {
			return t
		}
		t, _ := s.GetSbTls(id)
		tlsCache[id] = t // cache nil too (negative cache)
		return t
	}
	// Each self-built node is owned by one of the user's buckets; the link uses
	// that bucket's credentials and shows its own remaining quota/expiry. A node
	// with no active owning bucket is omitted (no access).
	owners, _ := s.UserOwnedInbounds(u.ID, time.Now().Unix())
	nodeNames, _ := s.SelfBuiltNodeNames()

	var out []SelfBuiltLink
	for _, ib := range inbounds {
		if !ib.Enabled {
			continue
		}
		owner := owners[ib.Tag]
		if owner == nil {
			continue // no active bucket grants this node
		}
		// Use the server's own host for remote nodes instead of the
		// global host which only applies to the local server.
		nodeHost := host
		if ib.ServerID != 0 {
			if sv, ok := serverCache[ib.ServerID]; ok {
				if sv != nil && sv.Host != "" {
					nodeHost = sv.Host
				}
			} else if sv, _ := s.GetServer(ib.ServerID); sv != nil {
				serverCache[ib.ServerID] = sv
				if sv.Host != "" {
					nodeHost = sv.Host
				}
			} else {
				serverCache[ib.ServerID] = nil // negative cache
			}
		}
		var server, client, opts map[string]interface{}
		if ib.TlsID != 0 {
			if t := getTls(ib.TlsID); t != nil {
				_ = json.Unmarshal([]byte(t.ServerJSON), &server)
				_ = json.Unmarshal([]byte(t.ClientJSON), &client)
			}
		}
		_ = json.Unmarshal([]byte(ib.Options), &opts)

		// Remark = the node's display name from the 节点 page; the raw inbound tag
		// is only a fallback for an inbound no node is bound to.
		remark := ib.Tag
		if n := nodeNames[ib.Tag]; n != "" {
			remark = n
		}
		p := singbox.LinkParams{
			Type: ib.Type, Tag: remark + subInfoSuffixBucket(owner), Host: nodeHost, Port: ib.ListenPort,
			UUID: owner.ClientUUID, Password: owner.ClientSecret,
			SNI:         mapStr(server, "server_name"),
			Fingerprint: nestedStr(client, "utls", "fingerprint"),
			Insecure:    mapBool(client, "insecure"),
			Congestion:  mapStr(opts, "congestion_control"),
			ZeroRTT:     mapBool(opts, "zero_rtt_handshake"), // tuic 0-RTT
			Method:      mapStr(opts, "method"),              // shadowsocks
			ServerKey:   mapStr(opts, "password"), // shadowsocks-2022 server PSK
			UpMbps:      mapInt(opts, "up_mbps"),  // hysteria v1
			DownMbps:    mapInt(opts, "down_mbps"),
			TCPFastOpen: mapBool(opts, "tcp_fast_open"),
			MPTCP:       mapBool(opts, "tcp_multi_path"),
		}
		// Multiplex + Brutal (vless/vmess/trojan): both are opt-in on the client,
		// so mirror the inbound's setting onto the link or Brutal does nothing.
		if mx, ok := opts["multiplex"].(map[string]interface{}); ok && mapBool(mx, "enabled") {
			p.Mux = true
			if br, ok := mx["brutal"].(map[string]interface{}); ok && mapBool(br, "enabled") {
				// Brutal bandwidths are per-endpoint and mirror across the link: the
				// server's uplink (up_mbps = what clients download) is the client's
				// downlink, and the server's downlink is the client's uplink.
				p.BrutalUp = mapInt(br, "down_mbps")
				p.BrutalDown = mapInt(br, "up_mbps")
			}
		}
		// hysteria2 salamander obfs lives in options as {"obfs":{"type","password"}}.
		if obfs, ok := opts["obfs"].(map[string]interface{}); ok {
			p.Obfs = mapStr(obfs, "type")
			p.ObfsPassword = mapStr(obfs, "password")
		}
		// transport (ws/grpc/httpupgrade) for vless/vmess/trojan
		if tr, ok := opts["transport"].(map[string]interface{}); ok {
			p.Network = mapStr(tr, "type")
			p.Path = mapStr(tr, "path")
			p.ServiceName = mapStr(tr, "service_name")
			p.WSMaxEarlyData = mapInt(tr, "max_early_data")
			p.WSEarlyDataHeader = mapStr(tr, "early_data_header_name")
			if h := mapStr(tr, "host"); h != "" {
				p.WSHost = h
			} else if hdr, ok := tr["headers"].(map[string]interface{}); ok {
				p.WSHost = mapStr(hdr, "Host")
			}
			if p.WSHost == "" && (p.Network == "ws" || p.Network == "httpupgrade") {
				p.WSHost = p.SNI // CDN host defaults to the TLS SNI
			}
		}
		if r, ok := server["reality"].(map[string]interface{}); ok {
			p.PublicKey = nestedStr(client, "reality", "public_key")
			p.ShortID = firstShortID(r["short_id"])
			// VLESS flow: 默认 vision，但 options.flow="none" 时关闭
			if ib.Type == "vless" && mapStr(opts, "flow") != "none" {
				p.Flow = true
			}
		}
		if alpn, ok := server["alpn"].([]interface{}); ok {
			parts := make([]string, 0, len(alpn))
			for _, a := range alpn {
				if str, ok := a.(string); ok {
					parts = append(parts, str)
				}
			}
			p.ALPN = strings.Join(parts, ",")
		}
		if link := singbox.BuildShareLink(p); link != "" {
			out = append(out, SelfBuiltLink{Tag: ib.Tag, Link: link})
		}
	}
	return out
}

// UserProxy is one mixed (HTTP/SOCKS5) inbound's connection info for a user,
// surfaced as copyable credentials to paste into plain-proxy fields (1Panel /
// Docker / git http.proxy, ...). Mixed inbounds are deliberately excluded from
// the Clash/sing-box subscription (BuildShareLink returns "" for them), so this
// is how a user retrieves them instead.
type UserProxy struct {
	Tag       string `json:"tag"`
	BucketID  int64  `json:"bucket_id"` // owning bucket — target of a credential edit
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Username  string `json:"username"`
	Password  string `json:"password"`
	TLS       bool   `json:"tls"`        // true → HTTPS proxy; false → plain HTTP/SOCKS5
	ExpiresAt int64  `json:"expires_at"` // 0 = permanent
	Expired   bool   `json:"expired"`    // credential past its expiry (won't authenticate)
	Custom    bool   `json:"custom"`     // true → user has set a custom username/password
}

// BuildUserProxies returns the mixed-inbound proxy credentials the user is
// entitled to. It mirrors BuildSelfBuiltLinks' ownership + per-server host
// override so a node hosted on a remote server advertises that server's host.
func (s *Store) BuildUserProxies(u *User, host string) []UserProxy {
	if host == "" {
		return nil
	}
	inbounds, err := s.ListSbInbounds()
	if err != nil {
		return nil
	}
	serverCache := make(map[int64]*Server)
	now := time.Now().Unix()
	owners, _ := s.UserOwnedInbounds(u.ID, now)
	var out []UserProxy
	for _, ib := range inbounds {
		if !ib.Enabled || ib.Type != "mixed" {
			continue
		}
		owner := owners[ib.Tag]
		if owner == nil {
			continue // no active bucket grants this node
		}
		nodeHost := host
		if ib.ServerID != 0 {
			if sv, ok := serverCache[ib.ServerID]; ok {
				if sv != nil && sv.Host != "" {
					nodeHost = sv.Host
				}
			} else if sv, _ := s.GetServer(ib.ServerID); sv != nil {
				serverCache[ib.ServerID] = sv
				if sv.Host != "" {
					nodeHost = sv.Host
				}
			} else {
				serverCache[ib.ServerID] = nil // negative cache
			}
		}
		out = append(out, UserProxy{
			Tag:       ib.Tag,
			BucketID:  owner.ID,
			Host:      nodeHost,
			Port:      ib.ListenPort,
			Username:  owner.ProxyName(),
			Password:  owner.ProxySecret(),
			TLS:       ib.TlsID != 0,
			ExpiresAt: owner.ProxyExpiresAt,
			Expired:   owner.ProxyExpiresAt != 0 && owner.ProxyExpiresAt <= now,
			Custom:    owner.ProxyUsername != "",
		})
	}
	return out
}

// subInfoSuffixBucket appends a node's owning plan/pool remaining traffic + days
// to its remark (e.g. " 208.67GB📊 58Days⏳"), so per-plan nodes show per-plan
// info — matching what sing-box's sub server used to show for the whole account.
func subInfoSuffixBucket(b *Bucket) string {
	var traffic, expiry string
	if b.TrafficLimit > 0 {
		rem := b.TrafficLimit - b.Used()
		if rem < 0 {
			rem = 0
		}
		traffic = fmt.Sprintf("%.2fGB📊", float64(rem)/(1<<30))
	} else {
		traffic = "不限📊"
	}
	if b.ExpiryAt > 0 {
		days := (b.ExpiryAt - time.Now().Unix()) / 86400
		if days < 0 {
			days = 0
		}
		expiry = fmt.Sprintf("%dDays⏳", days)
	} else {
		expiry = "永久⏳"
	}
	return " " + traffic + " " + expiry
}

func mapStr(m map[string]interface{}, k string) string {
	if m == nil {
		return ""
	}
	s, _ := m[k].(string)
	return s
}
func mapInt(m map[string]interface{}, k string) int {
	if m == nil {
		return 0
	}
	if f, ok := m[k].(float64); ok {
		return int(f)
	}
	return 0
}
func mapBool(m map[string]interface{}, k string) bool {
	if m == nil {
		return false
	}
	b, _ := m[k].(bool)
	return b
}
func nestedStr(m map[string]interface{}, k1, k2 string) string {
	if m == nil {
		return ""
	}
	if inner, ok := m[k1].(map[string]interface{}); ok {
		s, _ := inner[k2].(string)
		return s
	}
	return ""
}
func firstShortID(v interface{}) string {
	arr, ok := v.([]interface{})
	if !ok {
		return ""
	}
	for _, e := range arr {
		if s, ok := e.(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// BuildUsersByTag computes which identity belongs in each enabled inbound, with
// per-bucket enforcement: each self-built node is owned by exactly one of the
// user's *active* buckets (the soonest-expiring plan that covers it, else the
// traffic pool), so an exhausted/expired plan only drops its own nodes while the
// user's other plans keep working. The owning bucket's identity (its own stats
// key) goes into the inbound, giving sing-box per-bucket traffic accounting.
// Banned users are excluded; admins holding an active plan are provisioned like
// any other user (so an admin account can also be used as a subscription). With
// no node groups configured at all it falls back to "every user's first active
// bucket in every inbound" (zero-config).
func (s *Store) BuildUsersByTag(now int64) (map[string][]singbox.User, error) {
	inbounds, err := s.ListSbInbounds()
	if err != nil {
		return nil, err
	}
	groupCount, _ := s.GroupCount()
	freeGroup, _ := s.GetSettingInt64("free_group_id", 0)

	// Load every bucket whose owner is an eligible (non-banned) user. Admins are
	// included: an admin who buys a plan gets a normal metered bucket and should
	// be usable as a subscription just like any other user. Only active buckets
	// (orderBuckets below) end up provisioned, so plan-less admins add nothing.
	rows, err := s.db.Query(`SELECT ` + bucketCols + ` FROM user_plans
		WHERE user_id IN (SELECT id FROM users WHERE status!='banned')
		ORDER BY user_id, id`)
	if err != nil {
		return nil, err
	}
	byUser := map[int64][]*Bucket{}
	for rows.Next() {
		b, err := scanBucket(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		byUser[b.UserID] = append(byUser[b.UserID], b)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	planGroupsCache := map[int64][]int64{}
	planGroups := func(pkgID int64) []int64 {
		if g, ok := planGroupsCache[pkgID]; ok {
			return g
		}
		g, _ := s.PlanGroupIDs(pkgID)
		planGroupsCache[pkgID] = g
		return g
	}

	ordered := map[int64][]ownedBucket{}
	for uid, bs := range byUser {
		if ord := orderBuckets(bs, now, freeGroup, planGroups); len(ord) > 0 {
			ordered[uid] = ord
		}
	}

	out := map[string][]singbox.User{}
	for _, ib := range inbounds {
		if !ib.Enabled {
			continue
		}
		var ibGroups []int64
		if groupCount > 0 {
			ibGroups, _ = s.SelfBuiltNodeGroupIDs(ib.Tag)
		}
		for _, ord := range ordered {
			b := pickOwner(ord, ibGroups, groupCount)
			if b == nil {
				continue
			}
			// A mixed (HTTP/SOCKS5) inbound authenticates with the bucket's custom
			// proxy credential (username/password), which has its own optional expiry
			// separate from the plan. Skip a bucket whose proxy credential has expired
			// so its user drops out of the mixed inbound (and GenerateConfig then omits
			// a now-userless mixed inbound rather than exposing an open proxy).
			if ib.Type == "mixed" {
				if !b.ProxyActive(now) {
					continue
				}
				out[ib.Tag] = append(out[ib.Tag], singbox.User{Name: b.ProxyName(), Password: b.ProxySecret()})
			} else {
				out[ib.Tag] = append(out[ib.Tag], singbox.User{Name: b.ClientName, UUID: b.ClientUUID, Password: b.ClientSecret})
			}
		}
	}
	return out, nil
}

// ownedBucket is one active bucket plus the node groups it covers.
type ownedBucket struct {
	b      *Bucket
	groups map[int64]bool
}

// orderBuckets returns a user's ACTIVE buckets in ownership-priority order:
// plans by soonest expiry (then id), then the traffic pool last. The pool always
// covers the free group (free/unmetered) and, when it has paid balance, the
// union of the user's plan groups as a fallback. planGroups should be a cached
// PlanGroupIDs lookup.
func orderBuckets(bs []*Bucket, now, freeGroup int64, planGroups func(int64) []int64) []ownedBucket {
	allPlanGroups := map[int64]bool{}
	var plans []*Bucket
	var pool *Bucket
	for _, b := range bs {
		if b.Kind == "pool" {
			pool = b
			continue
		}
		plans = append(plans, b)
		for _, g := range planGroups(b.PackageID) {
			allPlanGroups[g] = true
		}
	}
	sort.Slice(plans, func(i, j int) bool {
		ei, ej := plans[i].ExpiryAt, plans[j].ExpiryAt
		if ei == 0 {
			ei = 1<<63 - 1 // never-expires sorts after dated plans
		}
		if ej == 0 {
			ej = 1<<63 - 1
		}
		if ei != ej {
			return ei < ej
		}
		return plans[i].ID < plans[j].ID
	})
	var ord []ownedBucket
	for _, b := range plans {
		if !b.Active(now) {
			continue
		}
		gs := map[int64]bool{}
		if b.PackageID == 0 {
			// Admin manual-grant bucket (no package of its own): scope it like the
			// pool — the free group plus the union of the user's plan groups — so a
			// comped allowance works on whatever nodes the user can already reach.
			if freeGroup > 0 {
				gs[freeGroup] = true
			}
			for g := range allPlanGroups {
				gs[g] = true
			}
		} else {
			for _, g := range planGroups(b.PackageID) {
				gs[g] = true
			}
		}
		ord = append(ord, ownedBucket{b: b, groups: gs})
	}
	if pool != nil {
		if freeGroup > 0 {
			ord = append(ord, ownedBucket{b: pool, groups: map[int64]bool{freeGroup: true}})
		}
		if pool.Active(now) && len(allPlanGroups) > 0 {
			gs := map[int64]bool{}
			for g := range allPlanGroups {
				gs[g] = true
			}
			ord = append(ord, ownedBucket{b: pool, groups: gs})
		}
	}
	return ord
}

// pickOwner returns the highest-priority active bucket that covers the inbound's
// node groups, or nil. With no groups configured (zero-config) the first active
// bucket owns every inbound.
func pickOwner(ord []ownedBucket, ibGroups []int64, groupCount int) *Bucket {
	if len(ord) == 0 {
		return nil
	}
	if groupCount == 0 {
		return ord[0].b
	}
	for _, ob := range ord {
		for _, g := range ibGroups {
			if ob.groups[g] {
				return ob.b
			}
		}
	}
	return nil
}

// UserOwnedInbounds maps each enabled self-built inbound tag to the bucket that
// currently owns it for this user (its credentials + remaining quota drive the
// subscription link), or omits it when the user has no active bucket covering
// it. Mirrors BuildUsersByTag's assignment for one user.
func (s *Store) UserOwnedInbounds(userID, now int64) (map[string]*Bucket, error) {
	buckets, err := s.ListBuckets(userID)
	if err != nil {
		return nil, err
	}
	freeGroup, _ := s.GetSettingInt64("free_group_id", 0)
	groupCount, _ := s.GroupCount()
	planGroupsCache := map[int64][]int64{}
	ord := orderBuckets(buckets, now, freeGroup, func(pkgID int64) []int64 {
		if g, ok := planGroupsCache[pkgID]; ok {
			return g
		}
		g, _ := s.PlanGroupIDs(pkgID)
		planGroupsCache[pkgID] = g
		return g
	})
	inbounds, err := s.ListSbInbounds()
	if err != nil {
		return nil, err
	}
	out := map[string]*Bucket{}
	for _, ib := range inbounds {
		if !ib.Enabled {
			continue
		}
		var ibGroups []int64
		if groupCount > 0 {
			ibGroups, _ = s.SelfBuiltNodeGroupIDs(ib.Tag)
		}
		if b := pickOwner(ord, ibGroups, groupCount); b != nil {
			out[ib.Tag] = b
		}
	}
	return out, nil
}
