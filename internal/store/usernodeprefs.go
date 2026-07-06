package store

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
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
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
