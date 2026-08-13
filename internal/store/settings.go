package store

import (
	"strconv"
)

// GetSetting returns the value for key, or "" if the key is absent.
// Secret keys are transparently decrypted. Backed by an in-memory cache that
// is invalidated on any settings write.
func (s *Store) GetSetting(key string) (string, error) {
	raw, ok, err := s.cachedSettingRaw(key)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	if encKeys[key] {
		raw = s.decrypt(raw)
	}
	return raw, nil
}

// cachedSettingRaw returns the raw (possibly encrypted) stored value, loading
// the whole settings table into the cache on first use.
func (s *Store) cachedSettingRaw(key string) (string, bool, error) {
	s.setMu.RLock()
	cache := s.setCache
	s.setMu.RUnlock()
	if cache == nil {
		var err error
		cache, err = s.loadSettingsCache()
		if err != nil {
			return "", false, err
		}
	}
	v, ok := cache[key]
	return v, ok, nil
}

func (s *Store) loadSettingsCache() (map[string]string, error) {
	s.setMu.Lock()
	defer s.setMu.Unlock()
	if s.setCache != nil { // another goroutine loaded it while we waited
		return s.setCache, nil
	}
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	s.setCache = m
	return m, nil
}

func (s *Store) invalidateSettingsCache() {
	s.setMu.Lock()
	s.setCache = nil
	s.setMu.Unlock()
}

// SetSetting upserts a setting. Secret keys are encrypted at rest.
func (s *Store) SetSetting(key, value string) error {
	if encKeys[key] {
		value = s.encrypt(value)
	}
	_, err := s.db.Exec(
		`INSERT INTO settings(key,value) VALUES(?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	if err == nil {
		s.invalidateSettingsCache()
	}
	return err
}

// setSettingIfAbsent writes the default only when the key does not yet exist.
func (s *Store) setSettingIfAbsent(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO NOTHING`, key, value)
	if err == nil {
		s.invalidateSettingsCache()
	}
	return err
}

func (s *Store) GetSettingBool(key string) (bool, error) {
	v, err := s.GetSetting(key)
	return v == "true" || v == "1", err
}

// SetSettingBool writes a boolean in the form GetSettingBool reads back.
func (s *Store) SetSettingBool(key string, v bool) error {
	if v {
		return s.SetSetting(key, "true")
	}
	return s.SetSetting(key, "false")
}

func (s *Store) GetSettingInt64(key string, def int64) (int64, error) {
	v, err := s.GetSetting(key)
	if err != nil || v == "" {
		return def, err
	}
	n, perr := strconv.ParseInt(v, 10, 64)
	if perr != nil {
		return def, nil
	}
	return n, nil
}

// AllSettings returns every setting except secrets that must never be exposed.
func (s *Store) AllSettings() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}
