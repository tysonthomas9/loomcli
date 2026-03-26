package migrations

import (
	"database/sql"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

// createMinimalCommentsDB creates a DB with a pre-migration comments table.
func createMinimalCommentsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE comments (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		issue_id TEXT NOT NULL,
		author TEXT NOT NULL,
		text TEXT NOT NULL,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatalf("failed to create comments table: %v", err)
	}
	return db
}

func TestMigrateCommentImprovements(t *testing.T) {
	db := createMinimalCommentsDB(t)
	defer db.Close()

	if err := MigrateCommentImprovements(db); err != nil {
		t.Fatalf("MigrateCommentImprovements failed: %v", err)
	}

	// Verify new columns on comments table
	for _, col := range []string{"parent_id", "edited_at", "deleted_at"} {
		var exists bool
		err := db.QueryRow(`SELECT COUNT(*) > 0 FROM pragma_table_info('comments') WHERE name = ?`, col).Scan(&exists)
		if err != nil {
			t.Fatalf("failed to check column %s: %v", col, err)
		}
		if !exists {
			t.Errorf("expected column %s on comments table", col)
		}
	}

	// Verify comment_reactions table columns
	for _, col := range []string{"id", "comment_id", "author", "emoji", "created_at"} {
		var exists bool
		err := db.QueryRow(`SELECT COUNT(*) > 0 FROM pragma_table_info('comment_reactions') WHERE name = ?`, col).Scan(&exists)
		if err != nil {
			t.Fatalf("failed to check comment_reactions.%s: %v", col, err)
		}
		if !exists {
			t.Errorf("expected column %s on comment_reactions table", col)
		}
	}

	// Verify comment_edits table columns
	for _, col := range []string{"id", "comment_id", "old_text", "new_text", "edited_by", "edited_at"} {
		var exists bool
		err := db.QueryRow(`SELECT COUNT(*) > 0 FROM pragma_table_info('comment_edits') WHERE name = ?`, col).Scan(&exists)
		if err != nil {
			t.Fatalf("failed to check comment_edits.%s: %v", col, err)
		}
		if !exists {
			t.Errorf("expected column %s on comment_edits table", col)
		}
	}

	// Verify indexes exist
	for _, idx := range []struct{ name, table string }{
		{"idx_comments_parent", "comments"},
		{"idx_comment_reactions_comment", "comment_reactions"},
		{"idx_comment_edits_comment", "comment_edits"},
	} {
		var name string
		err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = ? AND name = ?`, idx.table, idx.name).Scan(&name)
		if err != nil {
			t.Errorf("index %s not found on %s: %v", idx.name, idx.table, err)
		}
	}
}

func TestMigrateCommentImprovements_Idempotent(t *testing.T) {
	db := createMinimalCommentsDB(t)
	defer db.Close()

	if err := MigrateCommentImprovements(db); err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if err := MigrateCommentImprovements(db); err != nil {
		t.Fatalf("second (idempotent) call failed: %v", err)
	}
}

func TestMigrateCommentImprovements_DataPreservation(t *testing.T) {
	db := createMinimalCommentsDB(t)
	defer db.Close()

	// Insert a row before migration
	_, err := db.Exec(`INSERT INTO comments (issue_id, author, text) VALUES ('issue-1', 'alice', 'hello world')`)
	if err != nil {
		t.Fatalf("failed to insert pre-migration comment: %v", err)
	}

	if err := MigrateCommentImprovements(db); err != nil {
		t.Fatalf("MigrateCommentImprovements failed: %v", err)
	}

	// Verify the row is intact with NULL for new columns
	var issueID, author, text string
	var parentID, editedAt, deletedAt sql.NullString
	err = db.QueryRow(`SELECT issue_id, author, text, parent_id, edited_at, deleted_at FROM comments WHERE id = 1`).
		Scan(&issueID, &author, &text, &parentID, &editedAt, &deletedAt)
	if err != nil {
		t.Fatalf("failed to query comment: %v", err)
	}
	if issueID != "issue-1" || author != "alice" || text != "hello world" {
		t.Errorf("original data changed: got (%s, %s, %s)", issueID, author, text)
	}
	if parentID.Valid || editedAt.Valid || deletedAt.Valid {
		t.Errorf("new columns should be NULL: parent_id=%v edited_at=%v deleted_at=%v", parentID, editedAt, deletedAt)
	}
}

func TestMigrateCommentImprovements_UniqueConstraint(t *testing.T) {
	db := createMinimalCommentsDB(t)
	defer db.Close()

	if err := MigrateCommentImprovements(db); err != nil {
		t.Fatalf("MigrateCommentImprovements failed: %v", err)
	}

	// Insert a comment to reference
	_, err := db.Exec(`INSERT INTO comments (issue_id, author, text) VALUES ('issue-1', 'alice', 'hello')`)
	if err != nil {
		t.Fatalf("failed to insert comment: %v", err)
	}

	// Insert a reaction
	_, err = db.Exec(`INSERT INTO comment_reactions (comment_id, author, emoji) VALUES (1, 'bob', '👍')`)
	if err != nil {
		t.Fatalf("failed to insert reaction: %v", err)
	}

	// Duplicate reaction should fail
	_, err = db.Exec(`INSERT INTO comment_reactions (comment_id, author, emoji) VALUES (1, 'bob', '👍')`)
	if err == nil {
		t.Error("expected UNIQUE constraint violation on duplicate reaction")
	}

	// Same user, different emoji should succeed
	_, err = db.Exec(`INSERT INTO comment_reactions (comment_id, author, emoji) VALUES (1, 'bob', '❤️')`)
	if err != nil {
		t.Errorf("different emoji should be allowed: %v", err)
	}
}

func TestMigrateCommentImprovements_CascadeDelete(t *testing.T) {
	db := createMinimalCommentsDB(t)
	defer db.Close()

	if err := MigrateCommentImprovements(db); err != nil {
		t.Fatalf("MigrateCommentImprovements failed: %v", err)
	}

	// Enable FK enforcement for cascade test
	_, err := db.Exec(`PRAGMA foreign_keys = ON`)
	if err != nil {
		t.Fatalf("failed to enable foreign keys: %v", err)
	}

	// Insert a comment
	_, err = db.Exec(`INSERT INTO comments (issue_id, author, text) VALUES ('issue-1', 'alice', 'hello')`)
	if err != nil {
		t.Fatalf("failed to insert comment: %v", err)
	}

	// Add a reaction and an edit
	_, err = db.Exec(`INSERT INTO comment_reactions (comment_id, author, emoji) VALUES (1, 'bob', '👍')`)
	if err != nil {
		t.Fatalf("failed to insert reaction: %v", err)
	}
	_, err = db.Exec(`INSERT INTO comment_edits (comment_id, old_text, new_text, edited_by) VALUES (1, 'hello', 'hello world', 'alice')`)
	if err != nil {
		t.Fatalf("failed to insert edit: %v", err)
	}

	// Delete the comment — cascade should remove reaction and edit
	_, err = db.Exec(`DELETE FROM comments WHERE id = 1`)
	if err != nil {
		t.Fatalf("failed to delete comment: %v", err)
	}

	var reactionCount, editCount int
	db.QueryRow(`SELECT COUNT(*) FROM comment_reactions WHERE comment_id = 1`).Scan(&reactionCount)
	db.QueryRow(`SELECT COUNT(*) FROM comment_edits WHERE comment_id = 1`).Scan(&editCount)

	if reactionCount != 0 {
		t.Errorf("expected 0 reactions after cascade delete, got %d", reactionCount)
	}
	if editCount != 0 {
		t.Errorf("expected 0 edits after cascade delete, got %d", editCount)
	}
}
