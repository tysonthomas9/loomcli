package journal

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

type LocalLineage struct {
	Workspace, Task, Repo, PredecessorChange string
	PredecessorRevision                      int
	BaseSHA                                  string
}

func createLocalLineageSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS local_lineage (
		workspace TEXT NOT NULL, task_id TEXT NOT NULL, repo TEXT NOT NULL,
		predecessor_change TEXT NOT NULL, predecessor_revision INTEGER NOT NULL,
		base_sha TEXT NOT NULL, PRIMARY KEY(workspace, task_id, repo)
	);
	CREATE TABLE IF NOT EXISTS abandoned_changes (
		workspace TEXT NOT NULL, change_id TEXT NOT NULL,
		PRIMARY KEY(workspace, change_id)
	)`)
	return err
}

// LatestTaskRevision reads the newest completed local revision for one task.
func (s *SQLite) LatestTaskRevision(ctx context.Context, workspace, task, repo string) (string, int, string, error) {
	var change, head string
	var number int
	err := s.db.QueryRowContext(ctx, `SELECT d.change_id, r.number, r.head_sha
		FROM driver_changes d JOIN change_revisions r
		ON r.workspace = d.workspace AND r.change_id = d.change_id
		WHERE d.workspace = ? AND d.task_id = ? AND d.repo = ? AND r.ready = 1
		ORDER BY r.number DESC LIMIT 1`, workspace, task, repo).Scan(&change, &number, &head)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, "", ErrNotFound
	}
	return change, number, head, err
}

func (s *SQLite) LatestReadyRevision(ctx context.Context, workspace, change string) (int, string, error) {
	var number int
	var head string
	err := s.db.QueryRowContext(ctx, `SELECT number, head_sha FROM change_revisions
		WHERE workspace = ? AND change_id = ? AND ready = 1
		ORDER BY number DESC LIMIT 1`, workspace, change).Scan(&number, &head)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", ErrNotFound
	}
	return number, head, err
}

// RecordLocalLineage pins a dependent to the predecessor revision used at delegation.
func (s *SQLite) RecordLocalLineage(ctx context.Context, l LocalLineage) error {
	if l.Workspace == "" || l.Task == "" || l.Repo == "" || l.PredecessorChange == "" ||
		l.PredecessorRevision < 1 || l.BaseSHA == "" {
		return errors.New("incomplete local lineage")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO local_lineage
		(workspace, task_id, repo, predecessor_change, predecessor_revision, base_sha)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(workspace, task_id, repo) DO NOTHING`, l.Workspace, l.Task, l.Repo,
		l.PredecessorChange, l.PredecessorRevision, l.BaseSHA)
	if err != nil {
		return err
	}
	stored, err := s.LocalLineage(ctx, l.Workspace, l.Task, l.Repo)
	if err != nil {
		return err
	}
	if stored.PredecessorChange != l.PredecessorChange ||
		stored.PredecessorRevision != l.PredecessorRevision || stored.BaseSHA != l.BaseSHA {
		return ErrStale
	}
	return nil
}

func (s *SQLite) LocalLineage(ctx context.Context, workspace, task, repo string) (LocalLineage, error) {
	l := LocalLineage{Workspace: workspace, Task: task, Repo: repo}
	err := s.db.QueryRowContext(ctx, `SELECT predecessor_change, predecessor_revision, base_sha
		FROM local_lineage WHERE workspace = ? AND task_id = ? AND repo = ?`,
		workspace, task, repo).Scan(&l.PredecessorChange, &l.PredecessorRevision, &l.BaseSHA)
	if errors.Is(err, sql.ErrNoRows) {
		return LocalLineage{}, ErrNotFound
	}
	return l, err
}

// AbandonChange records a deliberate abandonment without deleting revisions.
func (s *SQLite) AbandonChange(ctx context.Context, workspace, change string) error {
	if workspace == "" || change == "" {
		return fmt.Errorf("workspace and change are required")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO abandoned_changes(workspace, change_id)
		VALUES (?, ?) ON CONFLICT DO NOTHING`, workspace, change)
	return err
}

func (s *SQLite) ChangeAbandoned(ctx context.Context, workspace, change string) (bool, error) {
	var exists int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM abandoned_changes
		WHERE workspace = ? AND change_id = ?`, workspace, change).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return exists == 1, err
}
