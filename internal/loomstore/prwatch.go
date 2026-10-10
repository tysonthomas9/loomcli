package loomstore

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// PRWatchKey names one agent's watch on one GitHub PR.
type PRWatchKey struct {
	AgentID string
	Owner   string
	Repo    string
	Number  int
}

// PRWatchCursor is what a watch last reported: the PR's head SHA, a digest
// of its check runs, and its comment cursor (OR10).
type PRWatchCursor struct {
	Head     string
	Checks   string
	Comments string
}

// PRWatch is a saved PR watch (OR10).
type PRWatch struct {
	PRWatchKey
	WorkspaceID string
	Viewer      string // the host GitHub login the watch last read as
	Cursor      PRWatchCursor
	WakeCount   int
	LastTold    string
	CreatedAt   string
	UpdatedAt   string
}

const prWatchCols = `agent_id, owner, repo, number, workspace_id, viewer, head_sha, checks_cursor, comments_cursor,
  wake_count, last_told, created_at, updated_at`

func scanPRWatch(row interface{ Scan(...any) error }) (PRWatch, error) {
	var w PRWatch
	err := row.Scan(&w.AgentID, &w.Owner, &w.Repo, &w.Number, &w.WorkspaceID, &w.Viewer, &w.Cursor.Head,
		&w.Cursor.Checks, &w.Cursor.Comments, &w.WakeCount, &w.LastTold, &w.CreatedAt, &w.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return w, ErrNotFound
	}
	return w, err
}

// RegisterPRWatch saves w unless its key is already watched; then only the
// viewer is updated and the saved watch, cursors and all, is kept. created
// reports a new row.
func (s *Store) RegisterPRWatch(ctx context.Context, w PRWatch) (saved PRWatch, created bool, err error) {
	err = s.tx(ctx, func(tx *sql.Tx) error {
		now := Stamp(time.Now())
		res, err := tx.ExecContext(ctx, `INSERT INTO pr_watches (`+prWatchCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, '', ?, ?)
ON CONFLICT (agent_id, owner, repo, number) DO NOTHING`, w.AgentID, w.Owner, w.Repo, w.Number, w.WorkspaceID, w.Viewer,
			w.Cursor.Head, w.Cursor.Checks, w.Cursor.Comments, now, now)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if created = n == 1; !created {
			if _, err := tx.ExecContext(ctx, `UPDATE pr_watches SET viewer = ?, updated_at = ?
WHERE agent_id = ? AND owner = ? AND repo = ? AND number = ? AND viewer != ?`,
				w.Viewer, now, w.AgentID, w.Owner, w.Repo, w.Number, w.Viewer); err != nil {
				return err
			}
		}
		saved, err = scanPRWatch(tx.QueryRowContext(ctx, `SELECT `+prWatchCols+` FROM pr_watches
WHERE agent_id = ? AND owner = ? AND repo = ? AND number = ?`, w.AgentID, w.Owner, w.Repo, w.Number))
		return err
	})
	return saved, created, err
}

// PRWatch returns the watch k, or ErrNotFound.
func (s *Store) PRWatch(ctx context.Context, k PRWatchKey) (PRWatch, error) {
	return scanPRWatch(s.db.QueryRowContext(ctx, `SELECT `+prWatchCols+` FROM pr_watches
WHERE agent_id = ? AND owner = ? AND repo = ? AND number = ?`, k.AgentID, k.Owner, k.Repo, k.Number))
}

// PRWatches lists workspace's PR watches in key order.
func (s *Store) PRWatches(ctx context.Context, workspace string) ([]PRWatch, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+prWatchCols+` FROM pr_watches WHERE workspace_id = ?
ORDER BY agent_id, owner, repo, number`, workspace)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []PRWatch
	for rows.Next() {
		w, err := scanPRWatch(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// UnregisterPRWatch deletes the watch k, reporting whether there was one.
func (s *Store) UnregisterPRWatch(ctx context.Context, k PRWatchKey) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM pr_watches WHERE agent_id = ? AND owner = ? AND repo = ? AND number = ?`,
		k.AgentID, k.Owner, k.Repo, k.Number)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// AdvancePRWatch saves what the watch k last reported, as viewer: its
// cursor, wake count and last-told text. ErrNotFound when k is not watched.
func (s *Store) AdvancePRWatch(ctx context.Context, k PRWatchKey, viewer string, c PRWatchCursor, wakeCount int, lastTold string) error {
	return s.tx(ctx, func(tx *sql.Tx) error { return advancePRWatch(ctx, tx, k, viewer, c, wakeCount, lastTold) })
}

// advancePRWatch is AdvancePRWatch inside tx, so a wake's Send receipt and
// its cursor can commit together.
func advancePRWatch(ctx context.Context, tx *sql.Tx, k PRWatchKey, viewer string, c PRWatchCursor, wakeCount int, lastTold string) error {
	res, err := tx.ExecContext(ctx, `UPDATE pr_watches SET viewer = ?, head_sha = ?, checks_cursor = ?, comments_cursor = ?,
  wake_count = ?, last_told = ?, updated_at = ? WHERE agent_id = ? AND owner = ? AND repo = ? AND number = ?`,
		viewer, c.Head, c.Checks, c.Comments, wakeCount, lastTold, Stamp(time.Now()), k.AgentID, k.Owner, k.Repo, k.Number)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil || n == 0 {
		if err == nil {
			err = ErrNotFound
		}
		return err
	}
	return nil
}
