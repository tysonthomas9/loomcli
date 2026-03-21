package migrations

import (
	"database/sql"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

// TestMigrateStatusSourceRepoIndex verifies that migration 041 creates the
// composite partial index idx_issues_status_source_repo on (status, source_repo).
func TestMigrateStatusSourceRepoIndex(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	// Create a minimal issues table with the columns referenced by the index.
	_, err = db.Exec(`
		CREATE TABLE issues (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL DEFAULT 'open',
			source_repo TEXT DEFAULT '',
			deleted_at DATETIME
		)
	`)
	if err != nil {
		t.Fatalf("failed to create issues table: %v", err)
	}

	// Run the migration.
	if err := MigrateStatusSourceRepoIndex(db); err != nil {
		t.Fatalf("MigrateStatusSourceRepoIndex failed: %v", err)
	}

	// Verify the index exists with the expected name.
	var indexName string
	err = db.QueryRow(`
		SELECT name FROM sqlite_master
		WHERE type = 'index' AND tbl_name = 'issues' AND name = 'idx_issues_status_source_repo'
	`).Scan(&indexName)
	if err != nil {
		t.Fatalf("index not found: %v", err)
	}
	if indexName != "idx_issues_status_source_repo" {
		t.Errorf("unexpected index name: got %q, want %q", indexName, "idx_issues_status_source_repo")
	}
}

// TestMigrateStatusSourceRepoIndex_Idempotent verifies that running the
// migration twice does not produce an error (CREATE INDEX IF NOT EXISTS).
func TestMigrateStatusSourceRepoIndex_Idempotent(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE issues (
			id TEXT PRIMARY KEY,
			status TEXT NOT NULL DEFAULT 'open',
			source_repo TEXT DEFAULT '',
			deleted_at DATETIME
		)
	`)
	if err != nil {
		t.Fatalf("failed to create issues table: %v", err)
	}

	// Run the migration twice — the second call must also succeed.
	if err := MigrateStatusSourceRepoIndex(db); err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if err := MigrateStatusSourceRepoIndex(db); err != nil {
		t.Fatalf("second (idempotent) call failed: %v", err)
	}
}
