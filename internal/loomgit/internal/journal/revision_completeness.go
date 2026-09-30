package journal

import (
	"database/sql"
	"strings"
)

func createRevisionCompleteness(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(change_revisions)`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid, notNull, pk int
		var name, kind string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == "incomplete" {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	_, err = db.Exec(`ALTER TABLE change_revisions ADD COLUMN incomplete INTEGER NOT NULL DEFAULT 0`)
	if err != nil && strings.Contains(err.Error(), "duplicate column name") {
		return nil
	}
	return err
}
