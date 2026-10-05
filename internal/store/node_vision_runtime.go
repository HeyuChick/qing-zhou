package store

import (
	"database/sql"
	"errors"
	"time"

	"qingzhou/internal/intervalcfg"
	"qingzhou/internal/sbver"
)

// The historical Vision table also stores transport-capability evidence; both
// capabilities are derived independently from the actual running version marker.
// Runtime evidence is distinct from an installed-binary/UI refresh. Otherwise
// a disk-only positive probe could erase a known old running core and falsely
// restore P1 attribution readiness. This table has no credential/user data.
const visionRuntimeCapabilitySchema = `
CREATE TABLE node_vision_runtime (
 server_id INTEGER PRIMARY KEY,
 version TEXT NOT NULL DEFAULT '',
 v2ray_api INTEGER NOT NULL DEFAULT 0,
 checked_at INTEGER NOT NULL DEFAULT 0,
 error TEXT NOT NULL DEFAULT ''
);`

// SetNodeVisionRuntime records a fresh verified running-core version, only
// after the controller has checked both the installed and actual process core.
func (s *Store) SetNodeVisionRuntime(serverID int64, info sbver.Info) error {
	_, err := s.db.Exec(`INSERT INTO node_vision_runtime(server_id,version,v2ray_api,checked_at,error) VALUES(?,?,?,?,'')
 ON CONFLICT(server_id) DO UPDATE SET version=excluded.version,v2ray_api=excluded.v2ray_api,checked_at=excluded.checked_at,error=''`, serverID, info.Version, b2i(info.HasV2RayAPI), time.Now().Unix())
	return err
}

func (s *Store) SetNodeVisionRuntimeError(serverID int64, msg string) error {
	if len(msg) > maxRawLen {
		msg = msg[:maxRawLen]
	}
	_, err := s.db.Exec(`INSERT INTO node_vision_runtime(server_id,checked_at,error) VALUES(?,?,?)
 ON CONFLICT(server_id) DO UPDATE SET checked_at=excluded.checked_at,error=excluded.error`, serverID, time.Now().Unix(), msg)
	return err
}

type visionSettingReader struct{ db txLike }

func (r visionSettingReader) GetSetting(key string) (string, error) {
	var value string
	err := r.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&value)
	return value, err
}

func nodeVisionRuntimeReadyWith(db txLike, serverID, now int64) (bool, error) {
	return nodeRelayCoreRuntimeReadyWith(db, serverID, now, sbver.HasVisionFramingFix)
}

func nodeRelayCoreRuntimeReadyWith(db txLike, serverID, now int64, accepts func(string) bool) (bool, error) {
	var version, probeErr string
	var checkedAt int64
	var stats bool
	err := db.QueryRow(`SELECT version,v2ray_api,checked_at,error FROM node_vision_runtime WHERE server_id=?`, serverID).Scan(&version, &stats, &checkedAt, &probeErr)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// Match the configured maintenance cadence, rather than turning a healthy
	// slow-reconcile installation stale after an unrelated fixed 15 minutes.
	_, reconcile := intervalcfg.Controller(visionSettingReader{db})
	maxAge := 2 * reconcile
	if maxAge < relayVisionProbeMaxAge {
		maxAge = relayVisionProbeMaxAge
	}
	return accepts(version) && stats && probeErr == "" && checkedAt > 0 && checkedAt >= now-int64(maxAge/time.Second) && checkedAt <= now+60, nil
}
