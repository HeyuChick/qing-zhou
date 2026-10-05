package store

import (
	"database/sql"
	"encoding/json"

	"qingzhou/internal/subconv"
)

const sourceNodeKeyAliasSchema = `
CREATE TABLE node_source_key_aliases (
 source_id INTEGER NOT NULL REFERENCES node_sources(id) ON DELETE CASCADE,
 current_key TEXT NOT NULL, legacy_key TEXT NOT NULL,
 PRIMARY KEY(source_id,current_key,legacy_key)
);
`

// Candidates are not authority: each must match a node key genuinely present
// in this source's old cache in this writer transaction. Host/credential edits,
// another source's identical display name, and URI-supplied alias claims cannot
// manufacture that evidence. No user preference rows are modified on refresh.
func (s *Store) prepareSourceNodeAliases(tx *sql.Tx, sourceID int64, nodes []Node) (map[string]map[string]bool, error) {
	old := map[string]map[string]bool{}
	rows, err := tx.Query(`SELECT share_link,remark FROM nodes WHERE source_id=?`, sourceID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var raw, remark string
		if err = rows.Scan(&raw, &remark); err != nil {
			rows.Close()
			return nil, err
		}
		keys := subconv.NodeKeys(raw)
		displayKeys := subconv.NodeKeys(subconv.WithLinkRemark(raw, remark))
		for _, key := range keys {
			if old[key] == nil {
				old[key] = map[string]bool{}
			}
			for _, prior := range append(keys, displayKeys...) {
				old[key][prior] = true
			}
		}
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	existing := map[string]map[string]bool{}
	rows, err = tx.Query(`SELECT current_key,legacy_key FROM node_source_key_aliases WHERE source_id=?`, sourceID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var current, legacy string
		if err = rows.Scan(&current, &legacy); err != nil {
			rows.Close()
			return nil, err
		}
		if existing[current] == nil {
			existing[current] = map[string]bool{}
		}
		existing[current][legacy] = true
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	out := map[string]map[string]bool{}
	for _, node := range nodes {
		current := subconv.NodeKey(node.ShareLink)
		// Previously proved aliases survive a temporary source omission; the
		// current hash includes host/credentials and cannot name a changed node.
		if prior := existing[current]; len(prior) > 0 {
			out[current] = map[string]bool{}
			for key := range prior {
				out[current][key] = true
			}
		}
		candidates := append([]string{current}, node.ImportLegacyKeys...)
		for _, candidate := range candidates {
			evidence := old[candidate]
			if evidence == nil {
				continue
			}
			if out[current] == nil {
				out[current] = map[string]bool{}
			}
			for key := range evidence {
				if key != current {
					out[current][key] = true
				}
			}
			for key := range existing[candidate] {
				if key != current {
					out[current][key] = true
				}
			}
		}
	}
	return out, nil
}
func (s *Store) replaceSourceNodeAliases(tx *sql.Tx, sourceID int64, aliases map[string]map[string]bool) error {
	// Keep proved lineage as a tombstone until the source itself is deleted.
	// Refresh omissions must not erase a user's older disabled-node choice.
	for current, legacy := range aliases {
		for key := range legacy {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO node_source_key_aliases(source_id,current_key,legacy_key) VALUES(?,?,?)`, sourceID, current, key); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Store) SourceNodeKeyAliases(sourceIDs []int64) (map[int64]map[string][]string, error) {
	out := map[int64]map[string][]string{}
	if len(sourceIDs) == 0 {
		return out, nil
	}
	raw, err := json.Marshal(sourceIDs)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT source_id,current_key,legacy_key FROM node_source_key_aliases WHERE source_id IN (SELECT value FROM json_each(?)) ORDER BY source_id,current_key,legacy_key`, string(raw))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var current, legacy string
		if err = rows.Scan(&id, &current, &legacy); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = map[string][]string{}
		}
		out[id][current] = append(out[id][current], legacy)
	}
	return out, rows.Err()
}
