package store

import (
	"database/sql"
	"encoding/json"
)

// Resolve the immutable P1 namespace in one indexed lookup and insert its
// frozen owners in one statement. Other namespaces retain the existing exact
// collision/provenance/entitlement resolver. This is statement batching only:
// every counter still has its own durable binding and observation.
func (s *Store) bindTrafficIdentities(tx *sql.Tx, p TrafficPoll) error {
	names := make([]string, 0, len(p.Traffic))
	for name := range p.Traffic {
		names = append(names, name)
	}
	raw, err := json.Marshal(names)
	if err != nil {
		return err
	}
	rows, err := tx.Query(`SELECT r.identity_name FROM json_each(?) wanted JOIN relay_metering_users r ON r.identity_name=wanted.value`, string(raw))
	if err != nil {
		return err
	}
	relay := map[string]bool{}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		relay[name] = true
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if len(relay) > 0 {
		if _, err = tx.Exec(`INSERT INTO traffic_poll_bindings(poll_id,counter_name,source_kind,link_id,user_id)
 SELECT ?,r.identity_name,CASE WHEN l.target_server_id=? AND r.user_id>0 THEN 'relay_user' ELSE 'unknown' END,r.link_id,CASE WHEN l.target_server_id=? AND r.user_id>0 THEN r.user_id ELSE 0 END
 FROM json_each(?) wanted JOIN relay_metering_users r ON r.identity_name=wanted.value LEFT JOIN relay_metering_links l ON l.id=r.link_id`, p.ID, p.ServerID, p.ServerID, string(raw)); err != nil {
			return err
		}
		var mismatch bool
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM traffic_poll_bindings WHERE poll_id=? AND source_kind='unknown')`, p.ID).Scan(&mismatch); err != nil {
			return err
		}
		if mismatch {
			if err = trafficGap(tx, p, "relay_source_mismatch"); err != nil {
				return err
			}
		}
	}
	for _, name := range names {
		if !relay[name] {
			if err = s.bindTrafficIdentity(tx, p, name); err != nil {
				return err
			}
		}
	}
	return nil
}

type trafficIdentityBinding struct {
	kind, bucketKind                string
	linkID, bucketID, userID, pkgID int64
}
type trafficCursor struct{ up, down, sequence int64 }
type trafficPollBatch struct {
	seen, pending map[string]bool
	bindings      map[string]trafficIdentityBinding
	cursors       map[string]trafficCursor
}

// Read once under the same writer transaction used for quota and cursor
// changes. Nothing is cached across polls, transactions, epochs or retries.
func loadTrafficPollBatch(tx *sql.Tx, p TrafficPoll) (*trafficPollBatch, error) {
	batch := &trafficPollBatch{seen: map[string]bool{}, pending: map[string]bool{}, bindings: map[string]trafficIdentityBinding{}, cursors: map[string]trafficCursor{}}
	rows, err := tx.Query(`SELECT b.counter_name,b.source_kind,b.link_id,b.bucket_id,b.user_id,b.package_id,b.bucket_kind,EXISTS(SELECT 1 FROM traffic_observations o WHERE o.poll_id=b.poll_id AND o.counter_name=b.counter_name) FROM traffic_poll_bindings b WHERE b.poll_id=?`, p.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name string
		var b trafficIdentityBinding
		var seen bool
		if err = rows.Scan(&name, &b.kind, &b.linkID, &b.bucketID, &b.userID, &b.pkgID, &b.bucketKind, &seen); err != nil {
			rows.Close()
			return nil, err
		}
		batch.bindings[name] = b
		batch.seen[name] = seen
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if p.Mode != "cumulative" {
		return batch, nil
	}
	rows, err = tx.Query(`SELECT c.counter_name,c.up,c.down,c.sequence FROM traffic_counter_cursors c JOIN traffic_poll_bindings b ON b.poll_id=? AND b.counter_name=c.counter_name WHERE c.server_id=? AND c.epoch=?`, p.ID, p.ServerID, p.Epoch)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name string
		var c trafficCursor
		if err = rows.Scan(&name, &c.up, &c.down, &c.sequence); err != nil {
			rows.Close()
			return nil, err
		}
		batch.cursors[name] = c
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	rows, err = tx.Query(`SELECT DISTINCT b.counter_name FROM traffic_polls earlier JOIN traffic_poll_bindings b ON b.poll_id=earlier.id WHERE earlier.state='pending' AND earlier.mode='cumulative' AND earlier.server_id=? AND earlier.epoch=? AND earlier.sequence<? AND NOT EXISTS(SELECT 1 FROM traffic_observations o WHERE o.poll_id=b.poll_id AND o.counter_name=b.counter_name)`, p.ServerID, p.Epoch, p.Sequence)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		batch.pending[name] = true
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	return batch, rows.Err()
}
