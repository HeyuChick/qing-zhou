package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"qingzhou/internal/intervalcfg"
	"qingzhou/internal/singbox"
)

// No identity, credential, observation or tombstone is removed. This table is
// only the reversible desired/runtime lifecycle of historical authentication.
const relayUserRetirementSchema = `
CREATE TABLE relay_user_retirements (
 relay_user_id INTEGER PRIMARY KEY,
 state TEXT NOT NULL DEFAULT 'active',
 source_clear_at INTEGER NOT NULL DEFAULT 0,
 source_config_hash TEXT NOT NULL DEFAULT '',
 updated_at INTEGER NOT NULL DEFAULT 0,
 applied_at INTEGER NOT NULL DEFAULT 0
);
ALTER TABLE relay_credential_audit ADD COLUMN user_id INTEGER NOT NULL DEFAULT 0;
CREATE INDEX idx_traffic_polls_server_state_time ON traffic_polls(server_id,state,observed_at);
CREATE INDEX idx_traffic_observations_identity_positive ON traffic_observations(server_id,counter_name,ts) WHERE up+down>0;
`

func (s *Store) relayUserCredentialViews() ([]RelayCredentialView, error) {
	rows, err := s.db.Query(`SELECT u.link_id,u.user_id,l.target_server_id,l.target_inbound_id,u.generation,l.generation,COALESCE(r.state,'active'),l.source_name,l.target_name FROM relay_metering_users u JOIN relay_metering_links l ON l.id=u.link_id LEFT JOIN relay_user_retirements r ON r.relay_user_id=u.id ORDER BY u.link_id,u.user_id,u.generation`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RelayCredentialView{}
	for rows.Next() {
		var v RelayCredentialView
		var current int
		var from, to string
		if err = rows.Scan(&v.LinkID, &v.UserID, &v.ServerID, &v.InboundID, &v.Generation, &current, &v.State, &from, &to); err != nil {
			return nil, err
		}
		v.Kind = "user_generation"
		v.Name = fmt.Sprintf("%s → %s 用户 #%d 第%d代", from, to, v.UserID, v.Generation)
		if v.Generation == current {
			v.State = "current"
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type retirementSettings struct{ db txLike }

func (g retirementSettings) GetSetting(key string) (string, error) {
	var v string
	err := g.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	return v, err
}

func (s *Store) canRetireRelayUser(db txLike, v RelayCredentialView) error {
	var enabled string
	if err := db.QueryRow(`SELECT value FROM settings WHERE key=?`, RelayUserMeteringSetting).Scan(&enabled); err != nil || enabled != "true" {
		return fmt.Errorf("逐用户计量必须保持启用，才能验证替代身份")
	}

	var oldID, source, clear, activated int64
	var name, currentState, life, proof, applied string
	var current int
	err := db.QueryRow(`SELECT u.id,u.identity_name,l.source_server_id,l.generation,COALESCE(n.state,''),COALESCE(n.activated_at,0),COALESCE(r.state,'active'),COALESCE(r.source_clear_at,0),COALESCE(r.source_config_hash,''),COALESCE(a.config_hash,'')
 FROM relay_metering_users u JOIN relay_metering_links l ON l.id=u.link_id
 LEFT JOIN relay_metering_users n ON n.link_id=l.id AND n.user_id=u.user_id AND n.generation=l.generation AND n.enabled=1
 LEFT JOIN relay_user_retirements r ON r.relay_user_id=u.id LEFT JOIN relay_metering_applies a ON a.server_id=l.source_server_id
 WHERE u.link_id=? AND u.user_id=? AND u.generation=? AND l.target_server_id=? AND l.target_inbound_id=?`, v.LinkID, v.UserID, v.Generation, v.ServerID, v.InboundID).Scan(&oldID, &name, &source, &current, &currentState, &activated, &life, &clear, &proof, &applied)
	if err != nil {
		return err
	}
	if v.Generation >= current || currentState != "active" || activated <= 0 {
		return fmt.Errorf("替代用户身份尚未在来源实际启用，保留旧代凭据")
	}
	if life != "active" {
		return fmt.Errorf("旧用户凭据已有待完成的生命周期操作")
	}
	if clear <= 0 || proof == "" || proof != applied {
		return fmt.Errorf("尚无最新来源配置已移除旧凭据依赖的证明")
	}
	boundary := clear
	if activated > boundary {
		boundary = activated
	}
	oldOutbound := fmt.Sprintf("outbound:relay-link-%d-u%d-g%d", v.LinkID, v.UserID, v.Generation)
	var positive int64
	if err = db.QueryRow(`SELECT COALESCE(MAX(ts),0) FROM traffic_observations WHERE ((server_id=? AND counter_name=?) OR (server_id=? AND counter_name=?)) AND up+down>0`, v.ServerID, name, source, oldOutbound).Scan(&positive); err != nil {
		return err
	}
	if positive > boundary {
		boundary = positive
	}
	var collecting int
	if err = db.QueryRow(`SELECT COUNT(*) FROM traffic_collection_leases WHERE server_id IN (?,?) AND expires_at>?`, source, v.ServerID, time.Now().Unix()).Scan(&collecting); err != nil {
		return err
	}
	if collecting > 0 {
		return fmt.Errorf("来源或落地正在采集，等待当前批次持久化后再停用")
	}
	var pending int
	if err = db.QueryRow(`SELECT COUNT(*) FROM traffic_polls WHERE server_id IN (?,?) AND state='pending'`, source, v.ServerID).Scan(&pending); err != nil {
		return err
	}
	if pending > 0 {
		return fmt.Errorf("来源或落地仍有未入库批次，先完成计数排空")
	}
	var lastGap int64
	if err = db.QueryRow(`SELECT COALESCE(MAX(ts),0) FROM traffic_metering_gaps WHERE server_id IN (?,?) AND ts>=?`, source, v.ServerID, boundary).Scan(&lastGap); err != nil {
		return err
	}
	if lastGap > boundary {
		boundary = lastGap
	}

	now := time.Now().Unix()
	quiet := int64((2 * intervalcfg.Stats(retirementSettings{db})) / time.Second)
	if now < boundary+quiet {
		return fmt.Errorf("等待旧用户身份的两个完整采集周期安静窗口")
	}
	for _, server := range []int64{source, v.ServerID} {
		var count int
		var latest int64
		if err = db.QueryRow(`SELECT COUNT(DISTINCT observed_at),COALESCE(MAX(observed_at),0) FROM traffic_polls WHERE server_id=? AND state='done' AND observed_at>? AND observed_at<=?`, server, boundary, now).Scan(&count, &latest); err != nil {
			return err
		}
		var status string
		if err = db.QueryRow(`SELECT status FROM traffic_metering_state WHERE server_id=?`, server).Scan(&status); err != nil {
			return err
		}
		if count < 2 || latest < boundary+quiet || latest < now-quiet-60 || status != "ok" {
			return fmt.Errorf("等待来源和落地各两次成功采集并覆盖完整安静窗口")
		}
	}
	return nil
}

func (s *Store) changeRelayUserCredential(tx *sql.Tx, v RelayCredentialView, action string, now int64) error {
	var id int64
	if err := tx.QueryRow(`SELECT u.id FROM relay_metering_users u JOIN relay_metering_links l ON l.id=u.link_id WHERE u.link_id=? AND u.user_id=? AND u.generation=? AND u.generation<l.generation AND l.target_server_id=? AND l.target_inbound_id=?`, v.LinkID, v.UserID, v.Generation, v.ServerID, v.InboundID).Scan(&id); err != nil {
		return fmt.Errorf("只能变更已被替代的本用户旧代凭据")
	}
	state := "retiring"
	if action == "restore" {
		state = "restoring"
	}
	_, err := tx.Exec(`INSERT INTO relay_user_retirements(relay_user_id,state,updated_at) VALUES(?,?,?) ON CONFLICT(relay_user_id) DO UPDATE SET state=excluded.state,updated_at=excluded.updated_at,applied_at=0`, id, state, now)
	return err
}

// Index all credential-like string values once per applied config. This catches
// renamed outbound aliases, normalized UUIDs, Hysteria base64 auth and SS2022
// server:user key pairs without rescanning the whole config for every old user.
type relayCredentialReferences map[string]bool

func collectRelayCredentialReferences(value interface{}) relayCredentialReferences {
	refs := relayCredentialReferences{}
	var visit func(interface{})
	visit = func(value interface{}) {
		switch v := value.(type) {
		case string:
			refs[v] = true
			if id, err := uuid.Parse(v); err == nil {
				refs[id.String()] = true
			}
			if decoded, err := base64.StdEncoding.DecodeString(v); err == nil {
				refs[string(decoded)] = true
			}
			if decoded, err := base64.RawStdEncoding.DecodeString(v); err == nil {
				refs[string(decoded)] = true
			}
			if i := strings.LastIndexByte(v, ':'); i >= 0 {
				refs[v[i+1:]] = true
			}
		case []interface{}:
			for _, child := range v {
				visit(child)
			}
		case map[string]interface{}:
			for _, child := range v {
				visit(child)
			}
		}
	}
	visit(value)
	return refs
}
func (refs relayCredentialReferences) has(u singbox.User, tag string) bool {
	for _, secret := range []string{tag, u.Name, u.UUID, u.Password, singbox.DeriveSSKey(u.Password, "2022-blake3-aes-128-gcm"), singbox.DeriveSSKey(u.Password, "2022-blake3-aes-256-gcm")} {
		if secret != "" && (refs[secret] || refs[strings.TrimRight(secret, "=")]) {
			return true
		}
	}
	return false
}

func (s *Store) acknowledgeRelayUserRetirements(tx *sql.Tx, serverID int64, raw []byte) error {
	var cfg struct {
		Inbounds  []map[string]interface{} `json:"inbounds"`
		Outbounds []interface{}            `json:"outbounds"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	outboundRefs := collectRelayCredentialReferences(cfg.Outbounds)
	allInboundUsers := []interface{}{}
	for _, ib := range cfg.Inbounds {
		if users, ok := ib["users"].([]interface{}); ok {
			allInboundUsers = append(allInboundUsers, users...)
		}
	}
	inboundRefs := collectRelayCredentialReferences(allInboundUsers)
	users, err := relayMeteringUsersWith(tx)
	if err != nil {
		return err
	}
	links, err := relayMeteringLinksWith(tx)
	if err != nil {
		return err
	}
	byID := map[int64]*RelayMeteringLink{}
	for _, link := range links {
		byID[link.ID] = link
	}
	currentUsers := map[[3]int64]*RelayMeteringUser{}
	for _, u := range users {
		currentUsers[[3]int64{u.LinkID, u.UserID, int64(u.Generation)}] = u
	}
	var enabled string
	if err = tx.QueryRow(`SELECT COALESCE((SELECT value FROM settings WHERE key=?),'false')`, RelayUserMeteringSetting).Scan(&enabled); err != nil {
		return err
	}
	hash := sha256.Sum256(raw)
	digest := hex.EncodeToString(hash[:])
	now := time.Now().Unix()
	for _, u := range users {
		link := byID[u.LinkID]
		if link == nil || u.Generation >= link.Generation || (link.SourceServerID != serverID && link.TargetServerID != serverID) {
			continue
		}
		credential, err := s.relayMeteringUserCredential(u)
		if err != nil {
			return err
		}
		if link.SourceServerID == serverID {
			clear := now
			replacement := currentUsers[[3]int64{u.LinkID, u.UserID, int64(link.Generation)}]
			if enabled != "true" || replacement == nil || !replacement.Enabled || replacement.State != "active" || outboundRefs.has(credential, u.outboundTag()) {
				clear = 0
			}
			if _, err = tx.Exec(`INSERT INTO relay_user_retirements(relay_user_id,source_clear_at,source_config_hash) VALUES(?,?,?) ON CONFLICT(relay_user_id) DO UPDATE SET source_clear_at=CASE WHEN source_clear_at>0 AND excluded.source_clear_at>0 THEN source_clear_at ELSE excluded.source_clear_at END,source_config_hash=excluded.source_config_hash`, u.ID, clear, digest); err != nil {
				return err
			}
		}
		if link.TargetServerID != serverID {
			continue
		}
		var state string
		var requestedAt int64
		err = tx.QueryRow(`SELECT state,updated_at FROM relay_user_retirements WHERE relay_user_id=?`, u.ID).Scan(&state, &requestedAt)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return err
		}
		if state == "active" && requestedAt == 0 {
			continue
		}
		if state == "retiring" || state == "retired" {
			next, appliedAt := "retiring", int64(0)
			if !inboundRefs.has(credential, "") {
				next = "retired"
				appliedAt = now
			}
			if next != state {
				if _, err = tx.Exec(`UPDATE relay_user_retirements SET state=?,applied_at=? WHERE relay_user_id=? AND state=?`, next, appliedAt, u.ID, state); err != nil {
					return err
				}
			}
			continue
		}

		target, err := meteringInboundWith(tx, link.TargetInboundID)
		if err != nil {
			return err
		}
		if target == nil || target.ServerID != serverID {
			continue
		}
		base, err := s.relayMeteringInboundBaseWith(tx, target)
		if err != nil {
			return err
		}
		expectedBase, err := relayMeteringRenderedInboundBase(target, base)
		if err != nil {
			return err
		}
		var observed struct {
			Experimental struct {
				V2Ray struct {
					Stats struct {
						Users []string `json:"users"`
					} `json:"stats"`
				} `json:"v2ray_api"`
			} `json:"experimental"`
		}
		if err = json.Unmarshal(raw, &observed); err != nil {
			return err
		}
		statsPresent := false
		for _, name := range observed.Experimental.V2Ray.Stats.Users {
			if name == u.IdentityName {
				statsPresent = true
			}
		}
		matched := false
		targetCount := 0
		for _, ib := range cfg.Inbounds {
			actualUsers, _ := ib["users"].([]interface{})
			if ib["tag"] == target.Tag {
				targetCount++
			}
			if ib["tag"] != target.Tag || ib["type"] != target.Type || ib["listen_port"] != float64(target.ListenPort) {
				continue
			}
			actualBase := map[string]interface{}{}
			for key, value := range ib {
				if key != "users" {
					actualBase[key] = value
				}
			}
			keys := map[string]bool{}
			valid := true
			found := false
			for _, actual := range actualUsers {
				user, ok := actual.(map[string]interface{})
				if !ok {
					valid = false
					continue
				}
				key := relayAuthenticationKey(target.Type, user)
				if key == "" || keys[key] {
					valid = false
				}
				keys[key] = true
				if relayAppliedObjectMatches(singbox.UserConfig(target.Type, credential, base), user) {
					found = true
				}
			}
			matched = valid && found && statsPresent && relayAppliedObjectMatches(expectedBase, actualBase)

		}
		next, appliedAt := "", int64(0)
		switch state {
		case "restoring", "active":
			next = "restoring"
			if matched && targetCount == 1 {
				next = "active"
				appliedAt = now
			}
		}
		if next != "" && next != state {
			if _, err = tx.Exec(`UPDATE relay_user_retirements SET state=?,applied_at=? WHERE relay_user_id=? AND state=?`, next, appliedAt, u.ID, state); err != nil {
				return err
			}
		}
	}
	return nil
}

func retiredRelayUserIDs(db txLike) (map[int64]bool, error) {
	rows, err := db.Query(`SELECT relay_user_id FROM relay_user_retirements WHERE state IN ('retiring','retired')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]bool{}
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
