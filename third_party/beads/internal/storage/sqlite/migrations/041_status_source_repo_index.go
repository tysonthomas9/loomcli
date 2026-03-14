package migrations

import (
	"database/sql"
	"fmt"
)

// MigrateStatusSourceRepoIndex adds a composite partial index on (status, source_repo)
// for efficient multi-repo filtering queries like WHERE status='open' AND source_repo IN (...).
// The existing single-column idx_issues_source_repo is kept for tombstone queries.
func MigrateStatusSourceRepoIndex(db *sql.DB) error {
	_, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_issues_status_source_repo ON issues(status, source_repo) WHERE deleted_at IS NULL`)
	if err != nil {
		return fmt.Errorf("failed to create idx_issues_status_source_repo: %w", err)
	}
	return nil
}
