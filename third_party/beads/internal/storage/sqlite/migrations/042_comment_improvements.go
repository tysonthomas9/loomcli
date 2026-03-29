package migrations

import (
	"database/sql"
	"fmt"
)

// MigrateCommentImprovements adds comment threading (parent_id), soft-delete
// (deleted_at), edit tracking (edited_at), and creates comment_reactions +
// comment_edits tables for the Comment Improvements epic.
func MigrateCommentImprovements(db *sql.DB) error {
	// 1. ALTER TABLE comments — add parent_id, edited_at, deleted_at
	columns := []struct {
		name       string
		definition string
	}{
		{"parent_id", "INTEGER REFERENCES comments(id) ON DELETE SET NULL"},
		{"edited_at", "DATETIME"},
		{"deleted_at", "DATETIME"},
	}

	for _, col := range columns {
		var columnExists bool
		err := db.QueryRow(`
			SELECT COUNT(*) > 0
			FROM pragma_table_info('comments')
			WHERE name = ?
		`, col.name).Scan(&columnExists)
		if err != nil {
			return fmt.Errorf("failed to check %s column: %w", col.name, err)
		}

		if columnExists {
			continue
		}

		_, err = db.Exec(fmt.Sprintf(`ALTER TABLE comments ADD COLUMN %s %s`, col.name, col.definition))
		if err != nil {
			return fmt.Errorf("failed to add %s column: %w", col.name, err)
		}
	}

	// 2. Partial index on parent_id for efficient threaded-comment queries
	_, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_comments_parent ON comments(parent_id) WHERE parent_id IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("failed to create idx_comments_parent: %w", err)
	}

	// 3. comment_reactions table
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS comment_reactions (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		comment_id INTEGER NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
		author TEXT NOT NULL,
		emoji TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(comment_id, author, emoji)
	)`)
	if err != nil {
		return fmt.Errorf("failed to create comment_reactions table: %w", err)
	}

	// 4. Index on comment_reactions(comment_id) for joins
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_comment_reactions_comment ON comment_reactions(comment_id)`)
	if err != nil {
		return fmt.Errorf("failed to create idx_comment_reactions_comment: %w", err)
	}

	// 5. comment_edits table
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS comment_edits (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		comment_id INTEGER NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
		old_text TEXT NOT NULL,
		new_text TEXT NOT NULL,
		edited_by TEXT NOT NULL,
		edited_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		return fmt.Errorf("failed to create comment_edits table: %w", err)
	}

	// 6. Index on comment_edits(comment_id) for joins
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_comment_edits_comment ON comment_edits(comment_id)`)
	if err != nil {
		return fmt.Errorf("failed to create idx_comment_edits_comment: %w", err)
	}

	return nil
}
