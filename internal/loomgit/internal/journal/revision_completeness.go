package journal

import (
	"database/sql"
	"strings"
)

func createRevisionCompleteness(db *sql.DB) error {
	columns, err := revisionColumnNames(db)
	if err != nil {
		return err
	}
	if !columns["incomplete"] {
		if err := addRevisionColumn(db, `ALTER TABLE change_revisions ADD COLUMN incomplete INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	if columns["no_changes"] {
		return nil
	}
	if err := addRevisionColumn(db, `ALTER TABLE change_revisions ADD COLUMN no_changes INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	// Older journals did not record base trees. A complete source revision with
	// no commits after its base (head == base) changed nothing; a revision with
	// commits that net to nothing stays reviewable until it is frozen again.
	_, err = db.Exec(`UPDATE change_revisions SET no_changes=1
		WHERE kind='source' AND ready=1 AND incomplete=0 AND head_sha<>'' AND head_sha=base_sha`)
	return err
}

func revisionColumnNames(db *sql.DB) (map[string]bool, error) {
	rows, err := db.Query(`PRAGMA table_info(change_revisions)`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	names := map[string]bool{}
	for rows.Next() {
		var cid, notNull, pk int
		var name, kind string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &kind, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		names[name] = true
	}
	return names, rows.Err()
}

func addRevisionColumn(db *sql.DB, statement string) error {
	_, err := db.Exec(statement)
	if err != nil && strings.Contains(err.Error(), "duplicate column name") {
		return nil
	}
	return err
}
