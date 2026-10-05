package store

// Service observations never confer permission to debit a quota. A relay is
// observation-only even if its name collides with a customer's proxy username.
import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
)

// TrafficPoll is an immutable persist-before-process journal entry. An ID
// belongs to one result, not to a request that might read different counters.
type TrafficPoll struct {
	Sequence   int64                 `json:"sequence,omitempty"`
	Transition bool                  `json:"transition,omitempty"`
	ID         string                `json:"id"`
	ServerID   int64                 `json:"server_id"`
	ObservedAt int64                 `json:"observed_at"`
	Mode       string                `json:"mode"`
	Epoch      string                `json:"epoch"`
	Baseline   bool                  `json:"baseline,omitempty"`
	Traffic    map[string]UsageDelta `json:"traffic"`
}

func NewTrafficPoll(serverID int64, traffic map[string]UsageDelta) TrafficPoll {
	return TrafficPoll{ID: uuid.NewString(), ServerID: serverID, ObservedAt: time.Now().Unix(), Mode: "reset", Traffic: traffic}
}
func (s *Store) RecordTrafficPoll(p TrafficPoll) (int, error) {
	if p.ID == "" || len(p.ID) > 128 || p.ServerID < 0 || p.Sequence < 0 || p.ObservedAt <= 0 || len(p.Traffic) > 100000 {
		return 0, fmt.Errorf("invalid traffic poll")
	}
	if p.Mode != "reset" && p.Mode != "cumulative" {
		return 0, fmt.Errorf("invalid traffic collection mode")
	}
	if p.Transition && (p.Mode != "reset" || p.Epoch == "") {
		return 0, fmt.Errorf("invalid cumulative transition")
	}
	if p.Mode == "cumulative" && (p.Epoch == "" || len(p.Epoch) > 256) {
		return 0, fmt.Errorf("cumulative traffic requires a verified process epoch")
	}
	for name, d := range p.Traffic {
		if name == "" || len(name) > 256 || strings.ContainsAny(name, "\x00\r\n") || d.Up < 0 || d.Down < 0 || d.Up > math.MaxInt64-d.Down {
			return 0, fmt.Errorf("invalid traffic counter")
		}
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return 0, err
	}
	if len(raw) > 16<<20 {
		return 0, fmt.Errorf("traffic poll too large")
	}
	h := sha256.Sum256(raw)
	digest := hex.EncodeToString(h[:])
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var stored string
	err = tx.QueryRow(`SELECT digest FROM traffic_polls WHERE id=?`, p.ID).Scan(&stored)
	if err == nil {
		if stored != digest {
			return 0, fmt.Errorf("traffic poll ID reused with different content")
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	} else {
		seq := p.Sequence
		if seq == 0 {
			if err = tx.QueryRow(`UPDATE traffic_collection_sequence SET value=value+1 WHERE id=1 RETURNING value`).Scan(&seq); err != nil {
				return 0, err
			}
		}
		_, err = tx.Exec(`INSERT INTO traffic_polls(id,sequence,server_id,observed_at,mode,epoch,payload,digest,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, p.ID, seq, p.ServerID, p.ObservedAt, p.Mode, p.Epoch, string(raw), digest, time.Now().Unix())
		if err != nil {
			return 0, err
		}
		if err = s.bindTrafficIdentities(tx, p); err != nil {
			return 0, err
		}
		// A reset intent is cleared only by the same transaction that durably
		// journals its response and frozen owners. Quota processing may still
		// fail afterwards without losing the destructive response.
		if _, err = tx.Exec(`DELETE FROM traffic_metering_gaps WHERE server_id=? AND poll_id=? AND reason='reset_outcome_unknown'`, p.ServerID, p.ID); err != nil {
			return 0, err
		}
		// The destructive boundary is complete once its immutable response is safe,
		// independently of per-identity processing. A retry must never reset again.
		if p.Transition {
			_, err = tx.Exec(`INSERT INTO traffic_metering_state(server_id,mode,epoch,last_sequence,last_attempt,status) VALUES(?,'cumulative',?,?,?,'transitioning') ON CONFLICT(server_id) DO UPDATE SET mode='cumulative',epoch=excluded.epoch,last_sequence=excluded.last_sequence,last_attempt=MAX(last_attempt,excluded.last_attempt),status='transitioning' WHERE excluded.last_sequence>last_sequence`, p.ServerID, p.Epoch, seq, p.ObservedAt)
			if err != nil {
				return 0, err
			}
		}
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}

	return s.processTrafficPoll(p.ID)
}

// NextTrafficSequence is reserved before a network read, so wall-clock changes
// and late journal retries cannot reorder snapshots. Unused reservations are safe.
func (s *Store) NextTrafficSequence() (int64, error) {
	var seq int64
	err := s.db.QueryRow(`UPDATE traffic_collection_sequence SET value=value+1 WHERE id=1 RETURNING value`).Scan(&seq)
	return seq, err
}

// BeginTrafficReset records the possibility of a destructive read BEFORE it
// reaches the remote core. A crash, timeout, or failed journal commit leaves a
// durable coverage warning; successful persistence clears it atomically above.
// This makes uncertainty visible, but cannot reconstruct a lost reset response.
func (s *Store) BeginTrafficReset(p TrafficPoll) error {
	if p.ID == "" || p.ServerID < 0 || p.Mode != "reset" || p.ObservedAt <= 0 {
		return fmt.Errorf("invalid reset intent")
	}
	_, err := s.db.Exec(`INSERT INTO traffic_metering_gaps(server_id,ts,reason,poll_id) VALUES(?,?,'reset_outcome_unknown',?)`, p.ServerID, p.ObservedAt, p.ID)
	return err
}

// RetryPendingTrafficPolls replays persisted results, never resetting a node.
func (s *Store) RetryPendingTrafficPolls() (int, error) {
	rows, err := s.db.Query(`SELECT id FROM traffic_polls WHERE state='pending' ORDER BY sequence LIMIT 100`)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err = rows.Close(); err != nil {
		return 0, err
	}
	if err = rows.Err(); err != nil {
		return 0, err
	}
	total := 0
	var errs []error
	for _, id := range ids {
		n, e := s.processTrafficPoll(id)
		total += n
		if e != nil {
			errs = append(errs, e)
		}
	}
	return total, errors.Join(errs...)
}
func (s *Store) processTrafficPoll(id string) (int, error) {
	var raw, state string
	var sequence int64
	if err := s.db.QueryRow(`SELECT payload,state,sequence FROM traffic_polls WHERE id=?`, id).Scan(&raw, &state, &sequence); err != nil {
		return 0, err
	}
	if state == "done" {
		return 0, nil
	}
	var p TrafficPoll
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return 0, err
	}
	p.Sequence = sequence
	targets, bindingErr := s.boundTrafficAccountTargets(p.ID)
	if targets == nil && bindingErr != nil {
		return 0, bindingErr
	}

	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if err = tx.QueryRow(`SELECT state FROM traffic_polls WHERE id=?`, id).Scan(&state); err != nil {
		return 0, err
	}
	if state == "done" {
		return 0, nil
	}
	// Freeze selected account buckets outside per-identity savepoints, so an
	// update failure cannot make a later retry choose another entitlement.
	for userID, b := range targets {
		if _, err = tx.Exec(`UPDATE traffic_poll_bindings SET bucket_id=?,package_id=?,bucket_kind=? WHERE poll_id=? AND user_id=? AND bucket_id=0 AND source_kind='direct_user'`, b.ID, b.PackageID, b.Kind, p.ID, userID); err != nil {
			return 0, err
		}
	}
	if p.Mode == "cumulative" {
		var epoch, mode string
		var seq int64
		e := tx.QueryRow(`SELECT epoch,mode,last_sequence FROM traffic_metering_state WHERE server_id=?`, p.ServerID).Scan(&epoch, &mode, &seq)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return 0, e
		}
		if e == nil && mode == "cumulative" && epoch != "" && epoch != p.Epoch && p.Sequence > seq {
			if e = trafficGap(tx, p, "process_restart_tail_unknown"); e != nil {
				return 0, e
			}
		}
	}
	batch, err := loadTrafficPollBatch(tx, p)
	if err != nil {
		return 0, err
	}
	names := make([]string, 0, len(p.Traffic))
	for name := range p.Traffic {
		names = append(names, name)
	}
	sort.Strings(names)
	applied := 0
	firstErr := bindingErr
	for _, name := range names {
		if batch.seen[name] {
			continue
		}
		if _, err = tx.Exec(`SAVEPOINT traffic_identity`); err != nil {
			return 0, err
		}
		n, e := s.processTrafficIdentity(tx, p, name, batch)
		if e != nil {
			_, _ = tx.Exec(`ROLLBACK TO traffic_identity`)
			_, _ = tx.Exec(`RELEASE traffic_identity`)
			if firstErr == nil {
				firstErr = e
			}
			continue
		}
		if _, err = tx.Exec(`RELEASE traffic_identity`); err != nil {
			return 0, err
		}
		applied += n
	}
	state = "done"
	msg := ""
	if firstErr != nil {
		state = "pending"
		msg = "用量入库失败，已保留原始采集等待重试"
	}
	if _, err = tx.Exec(`UPDATE traffic_polls SET state=?,error=? WHERE id=?`, state, msg, id); err != nil {
		return 0, err
	}
	mode, status := p.Mode, "ok"
	lastSuccess := p.ObservedAt
	if p.Transition {
		mode, status = "cumulative", "transitioning"
	}
	if firstErr != nil {
		status = "pending"
		lastSuccess = 0
	}
	// Receipt order determines the current epoch. Historical replay still credits
	// its own epoch cursor but cannot replace a newer process or lower success time.
	_, err = tx.Exec(`INSERT INTO traffic_metering_state(server_id,mode,epoch,last_sequence,last_success,last_attempt,failures,status,error) VALUES(?,?,?,?,?,?,0,?,?) ON CONFLICT(server_id) DO UPDATE SET mode=CASE WHEN mode='cumulative' THEN mode ELSE excluded.mode END,epoch=excluded.epoch,last_sequence=excluded.last_sequence,last_success=MAX(last_success,excluded.last_success),last_attempt=MAX(last_attempt,excluded.last_attempt),failures=0,status=excluded.status,error=excluded.error WHERE excluded.last_sequence>=last_sequence`, p.ServerID, mode, p.Epoch, p.Sequence, lastSuccess, p.ObservedAt, status, msg)
	if err != nil {
		return 0, err
	}

	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return applied, firstErr
}
func trafficGap(tx *sql.Tx, p TrafficPoll, reason string) error {
	_, err := tx.Exec(`INSERT OR IGNORE INTO traffic_metering_gaps(server_id,ts,reason,poll_id) VALUES(?,?,?,?)`, p.ServerID, p.ObservedAt, reason, p.ID)
	return err
}
func (s *Store) processTrafficIdentity(tx *sql.Tx, p TrafficPoll, name string, batch *trafficPollBatch) (int, error) {
	d := p.Traffic[name]
	quality := "observed"
	if p.Mode == "cumulative" {
		// A failed earlier observation still owns its interval and its frozen
		// billing identity. Advancing this shared counter past it could charge
		// that interval to a later name owner, then make its retry look stale.
		// Only block the same counter/epoch; other identities in a partial poll
		// and observations from a restarted process can continue independently.
		if batch.pending[name] {
			return 0, fmt.Errorf("earlier cumulative observation is pending; retain this counter for ordered retry")
		}
		cursor, exists := batch.cursors[name]
		up, down, seq := cursor.up, cursor.down, cursor.sequence
		var e error
		switch {
		case !exists:
			if p.Baseline {
				d = UsageDelta{}
				quality = "baseline"
				if e = trafficGap(tx, p, "unanchored_baseline"); e != nil {
					return 0, e
				}
			}
		case p.Sequence <= seq:
			d = UsageDelta{}
			quality = "out_of_order"
			if e = trafficGap(tx, p, "out_of_order_snapshot"); e != nil {
				return 0, e
			}
		case d.Up < up || d.Down < down:
			// A decrease without a verified new process epoch is ambiguous (external
			// reset or stale response). Never lower the high-water mark and re-charge
			// already accounted stock. Coverage stays incomplete until a new epoch.
			d = UsageDelta{}
			quality = "counter_reset"
			if e = trafficGap(tx, p, "unexpected_counter_reset"); e != nil {
				return 0, e
			}
		default:
			d.Up -= up
			d.Down -= down
		}
		if quality != "out_of_order" {
			raw := p.Traffic[name]
			if quality == "counter_reset" {
				raw = UsageDelta{Up: up, Down: down}
			}
			if _, e = tx.Exec(`INSERT INTO traffic_counter_cursors(server_id,counter_name,epoch,up,down,observed_at,sequence) VALUES(?,?,?,?,?,?,?) ON CONFLICT(server_id,counter_name,epoch) DO UPDATE SET up=excluded.up,down=excluded.down,observed_at=excluded.observed_at,sequence=excluded.sequence`, p.ServerID, name, p.Epoch, raw.Up, raw.Down, p.ObservedAt, p.Sequence); e != nil {
				return 0, e
			}
		}

	}
	binding, exists := batch.bindings[name]
	if !exists {
		return 0, fmt.Errorf("frozen traffic binding unavailable; retain poll for retry")
	}
	kind, bucketKind := binding.kind, binding.bucketKind
	linkID, bucketID, userID, pkgID := binding.linkID, binding.bucketID, binding.userID, binding.pkgID
	var err error
	billable := kind == "direct_user"
	if billable && bucketID == 0 {
		return 0, fmt.Errorf("account billing target unavailable; owner retained for retry")
	}
	if kind == "ambiguous_identity" {
		quality = "identity_collision"
	}

	if d.Up > 0 || d.Down > 0 {
		if billable {
			if err = safeApplyBucketUsage(tx, bucketID, userID, pkgID, bucketKind, d, p.ObservedAt); err != nil {
				return 0, err
			}
			if _, err = tx.Exec(`INSERT INTO server_user_traffic_samples(server_id,user_id,ts,up,down) VALUES(?,?,?,?,?)`, p.ServerID, userID, p.ObservedAt, d.Up, d.Down); err != nil {
				return 0, err
			}
		}
		day := time.Unix(p.ObservedAt, 0).UTC().Format("2006-01-02")
		if _, err = tx.Exec(`INSERT INTO machine_traffic_daily(day,server_id,source_kind,link_id,user_id,up,down) VALUES(?,?,?,?,?,?,?) ON CONFLICT(day,server_id,source_kind,link_id,user_id) DO UPDATE SET up=up+excluded.up,down=down+excluded.down`, day, p.ServerID, kind, linkID, userID, d.Up, d.Down); err != nil {
			return 0, err
		}
	}
	_, err = tx.Exec(`INSERT INTO traffic_observations(poll_id,counter_name,server_id,ts,source_kind,link_id,user_id,up,down,quality,billable) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, p.ID, name, p.ServerID, p.ObservedAt, kind, linkID, userID, d.Up, d.Down, quality, billable && bucketKind != KindFree)
	if err != nil {
		return 0, err
	}
	if billable && d.Up+d.Down > 0 {
		return 1, nil
	}
	return 0, nil
}
func safeApplyBucketUsage(tx *sql.Tx, bucketID, userID, pkgID int64, kind string, d UsageDelta, at int64) error {
	var up, down int64
	if err := tx.QueryRow(`SELECT used_up,used_down FROM user_plans WHERE id=?`, bucketID).Scan(&up, &down); err != nil {
		return err
	}
	if up < 0 || down < 0 || d.Up > math.MaxInt64-up || d.Down > math.MaxInt64-down || up+d.Up > math.MaxInt64-(down+d.Down) {
		return fmt.Errorf("traffic quota counter overflow")
	}
	if kind != KindFree {
		if err := tx.QueryRow(`SELECT used_up,used_down FROM users WHERE id=?`, userID).Scan(&up, &down); err != nil {
			return err
		}
		if up < 0 || down < 0 || d.Up > math.MaxInt64-up || d.Down > math.MaxInt64-down || up+d.Up > math.MaxInt64-(down+d.Down) {
			return fmt.Errorf("traffic user aggregate overflow")
		}
	}
	return applyBucketUsage(tx, bucketID, userID, pkgID, kind, d.Up, d.Down, at)
}

// A name's spelling is not proof that its traffic belongs to a relay. Match
// durable generations first, including retired generations and other machines.
// Only the old shared namespace lacks a durable history after inbound deletion;
// its exact generated names therefore remain nonbillable when provenance is lost.
func relayObservationIdentity(tx txLike, serverID int64, name string) (kind string, linkID int64, gap string, err error) {
	if strings.HasPrefix(name, "outbound:") {
		var id int64
		parts := strings.Split(strings.TrimPrefix(name, "outbound:"), "-")
		if len(parts) >= 4 && parts[0] == "relay" && parts[1] == "link" {
			link, linkErr := strconv.ParseInt(parts[2], 10, 64)
			if linkErr == nil && link > 0 && strconv.FormatInt(link, 10) == parts[2] {
				if len(parts) == 4 && strings.HasPrefix(parts[3], "g") {
					generation, e := strconv.Atoi(strings.TrimPrefix(parts[3], "g"))
					if e == nil && parts[3] == fmt.Sprintf("g%d", generation) {
						err = tx.QueryRow(`SELECT l.id FROM relay_metering_links l JOIN relay_metering_generations g ON g.link_id=l.id WHERE l.id=? AND l.source_server_id=? AND g.generation=?`, link, serverID, generation).Scan(&id)
					}
				} else if len(parts) == 5 && strings.HasPrefix(parts[3], "u") && strings.HasPrefix(parts[4], "g") {
					owner, e1 := strconv.ParseInt(strings.TrimPrefix(parts[3], "u"), 10, 64)
					generation, e2 := strconv.Atoi(strings.TrimPrefix(parts[4], "g"))
					if e1 == nil && e2 == nil && parts[3] == fmt.Sprintf("u%d", owner) && parts[4] == fmt.Sprintf("g%d", generation) {
						err = tx.QueryRow(`SELECT l.id FROM relay_metering_links l JOIN relay_metering_users u ON u.link_id=l.id WHERE l.id=? AND l.source_server_id=? AND u.user_id=? AND u.generation=?`, link, serverID, owner, generation).Scan(&id)
					}
				}
			}
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", 0, "", err
		}
		return "diagnostic_outbound", id, "", nil
	}
	var legacyServerID int64
	err = tx.QueryRow(`SELECT server_id FROM legacy_relay_identities WHERE identity_name=?`, name).Scan(&legacyServerID)
	if err == nil {
		if legacyServerID != serverID {
			return "unknown", 0, "relay_source_mismatch", nil
		}
		return "legacy_shared_relay", 0, "", nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", 0, "", err
	}
	var id int64
	var targetServerID sql.NullInt64
	err = tx.QueryRow(`SELECT g.link_id,l.target_server_id FROM relay_metering_generations g LEFT JOIN relay_metering_links l ON l.id=g.link_id WHERE g.identity_name=?`, name).Scan(&id, &targetServerID)
	if err == nil {
		if !targetServerID.Valid || targetServerID.Int64 != serverID {
			return "unknown", id, "relay_source_mismatch", nil
		}
		return "relay_link", id, "", nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", 0, "", err
	}
	if inboundID, ok := legacyRelayInboundID(name); ok {
		var target int64
		var secret string
		err = tx.QueryRow(`SELECT server_id,relay_secret FROM sb_inbounds WHERE id=?`, inboundID).Scan(&target, &secret)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return "", 0, "", err
		}
		if err == nil && target == serverID && secret != "" {
			return "legacy_shared_relay", 0, "", nil
		}
		// Deleted/moved inbounds must not turn a late shared-relay sample into
		// a customer's similarly named legacy proxy account. Make the unresolved
		// attribution visible even though the observation itself can be stored.
		return "unknown", 0, "legacy_relay_provenance_unknown", nil
	}
	// Existing qzr_* and non-system relay_* customer names predate the new
	// validation rule. Generation creation checks those owners for collisions,
	// so an unmapped name must still reach the ordinary customer resolver.
	return "", 0, "", nil
}

func legacyRelayInboundID(name string) (int64, bool) {
	if !strings.HasPrefix(name, "relay_") {
		return 0, false
	}
	suffix := strings.TrimPrefix(name, "relay_")
	id, err := strconv.ParseInt(suffix, 10, 64)
	// Match only what fmt.Sprintf("relay_%d", inbound.ID) can generate. In
	// particular, valid customer route/alias suffixes are not shared relays.
	return id, err == nil && id > 0 && strconv.FormatInt(id, 10) == suffix
}

// Category-only messages avoid exposing raw SSH/config/credential diagnostics.
func (s *Store) RecordTrafficCollectionFailure(serverID int64, status string) error {
	switch status {
	case "unavailable", "unsupported", "disabled", "legacy", "legacy_identity_unverified":
	default:
		status = "unavailable"
	}
	message := "用户统计未成功采集；缺失不是零流量"
	if status == "legacy_identity_unverified" {
		message = "旧 relay_数字 自定义账号尚无运行配置切换证明；该类流量暂未归属，其他账号继续采集"
	}
	_, err := s.db.Exec(`INSERT INTO traffic_metering_state(server_id,last_attempt,failures,status,error) VALUES(?,?,1,?,?) ON CONFLICT(server_id) DO UPDATE SET last_attempt=excluded.last_attempt,failures=failures+1,status=excluded.status,error=excluded.error`, serverID, time.Now().Unix(), status, message)
	return err
}

// The lease bounds destructive mode handover across multiple panel processes.
func (s *Store) AcquireTrafficLease(serverID int64, owner string) (bool, error) {
	now := time.Now().Unix()
	r, err := s.db.Exec(`INSERT INTO traffic_collection_leases(server_id,owner,expires_at) VALUES(?,?,?) ON CONFLICT(server_id) DO UPDATE SET owner=excluded.owner,expires_at=excluded.expires_at WHERE expires_at<=? OR owner=?`, serverID, owner, now+60, now, owner)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}
func (s *Store) ReleaseTrafficLease(serverID int64, owner string) {
	_, _ = s.db.Exec(`DELETE FROM traffic_collection_leases WHERE server_id=? AND owner=?`, serverID, owner)
}
func (s *Store) RecordTrafficBoundaryGap(serverID int64, reason string) error {
	switch reason {
	case "transition_reset_uncertain", "process_changed_during_read", "cumulative_transition_incomplete", "planned_config_restart_tail":
	default:
		return fmt.Errorf("invalid metering gap")
	}
	_, err := s.db.Exec(`INSERT INTO traffic_metering_gaps(server_id,ts,reason,poll_id) VALUES(?,?,?,?)`, serverID, time.Now().Unix(), reason, uuid.NewString())
	return err
}
