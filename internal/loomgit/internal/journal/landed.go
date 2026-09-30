package journal

import (
	"context"
	"database/sql"
	"errors"
)

func createPullSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS landed_changes (
        workspace TEXT NOT NULL, change_id TEXT NOT NULL, rule TEXT NOT NULL DEFAULT '',
        PRIMARY KEY(workspace, change_id)
    );
    CREATE TABLE IF NOT EXISTS pull_plans (
        request_id TEXT PRIMARY KEY, workspace TEXT NOT NULL, lead TEXT NOT NULL,
        repo TEXT NOT NULL, base_sha TEXT NOT NULL, layers BLOB NOT NULL,
        remove_change TEXT NOT NULL DEFAULT ''
    );`)
	if err != nil {
		return err
	}
	if err := ensureLandedRule(db); err != nil {
		return err
	}
	return ensurePullRemoveChange(db)
}

func ensurePullRemoveChange(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(pull_plans)`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			return err
		}
		if name == "remove_change" {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.Exec(`ALTER TABLE pull_plans ADD COLUMN remove_change TEXT NOT NULL DEFAULT ''`)
	return err
}

func (s *SQLite) MarkLanded(ctx context.Context, workspace, change string, rule ...string) error {
	if workspace == "" || change == "" {
		return errors.New("workspace and change are required")
	}
	selected := ""
	if len(rule) > 0 {
		selected = rule[0]
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO landed_changes(workspace, change_id, rule) VALUES (?,?,?)
		ON CONFLICT(workspace, change_id) DO UPDATE SET rule=CASE
		WHEN landed_changes.rule='' THEN excluded.rule ELSE landed_changes.rule END`, workspace, change, selected); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM merged_changes WHERE workspace=? AND change_id=?`, workspace, change); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLite) IsLanded(ctx context.Context, workspace, change string) (bool, error) {
	var found int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM landed_changes WHERE workspace=? AND change_id=?`, workspace, change).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return found == 1, err
}
