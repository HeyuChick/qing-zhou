package store

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"qingzhou/internal/singbox"
)

const RelayUserMeteringSetting = "relay_user_metering"

type relayMeteringEdge struct {
	from  *SbInbound
	to    *SbInbound
	route int64
}

// RelayMeteringUser is an immutable user-to-physical-link generation. Credentials
// are only for managed proxy hops and never replace a customer's credentials.
type RelayMeteringUser struct {
	ID               int64  `json:"id"`
	LinkID           int64  `json:"link_id"`
	UserID           int64  `json:"user_id"`
	Generation       int    `json:"generation"`
	IdentityName     string `json:"-"`
	Credential       string `json:"-"`
	Enabled          bool   `json:"enabled"`
	State            string `json:"state"`
	CreatedAt        int64  `json:"created_at"`
	AcceptedAt       int64  `json:"accepted_at"`
	ActivatedAt      int64  `json:"activated_at"`
	SourceNames      string `json:"-"` // current desired auth_user names; never credentials
	SourceAuthHashes string `json:"-"` // keyed digests of the exact source authentication objects
}

const relayMeteringUserCols = `id,link_id,user_id,generation,identity_name,credential,enabled,state,created_at,accepted_at,activated_at,source_names,source_auth_hashes`

func (s *Store) RelayUserMeteringEnabled() bool {
	value, err := s.GetSetting(RelayUserMeteringSetting)
	return err == nil && value == "true" && s.RelayMeteringEnabled()
}

func supportedUserMeteringEdge(from, to *SbInbound) error {
	// Every supported listener must expose its authenticated user to the core's
	// router and v2ray statistics. The managed Shadowsocks implementation is
	// currently restricted to the two AES SS2022 methods it renders and tests.
	if from == nil || to == nil {
		return fmt.Errorf("逐用户机器观测的入口或落地不存在，保持原配置")
	}
	if from.Type != "mixed" {
		if err := supportedMeteringLanding(from); err != nil {
			return fmt.Errorf("逐用户机器观测入口不可用：%w", err)
		}
	}
	if err := supportedMeteringLanding(to); err != nil {
		return fmt.Errorf("逐用户机器观测落地不可用：%w", err)
	}
	return nil
}

