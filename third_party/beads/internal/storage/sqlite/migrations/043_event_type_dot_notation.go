package migrations

import (
	"database/sql"
	"fmt"
)

// MigrateEventTypeDotNotation updates event_type values in the events table
// from underscore format to dot-notation (e.g., "created" -> "issue.created").
// This migration is idempotent: re-running it on already-migrated data is a no-op.
func MigrateEventTypeDotNotation(db *sql.DB) error {
	renames := []struct{ old, new string }{
		{"created", "issue.created"},
		{"updated", "issue.updated"},
		{"status_changed", "issue.status_changed"},
		{"commented", "issue.commented"},
		{"closed", "issue.closed"},
		{"reopened", "issue.reopened"},
		{"dependency_added", "issue.dependency_added"},
		{"dependency_removed", "issue.dependency_removed"},
		{"label_added", "issue.label_added"},
		{"label_removed", "issue.label_removed"},
		{"compacted", "issue.compacted"},
	}

	for _, r := range renames {
		_, err := db.Exec(`UPDATE events SET event_type = ? WHERE event_type = ?`, r.new, r.old)
		if err != nil {
			return fmt.Errorf("failed to rename event_type %q to %q: %w", r.old, r.new, err)
		}
	}

	return nil
}
