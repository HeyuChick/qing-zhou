package store

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// BackupTo writes a consistent snapshot of the whole database to dst.
//
// This exists because the obvious thing — copying qingzhou.db — is wrong here.
// The database runs in WAL mode, so at any moment an arbitrary amount of
// committed data lives in the -wal file and not in the main file at all. A copy
// of qingzhou.db alone is a torn, stale database; a copy of all three files
// taken while a write is in flight is worse, because it looks intact and only
// fails later. Restoring either one loses orders, traffic counters, or the
// certificate table, and the operator finds out at the moment they can least
// afford to.
//
// VACUUM INTO takes the snapshot from inside SQLite: it runs against a read
// transaction, so it sees exactly one committed point in time, includes
// everything in the WAL, and produces a single self-contained file with no
// sidecars. Readers and writers keep working while it runs.
//
// dst must not already exist — SQLite refuses to overwrite, and we surface that
// rather than deleting whatever is in the way.
func (s *Store) BackupTo(dst string) error {
	if strings.TrimSpace(dst) == "" {
		return fmt.Errorf("backup destination is empty")
	}
	abs, err := filepath.Abs(dst)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err == nil {
		return fmt.Errorf("backup destination already exists: %s", abs)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return err
	}
	// The path is a bound parameter, not string-concatenated, so a directory
	// name containing a quote cannot terminate the statement.
	if _, err := s.db.Exec(`VACUUM INTO ?`, abs); err != nil {
		// A half-written file left behind by a failed vacuum would be mistaken
		// for a usable backup.
		_ = os.Remove(abs)
		return err
	}
	return nil
}
