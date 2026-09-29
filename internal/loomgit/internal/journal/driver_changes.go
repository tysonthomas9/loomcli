package journal

import (
	"context"
	"database/sql"
	"errors"
)

func createDriverChanges(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS driver_changes (
		workspace TEXT NOT NULL, task_id TEXT NOT NULL, repo TEXT NOT NULL,
		change_id TEXT NOT NULL UNIQUE, PRIMARY KEY(workspace, task_id, repo)
	)`)
	return err
}

// DriverChange returns the durable Change for one task layer, creating it once.
func (s *SQLite) DriverChange(ctx context.Context, workspace, task, repo, id string) (string, error) {
	if workspace == "" || task == "" || repo == "" || id == "" {
		return "", errors.New("workspace, task, repo and change ID are required")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO driver_changes(workspace, task_id, repo, change_id)
		VALUES (?, ?, ?, ?) ON CONFLICT(workspace, task_id, repo) DO NOTHING`, workspace, task, repo, id)
	if err != nil {
		return "", err
	}
	var change string
	err = s.db.QueryRowContext(ctx, `SELECT change_id FROM driver_changes
		WHERE workspace = ? AND task_id = ? AND repo = ?`, workspace, task, repo).Scan(&change)
	return change, err
}