// Activation preflight shares the settings transaction. An unsupported existing
// topology must not save an enabled switch and then block all future rebuilds.
func (s *Store) validateRelayUserMeteringTopology(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT i.id,i.server_id,i.type,i.tag,i.upstream_inbound_id FROM sb_inbounds i WHERE i.enabled=1 AND i.upstream_inbound_id<>0
 UNION ALL SELECT i.id,i.server_id,i.type,i.tag,n.route_upstream_inbound_id FROM nodes n JOIN sb_inbounds i ON i.tag=n.inbound_tag WHERE n.enabled=1 AND n.type='self_built' AND n.route_upstream_broken=0 AND n.route_upstream_inbound_id<>0 AND i.enabled=1`)
	if err != nil {
		return err
	}
	type route struct {
		from     SbInbound
		targetID int64
	}
	var routes []route
	for rows.Next() {
		var edge route
		if err = rows.Scan(&edge.from.ID, &edge.from.ServerID, &edge.from.Type, &edge.from.Tag, &edge.targetID); err != nil {
			rows.Close()
			return err
		}
		routes = append(routes, edge)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if err = rows.Err(); err != nil {
		return err
	}
	graph := map[int64][]int64{}
	for _, edge := range routes {
		target, err := meteringInboundWith(tx, edge.targetID)
		if err != nil {
			return err
		}
		if target == nil || !target.Enabled {
			return fmt.Errorf("逐用户机器观测无法启用：入口 %s 的落地不存在或已禁用", edge.from.Tag)
		}
		if edge.from.ServerID == target.ServerID {
			return fmt.Errorf("逐用户机器观测无法启用：入口 %s 存在尚不支持的同机多跳", edge.from.Tag)
		}
		// The initial topology scan contains only graph fields. Read the full
		// source in this same transaction before checking protocol options.
		source, err := meteringInboundWith(tx, edge.from.ID)
		if err != nil {
			return err
		}
		if err = supportedUserMeteringEdge(source, target); err != nil {
			return err
		}
		// Pure protocol rendering catches known incompatible TLS/transport
		// combinations before the opt-in switch commits. This is a static
		// preflight, not a claim of end-to-end connectivity or a core check.
		for _, endpoint := range []*SbInbound{source, target} {
			if endpoint.Type == "mixed" {
				continue
			}
			_, server, tls, err := s.relayTargetSpecWith(tx, endpoint)
			if err != nil {
				return fmt.Errorf("逐用户机器观测无法启用：入站 %s 不可用：%w", endpoint.Tag, err)
			}
			probe := singbox.User{UUID: "00000000-0000-4000-8000-000000000001", Password: "protocol-preflight-only"}
			if _, err = s.relayOutboundWithIdentity(endpoint,
				map[int64]*Server{endpoint.ServerID: server}, map[int64]*SbTls{endpoint.TlsID: tls}, &probe, "protocol-preflight-only"); err != nil {
				return fmt.Errorf("逐用户机器观测无法启用：入站 %s 的协议配置不可用：%w", endpoint.Tag, err)
			}
		}
		graph[edge.from.ServerID] = append(graph[edge.from.ServerID], target.ServerID)
	}
	colors := map[int64]int{}
	var visit func(int64) bool
	visit = func(serverID int64) bool {
		if colors[serverID] == 1 {
			return false
		}
		if colors[serverID] == 2 {
			return true
		}
		colors[serverID] = 1
		for _, target := range graph[serverID] {
			if !visit(target) {
				return false
			}
		}
		colors[serverID] = 2
		return true
	}
	for serverID := range graph {
		if !visit(serverID) {
			return fmt.Errorf("逐用户机器观测无法启用：现有中转路径存在环路")
		}
	}
	return validateRelayVisionCapabilitiesWith(tx, time.Now().Unix())
}

func scanRelayMeteringUser(row scanner) (*RelayMeteringUser, error) {
	u := new(RelayMeteringUser)
	err := row.Scan(&u.ID, &u.LinkID, &u.UserID, &u.Generation, &u.IdentityName, &u.Credential, &u.Enabled, &u.State, &u.CreatedAt, &u.AcceptedAt, &u.ActivatedAt, &u.SourceNames, &u.SourceAuthHashes)
	return u, err
}

func relayMeteringUsersWith(db txLike) ([]*RelayMeteringUser, error) {
	rows, err := db.Query(`SELECT ` + relayMeteringUserCols + ` FROM relay_metering_users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []*RelayMeteringUser{}
	for rows.Next() {
		u, err := scanRelayMeteringUser(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (s *Store) RelayMeteringUsers() ([]*RelayMeteringUser, error) {
	return relayMeteringUsersWith(s.db)
}

func (u *RelayMeteringUser) outboundTag() string {
	return fmt.Sprintf("relay-link-%d-u%d-g%d", u.LinkID, u.UserID, u.Generation)
}

func (s *Store) relayMeteringUserCredential(u *RelayMeteringUser) (singbox.User, error) {
	user, err := s.meteringRelayUser(&RelayMeteringLink{ID: u.LinkID, IdentityName: u.IdentityName, Credential: u.Credential})
	user.OwnerID = u.UserID
	return user, err
}

// Recompute reachability from actual entitled client identities, never from old
// relay rows. Physical next hops propagate the original owner across a DAG.
// Logical routes apply only to the original route-qualified client identities;
// receiving a relay on an inbound does not select an unrelated logical exit.
func (s *Store) prepareRelayMeteringUsers(inbounds []*SbInbound, edges []relayMeteringEdge) error {
	if !s.RelayUserMeteringEnabled() {
		return nil
	}
	usersByTag, err := s.BuildUsersByTag(time.Now().Unix())
	if err != nil {
		return err
	}
	logical := map[int64][]int64{}
	for _, edge := range edges {
		if edge.route != 0 {
			logical[edge.from.ID] = append(logical[edge.from.ID], edge.route)
		}
	}
	owners := make([]map[int64]bool, len(edges))
	clientNames := make([]map[int64][]string, len(edges))
	incoming := map[int64]map[int64]bool{}
	for i, edge := range edges {
		owners[i] = map[int64]bool{}
		clientNames[i] = map[int64][]string{}
		for _, user := range usersByTag[edge.from.Tag] {
			if user.OwnerID <= 0 {
				return fmt.Errorf("入口 %s 存在未确认归属的用户，不能切换逐用户机器观测", edge.from.Tag)
			}
			if edge.route != 0 && !isRouteIdentityFor(user.Name, edge.route) {
				continue
			}
			if edge.route == 0 && matchesLogicalRelay(user.Name, logical[edge.from.ID]) {
				continue
			}
			owners[i][user.OwnerID] = true
			clientNames[i][user.OwnerID] = append(clientNames[i][user.OwnerID], user.Name)
		}
	}
	// At most one new downstream level per pass; PrepareRelayMetering already
	// rejected cycles and same-machine hops before any identities are persisted.
	for pass := 0; pass <= len(edges); pass++ {
		changed := false
		for i, edge := range edges {
			if edge.route == 0 {
				for owner := range incoming[edge.from.ID] {
					if !owners[i][owner] {
						owners[i][owner] = true
						changed = true
					}
				}
			}
			if incoming[edge.to.ID] == nil {
				incoming[edge.to.ID] = map[int64]bool{}
			}
			for owner := range owners[i] {
				if !incoming[edge.to.ID][owner] {
					incoming[edge.to.ID][owner] = true
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var enabled string
	if err = tx.QueryRow(`SELECT value FROM settings WHERE key=?`, RelayUserMeteringSetting).Scan(&enabled); err != nil || enabled != "true" {
		return err
	}
	existing, err := relayMeteringUsersWith(tx)
	if err != nil {
		return err
	}
	type key struct {
		link, owner int64
		generation  int
	}
	byKey := map[key]*RelayMeteringUser{}
	for _, user := range existing {
		byKey[key{user.LinkID, user.UserID, user.Generation}] = user
	}
	expected := map[[2]int64]bool{}
	edgeLinks := make([]*RelayMeteringLink, len(edges))
	for i, edge := range edges {
		link, err := scanRelayMetering(tx.QueryRow(`SELECT `+relayMeteringCols+` FROM relay_metering_links WHERE source_server_id=? AND source_inbound_id=? AND route_node_id=? AND target_server_id=? AND target_inbound_id=?`, edge.from.ServerID, edge.from.ID, edge.route, edge.to.ServerID, edge.to.ID))
		if err != nil {
			return err
		}
		edgeLinks[i] = link
		ids := make([]int64, 0, len(owners[i]))
		for owner := range owners[i] {
			ids = append(ids, owner)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, owner := range ids {
			expected[[2]int64{link.ID, owner}] = true
			if byKey[key{link.ID, owner, link.Generation}] != nil {
				continue
			}
			name, encrypted, err := s.newRelayMeteringCredential(tx)
			if err != nil {
				return err
			}
			if _, err = tx.Exec(`INSERT INTO relay_metering_users(link_id,user_id,generation,identity_name,credential,created_at) VALUES(?,?,?,?,?,?)`, link.ID, owner, link.Generation, name, encrypted, time.Now().Unix()); err != nil {
				return err
			}
			// Existing shared readiness cannot endorse a newly added user.
			if _, err = tx.Exec(`UPDATE relay_metering_links SET state='prepared',accepted_at=0,activated_at=0 WHERE id=?`, link.ID); err != nil {
				return err
			}
		}
	}
	for _, user := range existing {
		enabled := expected[[2]int64{user.LinkID, user.UserID}]
		if user.Enabled != enabled {
			state := user.State
			if enabled {
				// A disabled credential might have been removed from the target.
				state = "prepared"
			}
			if _, err = tx.Exec(`UPDATE relay_metering_users SET enabled=?,state=?,accepted_at=CASE WHEN ? THEN 0 ELSE accepted_at END,activated_at=CASE WHEN ? THEN 0 ELSE activated_at END WHERE id=?`, enabled, state, enabled, enabled, user.ID); err != nil {
				return err
			}
		}
	}
	// Capture exact desired source identities after registering all downstream
	// rows. Applied outbound credentials alone cannot prove that the original
	// users were actually routed into them.
	allUsers, err := relayMeteringUsersWith(tx)
	if err != nil {
		return err
	}
	retiredUsers, err := retiredRelayUserIDs(tx)
	if err != nil {
		return err
	}
	allLinks, err := relayMeteringLinksWith(tx)
	if err != nil {
		return err
	}
	linkByID := map[int64]*RelayMeteringLink{}
	serverByInbound := map[int64]int64{}
	for _, inbound := range inbounds {
		serverByInbound[inbound.ID] = inbound.ServerID
	}
	for _, link := range allLinks {
		linkByID[link.ID] = link
	}
	incomingNames := map[int64]map[int64][]string{}
	incomingCredentials := map[string]singbox.User{}
	for _, user := range allUsers {
		link := linkByID[user.LinkID]
		if !user.Enabled || link == nil || retiredUsers[user.ID] {
			continue
		}
		server, exists := serverByInbound[link.TargetInboundID]
		if !exists || server != link.TargetServerID {
			continue
		}
		if incomingNames[link.TargetInboundID] == nil {
			incomingNames[link.TargetInboundID] = map[int64][]string{}
		}
		incomingNames[link.TargetInboundID][user.UserID] = append(incomingNames[link.TargetInboundID][user.UserID], user.IdentityName)
		credential, err := s.relayMeteringUserCredential(user)
		if err != nil {
			return err
		}
		incomingCredentials[user.IdentityName] = credential
	}
	for i, edge := range edges {
		link := edgeLinks[i]
		base, err := s.relayMeteringInboundBaseWith(tx, edge.from)
		if err != nil {
			return err
		}
		authHashes := map[string]string{}
		for _, user := range usersByTag[edge.from.Tag] {
			authHashes[user.Name] = s.relayAuthenticationHash(singbox.UserConfig(edge.from.Type, user, base))
		}
		for _, names := range incomingNames[edge.from.ID] {
			for _, name := range names {
				authHashes[name] = s.relayAuthenticationHash(singbox.UserConfig(edge.from.Type, incomingCredentials[name], base))
			}
		}
		for owner := range owners[i] {
			names := map[string]bool{}
			for _, name := range clientNames[i][owner] {
				names[name] = true
			}
			if edge.route == 0 {
				for _, name := range incomingNames[edge.from.ID][owner] {
					names[name] = true
				}
			}
			ordered := make([]string, 0, len(names))
			for name := range names {
				ordered = append(ordered, name)
			}
			sort.Strings(ordered)
			encoded, err := json.Marshal(ordered)
			if err != nil {
				return err
			}
			ownedHashes := map[string]string{}
			for _, name := range ordered {
				if authHashes[name] == "" {
					return fmt.Errorf("入口 %s 的用户认证映射未就绪", edge.from.Tag)
				}
				ownedHashes[name] = authHashes[name]
			}
			hashes, err := json.Marshal(ownedHashes)
			if err != nil {
				return err
			}
			if _, err = tx.Exec(`UPDATE relay_metering_users SET source_names=?,source_auth_hashes=?,state=CASE WHEN state='active' THEN 'accepted' ELSE state END,activated_at=0 WHERE link_id=? AND user_id=? AND generation=? AND (source_names<>? OR source_auth_hashes<>?)`, string(encoded), string(hashes), link.ID, owner, link.Generation, string(encoded), string(hashes)); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func matchesLogicalRelay(name string, ids []int64) bool {
	for _, id := range ids {
		if isRouteIdentityFor(name, id) {
			return true
		}
	}
	return false
}

// Keep actually accepted old generations during opt-out. Prepared credentials
// are only installed while P1 is enabled, and disabled owners never self-revive.
func (s *Store) relayUserLandingUsers() (map[int64][]singbox.User, error) {
	rows, err := s.db.Query(`SELECT u.identity_name,u.credential,u.user_id,u.link_id,l.target_inbound_id,l.target_server_id FROM relay_metering_users u JOIN relay_metering_links l ON l.id=u.link_id JOIN sb_inbounds i ON i.id=l.target_inbound_id AND i.server_id=l.target_server_id WHERE u.enabled=1 AND NOT EXISTS(SELECT 1 FROM relay_user_retirements r WHERE r.relay_user_id=u.id AND r.state IN ('retiring','retired')) AND (u.accepted_at>0 OR ?=1 OR EXISTS(SELECT 1 FROM relay_user_retirements r WHERE r.relay_user_id=u.id AND r.state='restoring')) ORDER BY u.id`, s.RelayUserMeteringEnabled())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]singbox.User{}
	for rows.Next() {
		var user RelayMeteringUser
		var inboundID, serverID int64
		if err = rows.Scan(&user.IdentityName, &user.Credential, &user.UserID, &user.LinkID, &inboundID, &serverID); err != nil {
			return nil, err
		}
		credential, err := s.relayMeteringUserCredential(&user)
		if err != nil {
			return nil, err
		}
		out[inboundID] = append(out[inboundID], credential)
	}
	return out, rows.Err()
}

func (s *Store) meteredUserRelays(from *SbInbound, routeID int64, landing *SbInbound, users []singbox.User, serverCache map[int64]*Server, tlsCache map[int64]*SbTls) ([]singbox.Relay, error) {
	if err := supportedUserMeteringEdge(from, landing); err != nil {
		return nil, err
	}
	groups := map[int64][]string{}
	for _, user := range users {
		if user.OwnerID <= 0 {
			if user.Relay {
				continue // old shared traffic remains explicitly unattributed
			}
			return nil, fmt.Errorf("入口 %s 的用户归属未确认，不能切换逐用户出口", from.Tag)
		}
		groups[user.OwnerID] = append(groups[user.OwnerID], user.Name)
	}
	if len(groups) == 0 {
		return nil, nil
	}
	link, err := scanRelayMetering(s.db.QueryRow(`SELECT `+relayMeteringCols+` FROM relay_metering_links WHERE source_server_id=? AND source_inbound_id=? AND route_node_id=? AND target_server_id=? AND target_inbound_id=?`, from.ServerID, from.ID, routeID, landing.ServerID, landing.ID))
	if err != nil {
		return nil, fmt.Errorf("逐用户线路尚未注册，保持原配置")
	}
	spec, server, tls, err := s.relayTargetSpec(landing)
	if err != nil || spec != link.SpecHash {
		return nil, fmt.Errorf("逐用户线路 %d 的落地配置已变化，等待重新确认", link.ID)
	}
	if server != nil {
		serverCache[landing.ServerID] = server
	}
	if tls != nil {
		tlsCache[landing.TlsID] = tls
	}
	rows, err := s.db.Query(`SELECT `+relayMeteringUserCols+` FROM relay_metering_users WHERE link_id=? AND generation=? AND enabled=1`, link.ID, link.Generation)
	if err != nil {
		return nil, err
	}
	byOwner := map[int64]*RelayMeteringUser{}
	for rows.Next() {
		u, err := scanRelayMeteringUser(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		byOwner[u.UserID] = u
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	owners := make([]int64, 0, len(groups))
	for owner := range groups {
		owners = append(owners, owner)
	}
	sort.Slice(owners, func(i, j int) bool { return owners[i] < owners[j] })
	out := make([]singbox.Relay, 0, len(owners))
	for _, owner := range owners {
		u := byOwner[owner]
		if u == nil || (u.State != "accepted" && u.State != "active") {
			return nil, fmt.Errorf("线路 %d 的用户 #%d 尚未由落地确认，入口保持原配置", link.ID, owner)
		}
		credential, err := s.relayMeteringUserCredential(u)
		if err != nil {
			return nil, err
		}
		outbound, err := s.relayOutboundWithIdentity(landing, serverCache, tlsCache, &credential, u.outboundTag())
		if err != nil {
			return nil, err
		}
		sort.Strings(groups[owner])
		out = append(out, singbox.Relay{Outbound: outbound, InboundTags: []string{from.Tag}, AuthUsers: groups[owner]})
	}
	return out, nil
}

func (s *Store) acknowledgeRelayMeteringUsers(tx *sql.Tx, serverID int64, raw []byte) error {
	if err := s.acknowledgeRelayMeteringUsersState(tx, serverID, raw); err != nil {
		return err
	}
	// Retirement proof must use readiness validated against this same apply,
	// never a stale active flag retained while P1 was disabled.
	return s.acknowledgeRelayUserRetirements(tx, serverID, raw)
}

func (s *Store) acknowledgeRelayMeteringUsersState(tx *sql.Tx, serverID int64, raw []byte) error {
	var enabled string
	err := tx.QueryRow(`SELECT value FROM settings WHERE key=?`, RelayUserMeteringSetting).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if enabled != "true" {
		return nil
	}
	var cfg struct {
		Inbounds []struct {
			Type  string                   `json:"type"`
			Tag   string                   `json:"tag"`
			Port  int                      `json:"listen_port"`
			Users []map[string]interface{} `json:"users"`
		} `json:"inbounds"`
		Outbounds []map[string]interface{} `json:"outbounds"`
		Route     struct {
			Rules []map[string]interface{} `json:"rules"`
		} `json:"route"`
		Experimental struct {
			V2Ray struct {
				Stats struct {
					Users []string `json:"users"`
				} `json:"stats"`
			} `json:"v2ray_api"`
		} `json:"experimental"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	var fullConfig struct {
		Inbounds []map[string]interface{} `json:"inbounds"`
	}
	if err := json.Unmarshal(raw, &fullConfig); err != nil {
		return err
	}
	actualBases := map[string]map[string]interface{}{}
	for _, inbound := range fullConfig.Inbounds {
		tag, _ := inbound["tag"].(string)
		delete(inbound, "users")
		actualBases[tag] = inbound
	}
	observed := map[string]bool{}
	for _, name := range cfg.Experimental.V2Ray.Stats.Users {
		observed[name] = true
	}
	inboundIndex := map[string]int{}
	inboundUsers := map[string]map[string]map[string]interface{}{}
	invalidInbounds := map[string]bool{}
	for i, inbound := range cfg.Inbounds {
		if _, exists := inboundIndex[inbound.Tag]; exists {
			invalidInbounds[inbound.Tag] = true
		}
		inboundIndex[inbound.Tag] = i
		wireKeys := map[string]bool{}
		inboundUsers[inbound.Tag] = map[string]map[string]interface{}{}
		for _, user := range inbound.Users {
			name, ok := user["name"].(string)
			if !ok {
				name, ok = user["username"].(string)
			}
			key := relayAuthenticationKey(inbound.Type, user)
			if !ok || name == "" || key == "" || inboundUsers[inbound.Tag][name] != nil || wireKeys[key] {
				invalidInbounds[inbound.Tag] = true
			}
			wireKeys[key] = true
			if ok {
				inboundUsers[inbound.Tag][name] = user
			}
		}
	}
	outboundIndex := map[string]map[string]interface{}{}
	duplicateOutbounds := map[string]bool{}
	for _, outbound := range cfg.Outbounds {
		if tag, ok := outbound["tag"].(string); ok {
			if outboundIndex[tag] != nil {
				duplicateOutbounds[tag] = true
			}
			outboundIndex[tag] = outbound
		}
	}
	rows, err := tx.Query(`SELECT u.id,u.link_id,u.user_id,u.generation,u.identity_name,u.credential,u.enabled,u.state,u.created_at,u.accepted_at,u.activated_at,u.source_names,u.source_auth_hashes FROM relay_metering_users u JOIN relay_metering_links l ON l.id=u.link_id WHERE u.enabled=1 AND u.generation=l.generation AND (l.source_server_id=? OR l.target_server_id=?) ORDER BY u.id`, serverID, serverID)
	if err != nil {
		return err
	}
	var users []*RelayMeteringUser
	for rows.Next() {
		user, err := scanRelayMeteringUser(rows)
		if err != nil {
			rows.Close()
			return err
		}
		users = append(users, user)
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if err = rows.Err(); err != nil {
		return err
	}
	links, err := relayMeteringLinksWith(tx)
	if err != nil {
		return err
	}
	byLink := map[int64]*RelayMeteringLink{}
	type targetState struct {
		inbound *SbInbound
		server  *Server
		tls     *SbTls
	}
	targets := map[int64]targetState{}
	sources := map[int64]*SbInbound{}
	for _, link := range links {
		byLink[link.ID] = link
		if link.SourceServerID != serverID && link.TargetServerID != serverID {
			continue
		}
		target, err := meteringInboundWith(tx, link.TargetInboundID)
		if err != nil {
			return err
		}
		if target == nil || target.ServerID != link.TargetServerID {
			continue
		}
		spec, server, tls, err := s.relayTargetSpecWith(tx, target)
		if err != nil || spec != link.SpecHash {
			continue
		}
		targets[link.ID] = targetState{target, server, tls}
		if link.SourceServerID == serverID {
			source, err := meteringInboundWith(tx, link.SourceInboundID)
			if err != nil {
				return err
			}
			if source != nil && source.ServerID == serverID {
				sources[link.ID] = source
			}
		}
	}
	wantedRoutes := map[string]map[string]string{}
	subjectNames := map[int64][]string{}
	subjectHashes := map[int64]map[string]string{}
	expectedBases := map[int64]map[string]interface{}{}
	authenticationBases := map[int64]map[string]interface{}{}
	for _, ib := range sources {
		if expectedBases[ib.ID] != nil {
			continue
		}
		base, err := s.relayMeteringInboundBaseWith(tx, ib)
		if err != nil {
			return err
		}
		authenticationBases[ib.ID] = base
		expectedBases[ib.ID], err = relayMeteringRenderedInboundBase(ib, base)
		if err != nil {
			return err
		}
	}
	for _, state := range targets {
		ib := state.inbound
		if expectedBases[ib.ID] != nil {
			continue
		}
		base, err := s.relayMeteringInboundBaseWith(tx, ib)
		if err != nil {
			return err
		}
		authenticationBases[ib.ID] = base
		expectedBases[ib.ID], err = relayMeteringRenderedInboundBase(ib, base)
		if err != nil {
			return err
		}
	}
	for _, user := range users {
		if source := sources[user.LinkID]; source != nil {
			var names []string
			if err = json.Unmarshal([]byte(user.SourceNames), &names); err != nil {
				return fmt.Errorf("线路 %d 的用户来源映射无效", user.LinkID)
			}
			subjectNames[user.ID] = names
			var hashes map[string]string
			if err = json.Unmarshal([]byte(user.SourceAuthHashes), &hashes); err != nil {
				return fmt.Errorf("线路 %d 的用户认证映射无效", user.LinkID)
			}
			subjectHashes[user.ID] = hashes
			if wantedRoutes[source.Tag] == nil {
				wantedRoutes[source.Tag] = map[string]string{}
			}
			for _, name := range names {
				if other := wantedRoutes[source.Tag][name]; other != "" && other != user.outboundTag() {
					return fmt.Errorf("入站 %s 存在冲突的逐用户路由", source.Tag)
				}
				wantedRoutes[source.Tag][name] = user.outboundTag()
			}
		}
	}
	appliedRoutes, invalidOutbounds := appliedRelayUserRoutes(cfg.Route.Rules, wantedRoutes)
	now := time.Now().Unix()
	for _, user := range users {
		link := byLink[user.LinkID]
		if !user.Enabled || link == nil || user.Generation != link.Generation || (link.TargetServerID != serverID && link.SourceServerID != serverID) {
			continue
		}
		targetState, valid := targets[link.ID]
		if !valid {
			continue
		}
		target := targetState.inbound
		credential, err := s.relayMeteringUserCredential(user)
		if err != nil {
			return err
		}
		if link.TargetServerID == serverID {
			index, exists := inboundIndex[target.Tag]
			matched := exists && !invalidInbounds[target.Tag] && cfg.Inbounds[index].Type == target.Type && cfg.Inbounds[index].Port == target.ListenPort && observed[user.IdentityName] && relayAppliedObjectMatches(expectedBases[target.ID], actualBases[target.Tag]) && relayAppliedObjectMatches(singbox.UserConfig(target.Type, credential, authenticationBases[target.ID]), inboundUsers[target.Tag][user.IdentityName])
			if matched {
				_, err = tx.Exec(`UPDATE relay_metering_users SET state='accepted',accepted_at=? WHERE id=? AND enabled=1 AND state='prepared'`, now, user.ID)
			} else {
				_, err = tx.Exec(`UPDATE relay_metering_users SET state='prepared',accepted_at=0,activated_at=0 WHERE id=? AND enabled=1 AND state<>'prepared'`, user.ID)
			}
			if err != nil {
				return err
			}
		}
		if link.SourceServerID == serverID {
			outbound := outboundIndex[user.outboundTag()]
			source := sources[link.ID]
			routed := source != nil && !invalidInbounds[source.Tag] && len(subjectNames[user.ID]) > 0 && !invalidOutbounds[user.outboundTag()] && !duplicateOutbounds[user.outboundTag()]
			if routed {
				index, exists := inboundIndex[source.Tag]
				routed = exists && cfg.Inbounds[index].Type == source.Type && cfg.Inbounds[index].Port == source.ListenPort && relayAppliedObjectMatches(expectedBases[source.ID], actualBases[source.Tag])
				for _, name := range subjectNames[user.ID] {
					if !observed[name] || inboundUsers[source.Tag][name] == nil || subjectHashes[user.ID][name] == "" || subjectHashes[user.ID][name] != s.relayAuthenticationHash(inboundUsers[source.Tag][name]) || appliedRoutes[source.Tag][name] != user.outboundTag() {
						routed = false
					}
				}
			}
			// Compare the whole managed outbound, not just a UUID. Password
			// protocols, TUIC's two credentials, SS2022's server/user key pair,
			// TLS/flow, transport and obfuscation must all match this generation.
			// Complete caches keep this renderer read-only inside the transaction.
			expected, renderErr := s.relayOutboundWithIdentity(target,
				map[int64]*Server{target.ServerID: targetState.server},
				map[int64]*SbTls{target.TlsID: targetState.tls}, &credential, user.outboundTag())
			if renderErr != nil {
				return renderErr
			}
			if routed && relayAppliedObjectMatches(expected, outbound) {
				_, err = tx.Exec(`UPDATE relay_metering_users SET state='active',activated_at=? WHERE id=? AND enabled=1 AND state='accepted'`, now, user.ID)
			} else {
				_, err = tx.Exec(`UPDATE relay_metering_users SET state='accepted',activated_at=0 WHERE id=? AND enabled=1 AND state='active'`, user.ID)
			}
			if err != nil {
				return err
			}
		}
	}
	// Report aggregate readiness for the current user set, not merely acceptance
	// of the old shared identity. This also makes staged rebuilds observe progress.
	for _, link := range links {
		if link.SourceServerID != serverID && link.TargetServerID != serverID {
			continue
		}
		var total, prepared, accepted int
		if err = tx.QueryRow(`SELECT COUNT(*),COALESCE(SUM(state='prepared'),0),COALESCE(SUM(state='accepted'),0) FROM relay_metering_users WHERE link_id=? AND generation=? AND enabled=1`, link.ID, link.Generation).Scan(&total, &prepared, &accepted); err != nil {
			return err
		}
		if total == 0 {
			continue
		}
		state := "active"
		if prepared > 0 {
			state = "prepared"
		} else if accepted > 0 {
			state = "accepted"
		}
		if _, err = tx.Exec(`UPDATE relay_metering_links SET state=? WHERE id=?`, state, link.ID); err != nil {
			return err
		}
	}
	return nil
}

// Resolve first-match routing for the expected source identities in linear
// passes over rule memberships. Unknown/custom route selectors are conservative:
// an earlier route that may take these users prevents an active acknowledgment.
func appliedRelayUserRoutes(rules []map[string]interface{}, wanted map[string]map[string]string) (map[string]map[string]string, map[string]bool) {
	applied := map[string]map[string]string{}
	invalid := map[string]bool{}
	ownedOutbounds := map[string]bool{}
	var allInbounds []string
	for inbound, users := range wanted {
		allInbounds = append(allInbounds, inbound)
		applied[inbound] = map[string]string{}
		for _, outbound := range users {
			ownedOutbounds[outbound] = true
		}
	}
	for _, rule := range rules {
		outbound, routes := rule["outbound"].(string)
		action, _ := rule["action"].(string)
		if !routes && action != "route" {
			continue
		}
		if outbound == "" {
			outbound = "unresolved-route"
		}
		if ownedOutbounds[outbound] {
			// The compiler's user branch is an unconditional positive match on
			// inbound+auth_user. Additional predicates (including invert) cannot
			// prove that all traffic for these users takes this observed hop.
			for field := range rule {
				switch field {
				case "inbound", "auth_user", "outbound", "action":
				default:
					invalid[outbound] = true
				}
			}
			if action != "" && action != "route" {
				invalid[outbound] = true
			}
		}
		inbounds := relayRuleStrings(rule["inbound"])
		if len(inbounds) == 0 {
			inbounds = allInbounds
		}
		auth := relayRuleStrings(rule["auth_user"])
		if inverted, _ := rule["invert"].(bool); inverted {
			// Conservatively treat an inverted earlier rule as potentially
			// selecting any expected identity, rather than narrowing it backwards.
			inbounds, auth = allInbounds, nil
		}
		for _, inbound := range inbounds {
			users := wanted[inbound]
			if users == nil {
				if ownedOutbounds[outbound] {
					invalid[outbound] = true
				}
				continue
			}
			if len(auth) == 0 {
				if ownedOutbounds[outbound] {
					invalid[outbound] = true // user-specific exits must not carry a wildcard
				}
				for name := range users {
					if applied[inbound][name] == "" {
						applied[inbound][name] = outbound
					}
				}
				continue
			}
			for _, name := range auth {
				if ownedOutbounds[outbound] && users[name] != outbound {
					invalid[outbound] = true
				}
				if users[name] != "" && applied[inbound][name] == "" {
					applied[inbound][name] = outbound
				}
			}
		}
	}
	return applied, invalid
}

func relayRuleStrings(value interface{}) []string {
	switch values := value.(type) {
	case string:
		return []string{values}
	case []interface{}:
		var out []string
		for _, item := range values {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	}
	return nil
}

// Both maps may come from different stages (Go values versus JSON decoding),
// so canonical JSON also normalizes int/float representations without weakening
// the exact-field check. Additional detours, network filters or TLS overrides
// cannot accidentally endorse an otherwise plausible credential.
func relayAppliedObjectMatches(expected, actual map[string]interface{}) bool {
	if expected == nil || actual == nil {
		return false
	}
	want, err := json.Marshal(expected)
	if err != nil {
		return false
	}
	got, err := json.Marshal(actual)
	return err == nil && bytes.Equal(want, got)
}
