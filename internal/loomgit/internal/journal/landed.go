package journal

import (
	"context"
	"database/sql"
	"errors"
)

func createPullSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS landed_changes (
        workspace TEXT NOT NULL, change_id TEXT NOT NULL,
        PRIMARY KEY(workspace, change_id)
    );
    CREATE TABLE IF NOT EXISTS pull_plans (
        request_id TEXT PRIMARY KEY, workspace TEXT NOT NULL, lead TEXT NOT NULL,
        repo TEXT NOT NULL, base_sha TEXT NOT NULL, layers BLOB NOT NULL
    );`)
	return err
}

func (s *SQLite) MarkLanded(ctx context.Context, workspace, change string) error {
	if workspace == "" || change == "" {
		return errors.New("workspace and change are required")
	}
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO landed_changes(workspace, change_id) VALUES (?,?)`, workspace, change)
	return err
}

func (s *SQLite) IsLanded(ctx context.Context, workspace, change string) (bool, error) {
	var found int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM landed_changes WHERE workspace=? AND change_id=?`, workspace, change).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return found == 1, err
}
