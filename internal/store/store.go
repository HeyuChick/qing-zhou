package store

import (
	"database/sql"
	"sync"

	_ "modernc.org/sqlite"
)

// Store is the SQLite-backed data layer.
type Store struct {
	db        *sql.DB
	secretKey []byte // for encrypting secret settings (see crypto.go)

	// settings cache: the settings table is tiny and read on every
	// subscription request but written rarely. Cache the whole table in
	// memory (raw stored values) and invalidate on any write.
	setMu    sync.RWMutex
	setCache map[string]string // raw values, nil until first load
}

// Open opens (creating if needed) the SQLite database at path and applies the
// pragmas we want for a small single-process deployment.
//
// Pragmas go in the DSN (not a one-off Exec) so EVERY pooled connection gets
// them — busy_timeout/foreign_keys are per-connection, not DB-wide.
func Open(path string) (*Store, error) {
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)" +
		"&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// WAL allows one writer plus many concurrent readers. We use a small pool
	// (not a single connection) because some writes call out to sing-box *inside* a
	// transaction and that callback reads the DB again (entitlement check); with
	// only one connection that nested read deadlocks waiting for the connection
	// the open transaction is holding. Extra connections let the nested read
	// proceed against the WAL snapshot. Writes still serialize via busy_timeout.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// DB exposes the underlying handle for packages that need raw access.
func (s *Store) DB() *sql.DB { return s.db }
