package store

import (
	"database/sql"
	"encoding/json"
	"strings"
)

const relaySemanticSpecPrefix = "json-v1:"

type relaySpecHashes struct {
	current, previous, legacy string
	managedCertificate        bool
}

// Match the production map decoder/marshaller: ignore whitespace, object key
// order and numeric spelling while retaining array order and actual values.
func canonicalRelayJSONObject(raw string) (string, error) {
	object := map[string]interface{}{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &object); err != nil {
			return "", err
		}
	}
	if object == nil {
		object = map[string]interface{}{}
	}
	canonical, err := json.Marshal(object)
	return string(canonical), err
}

// Only translate a legacy hash when it still proves the current desired raw
// configuration. An arbitrary/stale hash is never blessed as equivalent. Old
// cert_id-only hashes could not prove certificate contents, so those retain the
// same credentials but must re-accept the actual target before source switching.
func upgradeRelaySpecHash(tx *sql.Tx, link *RelayMeteringLink, hashes relaySpecHashes) (bool, error) {
	if strings.HasPrefix(link.SpecHash, relaySemanticSpecPrefix) ||
		(link.SpecHash != hashes.previous && link.SpecHash != hashes.legacy) {
		return false, nil
	}
	unprovenCertificate := hashes.managedCertificate && link.SpecHash != hashes.previous
	if unprovenCertificate {
		if _, err := tx.Exec(`UPDATE relay_metering_users SET state='prepared',accepted_at=0,activated_at=0 WHERE link_id=? AND generation=? AND enabled=1`, link.ID, link.Generation); err != nil {
			return false, err
		}
		if _, err := tx.Exec(`UPDATE relay_metering_links SET spec_hash=?,state='prepared',accepted_at=0,activated_at=0 WHERE id=?`, hashes.current, link.ID); err != nil {
			return false, err
		}
		link.State, link.AcceptedAt, link.ActivatedAt = "prepared", 0, 0
	} else {
		if _, err := tx.Exec(`UPDATE relay_metering_links SET spec_hash=? WHERE id=?`, hashes.current, link.ID); err != nil {
			return false, err
		}
	}
	link.SpecHash = hashes.current
	return true, nil
}

func (s *Store) migrateRelayProtocolAuthVerification(tx *sql.Tx) error {
	if _, err := tx.Exec(relayProtocolAuthSchema); err != nil {
		return err
	}
	links, err := relayMeteringLinksWith(tx)
	if err != nil {
		return err
	}
	for _, link := range links {
		target, err := meteringInboundWith(tx, link.TargetInboundID)
		if err != nil {
			return err
		}
		if target == nil || target.ServerID != link.TargetServerID {
			continue
		}
		hashes, _, _, err := s.relayTargetSpecHashesWith(tx, target)
		if err != nil {
			// A previously broken target remains unproven. Do not reinterpret a
			// missing certificate/server as a valid new hash or block upgrades.
			continue
		}
		if _, err = upgradeRelaySpecHash(tx, link, hashes); err != nil {
			return err
		}
	}
	return nil
}
