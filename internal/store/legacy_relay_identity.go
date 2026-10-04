package store

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const legacyRelayIdentitySchema = `
CREATE TABLE legacy_relay_identities (
 server_id INTEGER NOT NULL, inbound_id INTEGER NOT NULL,
 identity_name TEXT NOT NULL UNIQUE, created_at INTEGER NOT NULL,
 PRIMARY KEY(server_id,inbound_id)
);
CREATE TABLE relay_namespace_epochs (
 server_id INTEGER NOT NULL, epoch TEXT NOT NULL, config_hash TEXT NOT NULL,
 applied_at INTEGER NOT NULL, PRIMARY KEY(server_id,epoch)
);
CREATE INDEX idx_traffic_polls_server_epoch_pending ON traffic_polls(server_id,epoch,state,sequence);
`

// Only the server's internal statistics label changes. The old shared wire
// credential and every customer's login name, UUID and password stay intact.
func (s *Store) legacyRelayStatsName(serverID, inboundID int64) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var name string
	err = tx.QueryRow(`SELECT identity_name FROM legacy_relay_identities WHERE server_id=? AND inbound_id=?`, serverID, inboundID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		random := make([]byte, 12)
		_, err = rand.Read(random)
		name = "qzr_s_" + hex.EncodeToString(random)
		if err == nil {
			var collision bool
			err = tx.QueryRow(`SELECT EXISTS(
 SELECT 1 FROM users WHERE proxy_username=? AND proxy_username<>'' UNION ALL SELECT 1 FROM users WHERE client_name=?
 UNION ALL SELECT 1 FROM user_plans WHERE proxy_username=? AND proxy_username<>'' UNION ALL SELECT 1 FROM user_plans WHERE client_name=?
 UNION ALL SELECT 1 FROM plan_identities WHERE proxy_username=? AND proxy_username<>'' UNION ALL SELECT 1 FROM plan_identities WHERE client_name=?)`, name, name, name, name, name, name).Scan(&collision)
			if err == nil && collision {
				err = fmt.Errorf("internal statistics identity collides with an existing customer")
			}
		}
		if err == nil {
			_, err = tx.Exec(`INSERT INTO legacy_relay_identities(server_id,inbound_id,identity_name,created_at) VALUES(?,?,?,?)`, serverID, inboundID, name, time.Now().Unix())
		}
	}
	if err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return name, nil
}

// HasLegacyNumericCustomers is a capability preflight, not an ownership lookup.
// These pre-existing mixed names need a verified applied-process boundary before
// a former shared statistics name can be safely interpreted as a customer.
func (s *Store) HasLegacyNumericCustomers() (bool, error) {
	rows, err := s.db.Query(`SELECT proxy_username FROM users WHERE proxy_username LIKE 'relay_%' UNION SELECT proxy_username FROM user_plans WHERE proxy_username LIKE 'relay_%' UNION SELECT proxy_username FROM plan_identities WHERE proxy_username LIKE 'relay_%'`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return false, err
		}
		if _, ok := legacyRelayInboundID(name); ok {
			return true, nil
		}
	}
	return false, rows.Err()
}

// RecordRelayNamespaceEpoch receives an epoch verified by the controller AFTER
// successful config application. Desired JSON or a wall-clock timestamp alone
// can never establish this boundary. Old proven epochs remain valid for delayed
// polls; no existing poll binding is reclassified.
func (s *Store) RecordRelayNamespaceEpoch(serverID int64, epoch string, raw []byte) error {
	if epoch == "" {
		return fmt.Errorf("missing verified process epoch")
	}
	var cfg struct {
		Inbounds []struct {
			Users []map[string]any `json:"users"`
		} `json:"inbounds"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return err
	}
	for _, ib := range cfg.Inbounds {
		for _, u := range ib.Users {
			if name, ok := u["name"].(string); ok {
				if _, old := legacyRelayInboundID(name); old {
					return fmt.Errorf("running config still contains an unseparated shared statistics name")
				}
			}
		}
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	_, err := s.db.Exec(`INSERT INTO relay_namespace_epochs(server_id,epoch,config_hash,applied_at) VALUES(?,?,?,?) ON CONFLICT(server_id,epoch) DO NOTHING`, serverID, epoch, hash, time.Now().Unix())
	if err != nil {
		return err
	}
	return s.ConfirmRelayNamespaceEpoch(serverID, epoch, hash)
}

func legacyRelayStatsNameWith(db txLike, serverID, inboundID int64) (string, error) {
	var name string
	err := db.QueryRow(`SELECT identity_name FROM legacy_relay_identities WHERE server_id=? AND inbound_id=?`, serverID, inboundID).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Sprintf("relay_%d", inboundID), nil
	}
	return name, err
}

// RelayNamespaceProofKnown permits a no-op only for this exact previously
// proven process and desired configuration. Unknown external restarts need a
// fresh controlled apply, even if the disk file still has an old proven hash.
func (s *Store) RelayNamespaceProofKnown(serverID int64, epoch string, raw []byte) (bool, error) {
	sum := sha256.Sum256(raw)
	var known bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM relay_namespace_epochs WHERE server_id=? AND epoch=? AND config_hash=?)`, serverID, epoch, hex.EncodeToString(sum[:])).Scan(&known)
	return known, err
}

func (s *Store) LatestRelayNamespaceProof(serverID int64) (string, error) {
	var hash string
	err := s.db.QueryRow(`SELECT config_hash FROM relay_namespace_epochs WHERE server_id=? ORDER BY applied_at DESC,rowid DESC LIMIT 1`, serverID).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return hash, err
}
func (s *Store) RelayNamespaceEpochKnown(serverID int64, epoch string) (bool, error) {
	var known bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM relay_namespace_epochs WHERE server_id=? AND epoch=?)`, serverID, epoch).Scan(&known)
	return known, err
}

// ConfirmRelayNamespaceEpoch checks an existing proof; it cannot promote an
// unknown epoch based on a reused on-disk hash.
func (s *Store) ConfirmRelayNamespaceEpoch(serverID int64, epoch, hash string) error {
	var known bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM relay_namespace_epochs WHERE server_id=? AND epoch=? AND config_hash=?)`, serverID, epoch, hash).Scan(&known)
	if err != nil {
		return err
	}
	if !known {
		return fmt.Errorf("unverified process epoch/configuration")
	}
	return nil
}

// HasRelayNamespaceProof scopes legacy epoch reads to nodes on which a numeric
// customer namespace has actually been deployed, including its delayed history.
func (s *Store) HasRelayNamespaceProof(serverID int64) (bool, error) {
	var known bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM relay_namespace_epochs WHERE server_id=?)`, serverID).Scan(&known)
	return known, err
}
