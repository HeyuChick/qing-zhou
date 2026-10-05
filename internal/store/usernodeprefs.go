package store

import "fmt"

// Per-user node blocklist. A node_key (subconv.NodeKey of a share link) present
// for a user is hidden from that user's subscription output. Only affects the
// owning user; other users and the node's existence are unchanged.

// DisabledNodeKeys returns the set of node keys the user has disabled.
func (s *Store) DisabledNodeKeys(userID int64) (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT node_key FROM user_disabled_nodes WHERE user_id=?`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out[k] = true
	}
	return out, rows.Err()
}

// SetNodeDisabled disables (insert) or enables (delete) one node for a user.
func (s *Store) SetNodeDisabled(userID int64, key string, disabled bool) error {
	if disabled {
		_, err := s.db.Exec(`INSERT OR IGNORE INTO user_disabled_nodes(user_id, node_key) VALUES(?,?)`, userID, key)
		return err
	}
	_, err := s.db.Exec(`DELETE FROM user_disabled_nodes WHERE user_id=? AND node_key=?`, userID, key)
	return err
}

// DisableNodeKeys disables many nodes for a user in one transaction.
func (s *Store) DisableNodeKeys(userID int64, keys []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.Prepare(`INSERT OR IGNORE INTO user_disabled_nodes(user_id, node_key) VALUES(?,?)`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, k := range keys {
		if _, err := st.Exec(userID, k); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// EnableAllNodes clears the user's entire blocklist.
func (s *Store) EnableAllNodes(userID int64) error {
	_, err := s.db.Exec(`DELETE FROM user_disabled_nodes WHERE user_id=?`, userID)
	return err
}

// ApplyNodePrefs disables (insert) the given keys and enables (delete) the
// others in a single transaction. Keys in neither list are left untouched.
func (s *Store) ApplyNodePrefs(userID int64, disable, enable []string) error {
	return s.ApplyNodePrefsWithAliases(userID, disable, enable, nil)
}

// ApplyNodePrefsWithAliases preserves siblings covered by a shared historical
// key. Compatibility is read-only until the user explicitly enables a node;
// sibling protection and removal of its old aliases commit in one transaction.
// aliases must be rebuilt from trusted current node metadata by the caller.
func (s *Store) ApplyNodePrefsWithAliases(userID int64, disable, enable []string, aliases map[string][]string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	requested := map[string]bool{}
	remove := map[string]bool{}
	for _, key := range enable {
		requested[key] = true
		remove[key] = true
		for _, old := range aliases[key] {
			remove[old] = true
		}
	}
	if len(aliases) > 0 && len(enable) > 0 {
		rows, err := tx.Query(`SELECT node_key FROM user_disabled_nodes WHERE user_id=?`, userID)
		if err != nil {
			return err
		}
		disabled := map[string]bool{}
		for rows.Next() {
			var key string
			if err = rows.Scan(&key); err != nil {
				rows.Close()
				return err
			}
			disabled[key] = true
		}
		if err = rows.Close(); err != nil {
			return err
		}
		if err = rows.Err(); err != nil {
			return err
		}
		for current, keys := range aliases {
			if requested[current] {
				continue
			}
			for _, old := range keys {
				if disabled[old] && remove[old] {
					if remove[current] {
						return fmt.Errorf("旧节点标识存在无法分离的共享冲突，请同时选择相关节点")
					}
					disable = append(disable, current)
					break
				}
			}
		}
	}
	enable = make([]string, 0, len(remove))
	for key := range remove {
		enable = append(enable, key)
	}
	ins, err := tx.Prepare(`INSERT OR IGNORE INTO user_disabled_nodes(user_id, node_key) VALUES(?,?)`)
	if err != nil {
		return err
	}
	defer ins.Close()
	for _, k := range disable {
		if _, err := ins.Exec(userID, k); err != nil {
			return err
		}
	}
	del, err := tx.Prepare(`DELETE FROM user_disabled_nodes WHERE user_id=? AND node_key=?`)
	if err != nil {
		return err
	}
	defer del.Close()
	for _, k := range enable {
		if _, err := del.Exec(userID, k); err != nil {
			return err
		}
	}
	return tx.Commit()
}
