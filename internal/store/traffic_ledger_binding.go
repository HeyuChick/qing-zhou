package store

import (
	"database/sql"
	"errors"
)

// Ownership is captured with the immutable journal entry, before any quota
// update can fail. Mutable proxy names are never resolved again during replay.
func (s *Store) bindTrafficIdentity(tx *sql.Tx, p TrafficPoll, name string) error {
	var kind, gap string
	var bucketID, userID, pkgID, linkID int64
	var bucketKind string
	var targetServerID sql.NullInt64
	// Per-user hop identities are immutable and remain resolvable after a user
	// leaves a route or a credential generation stops accepting connections.
	// Their bytes are measured on this machine, but never debit an entitlement.
	err := tx.QueryRow(`SELECT r.link_id,r.user_id,l.target_server_id FROM relay_metering_users r LEFT JOIN relay_metering_links l ON l.id=r.link_id WHERE r.identity_name=?`, name).Scan(&linkID, &userID, &targetServerID)
	if err == nil {
		kind = "relay_user"
		if !targetServerID.Valid || targetServerID.Int64 != p.ServerID || userID <= 0 {
			kind, gap, userID = "unknown", "relay_source_mismatch", 0
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		separated := false
		if _, legacyName := legacyRelayInboundID(name); legacyName && p.Epoch != "" {
			if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM relay_namespace_epochs WHERE server_id=? AND epoch=?)`, p.ServerID, p.Epoch).Scan(&separated); err != nil {
				return err
			}
		}
		if !separated {
			kind, linkID, gap, err = relayObservationIdentity(tx, p.ServerID, name)
			if err != nil {
				return err
			}
		}
	} else {
		return err
	}
	if kind == "legacy_shared_relay" {
		// These exact probes use the existing identity indexes. A line and its
		// historical buckets may both match: any customer owner conflicts with
		// this proven relay, but duplicate rows alone never invent another owner.
		var collision bool
		err = tx.QueryRow(`SELECT EXISTS(
 SELECT 1 FROM users WHERE proxy_username=? AND proxy_username<>''
 UNION ALL SELECT 1 FROM users WHERE client_name=?
 UNION ALL SELECT 1 FROM user_plans WHERE proxy_username=? AND proxy_username<>''
 UNION ALL SELECT 1 FROM user_plans WHERE client_name=?
 UNION ALL SELECT 1 FROM plan_identities WHERE proxy_username=? AND proxy_username<>''
 UNION ALL SELECT 1 FROM plan_identities WHERE client_name=?)`, name, name, name, name, name, name).Scan(&collision)
		if err != nil {
			return err
		}
		if collision {
			kind = "ambiguous_identity"
			gap = "relay_identity_collision"
		}
	}
	// Preserve the classification boundary with the original response. A later
	// rename or topology edit cannot make a replay charge an uncertain sample.
	if gap != "" {
		if err = trafficGap(tx, p, gap); err != nil {
			return err
		}
	}
	if kind == "" {
		canonical, _ := canonicalStatsIdentity(name)
		bucketID, userID, pkgID, bucketKind, err = s.resolveStatsIdentity(tx, canonical, map[string]*Bucket{})
		if errors.Is(err, sql.ErrNoRows) {
			// Account identities don't name a bucket. Freeze their owner now; the
			// existing entitlement selector chooses and freezes a bucket before debit.
			userID, err = accountUserID(tx, canonical)
		}
		if errors.Is(err, sql.ErrNoRows) {
			kind = "unknown"
			userID = 0
		} else if err != nil {
			return err
		} else {
			kind = "direct_user"
		}
	}
	_, err = tx.Exec(`INSERT INTO traffic_poll_bindings(poll_id,counter_name,source_kind,link_id,user_id,bucket_id,package_id,bucket_kind) VALUES(?,?,?,?,?,?,?,?)`, p.ID, name, kind, linkID, userID, bucketID, pkgID, bucketKind)
	return err
}

func (s *Store) boundTrafficAccountTargets(pollID string) (map[int64]*Bucket, error) {
	rows, err := s.db.Query(`SELECT DISTINCT user_id FROM traffic_poll_bindings WHERE poll_id=? AND source_kind='direct_user' AND bucket_id=0`, pollID)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := map[int64]*Bucket{}
	var firstErr error
	for _, id := range ids {
		b, e := s.accountMeterBucket(id)
		if e != nil {
			if firstErr == nil {
				firstErr = e
			}
			continue
		}
		out[id] = b
	}
	return out, firstErr
}
