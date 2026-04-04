package migrations

import (
	"database/sql"
	"testing"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

// createMinimalEventsDB creates a DB with a pre-migration events table.
func createMinimalEventsDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("failed to open database: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE events (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		issue_id TEXT NOT NULL,
		event_type TEXT NOT NULL,
		actor TEXT NOT NULL,
		old_value TEXT,
		new_value TEXT,
		comment TEXT,
		created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatalf("failed to create events table: %v", err)
	}
	return db
}

func TestMigrateEventTypeDotNotation(t *testing.T) {
	db := createMinimalEventsDB(t)
	defer db.Close()

	// Insert rows with old underscore-format event_type values.
	oldTypes := []string{
		"created", "updated", "status_changed", "commented", "closed",
		"reopened", "dependency_added", "dependency_removed",
		"label_added", "label_removed", "compacted",
	}
	for _, et := range oldTypes {
		_, err := db.Exec(
			`INSERT INTO events (issue_id, event_type, actor) VALUES ('issue-1', ?, 'alice')`,
			et,
		)
		if err != nil {
			t.Fatalf("failed to insert event_type %q: %v", et, err)
		}
	}

	if err := MigrateEventTypeDotNotation(db); err != nil {
		t.Fatalf("MigrateEventTypeDotNotation failed: %v", err)
	}

	// Verify all values updated to dot-notation.
	expectedTypes := map[string]string{
		"created":            "issue.created",
		"updated":            "issue.updated",
		"status_changed":     "issue.status_changed",
		"commented":          "issue.commented",
		"closed":             "issue.closed",
		"reopened":           "issue.reopened",
		"dependency_added":   "issue.dependency_added",
		"dependency_removed": "issue.dependency_removed",
		"label_added":        "issue.label_added",
		"label_removed":      "issue.label_removed",
		"compacted":          "issue.compacted",
	}

	rows, err := db.Query(`SELECT event_type FROM events ORDER BY id`)
	if err != nil {
		t.Fatalf("failed to query events: %v", err)
	}
	defer rows.Close()

	i := 0
	for rows.Next() {
		var eventType string
		if err := rows.Scan(&eventType); err != nil {
			t.Fatalf("failed to scan row: %v", err)
		}
		oldType := oldTypes[i]
		expected := expectedTypes[oldType]
		if eventType != expected {
			t.Errorf("row %d: event_type = %q, want %q", i, eventType, expected)
		}
		i++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows iteration error: %v", err)
	}
	if i != len(oldTypes) {
		t.Errorf("got %d rows, want %d", i, len(oldTypes))
	}
}

func TestMigrateEventTypeDotNotation_Idempotent(t *testing.T) {
	db := createMinimalEventsDB(t)
	defer db.Close()

	// Insert rows with old-format values.
	for _, et := range []string{"created", "status_changed", "commented"} {
		_, err := db.Exec(
			`INSERT INTO events (issue_id, event_type, actor) VALUES ('issue-1', ?, 'bob')`,
			et,
		)
		if err != nil {
			t.Fatalf("failed to insert event_type %q: %v", et, err)
		}
	}

	// Run migration twice.
	if err := MigrateEventTypeDotNotation(db); err != nil {
		t.Fatalf("first call failed: %v", err)
	}
	if err := MigrateEventTypeDotNotation(db); err != nil {
		t.Fatalf("second (idempotent) call failed: %v", err)
	}

	// Verify values are still correct after the second run.
	expected := []string{"issue.created", "issue.status_changed", "issue.commented"}
	rows, err := db.Query(`SELECT event_type FROM events ORDER BY id`)
	if err != nil {
		t.Fatalf("failed to query events: %v", err)
	}
	defer rows.Close()

	i := 0
	for rows.Next() {
		var eventType string
		if err := rows.Scan(&eventType); err != nil {
			t.Fatalf("failed to scan row: %v", err)
		}
		if eventType != expected[i] {
			t.Errorf("row %d: event_type = %q, want %q", i, eventType, expected[i])
		}
		i++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows iteration error: %v", err)
	}
	if i != len(expected) {
		t.Errorf("got %d rows, want %d", i, len(expected))
	}
}

func TestMigrateEventTypeDotNotation_EmptyTable(t *testing.T) {
	db := createMinimalEventsDB(t)
	defer db.Close()

	// Run migration on empty events table — should be a no-op.
	if err := MigrateEventTypeDotNotation(db); err != nil {
		t.Fatalf("MigrateEventTypeDotNotation on empty table failed: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM events`).Scan(&count); err != nil {
		t.Fatalf("failed to count events: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 rows, got %d", count)
	}
}
