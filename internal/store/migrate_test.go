package store

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// openMigrated returns a Store on a throwaway file, migrated once.
func openMigrated(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return st
}

// The v0.2.48 outage, as a test. That release added users.proxy_username via
// ALTER *and* put a unique index on it in the base schema, which runs first: on
// an existing DB the CREATE TABLE is a no-op, the index named a column that was
// not there yet, the whole schema Exec failed, and main log.Fatal'd. systemd
// restarted it into the same wall ~650 times. Every test passed the whole time,
// because a test DB is created by CREATE TABLE and therefore never takes the
// upgrade path.
//
// Rewinding a migrated DB is what makes that path reachable here: the assertion
// is not about these three columns, it is that a column added by ALTER is usable
// by the time the backfills run.
func TestMigrate_UpgradesDBMissingAlterAddedColumns(t *testing.T) {
	st := openMigrated(t)
	for _, stmt := range []string{
		`DROP INDEX IF EXISTS idx_users_proxy_username`,
		`ALTER TABLE users DROP COLUMN proxy_username`,
		`ALTER TABLE users DROP COLUMN proxy_password`,
		`ALTER TABLE users DROP COLUMN proxy_expires_at`,
	} {
		if _, err := st.db.Exec(stmt); err != nil {
			t.Fatalf("rewinding the schema with %q: %v", stmt, err)
		}
	}

	if err := st.Migrate(); err != nil {
		t.Fatalf("upgrading a DB that predates the column failed: %v", err)
	}

	// Re-added, not merely tolerated: the backfills and every user query read it.
	var n int
	if err := st.db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('users') WHERE name IN
		 ('proxy_username','proxy_password','proxy_expires_at')`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("users has %d of the 3 proxy columns after the upgrade", n)
	}
	if _, err := st.db.Exec(`SELECT proxy_username FROM users LIMIT 1`); err != nil {
		t.Errorf("column present but unusable: %v", err)
	}
}

// Migrating twice must be a no-op, which is what every restart after a normal
// upgrade actually does.
func TestMigrate_Idempotent(t *testing.T) {
	st := openMigrated(t)
	if err := st.Migrate(); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}

var schemaIndexStmt = regexp.MustCompile(`(?mi)^\s*CREATE\s+(UNIQUE\s+)?INDEX`)

// The invariant behind the fix, checked on the source rather than on behavior:
// index DDL lives in `indexes` (applied after the ALTER phase), never in
// `schema` (applied before it). Adding one to schema next to a brand-new column
// looks harmless — CREATE TABLE right above it declares the column — and is
// invisible until someone upgrades a real DB, which is exactly too late.
func TestMigrate_SchemaDeclaresNoIndexes(t *testing.T) {
	if loc := schemaIndexStmt.FindStringIndex(schema); loc != nil {
		line := strings.SplitN(strings.TrimSpace(schema[loc[0]:]), "\n", 2)[0]
		t.Errorf("schema declares an index: %q\nMove it to the `indexes` const —"+
			" schema runs before the ALTER TABLE phase, so an index there cannot"+
			" name a column that phase adds, and the failure is a boot loop.", line)
	}
	if !schemaIndexStmt.MatchString(indexes) {
		t.Error("`indexes` holds no index DDL — did it get emptied?")
	}
}
