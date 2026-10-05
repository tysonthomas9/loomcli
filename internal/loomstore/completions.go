package loomstore

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CompletionMarker is a child attempt's task_completed owed to its parent
// (OR3c). The transaction that ends the attempt saves it, with what the
// child's row and history hold then; the one that appends the record on
// the parent deletes it.
type CompletionMarker struct {
	Parent, Child   string
	Attempt         int64
	Outcome, Branch string
	Summary, Result string
	Backfilled      bool // saved by the upgrade: Summary and Result were not read; read them from the child
}

// insertMarker saves m in tx; a repeat of the same attempt changes nothing.
func insertMarker(ctx context.Context, tx *sql.Tx, m CompletionMarker) error {
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO agent_completion_markers
		(child_agent_id, attempt, parent_agent_id, outcome, branch, summary, result, created_at) VALUES (?,?,?,?,?,?,?,?)`,
		m.Child, m.Attempt, m.Parent, m.Outcome, m.Branch, m.Summary, m.Result, Stamp(time.Now()))
	return err
}

// CompletionMarkers returns the markers owed to parents in workspaceID,
// oldest first.
func (s *Store) CompletionMarkers(ctx context.Context, workspaceID string) ([]CompletionMarker, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT m.parent_agent_id, m.child_agent_id, m.attempt, m.outcome, m.branch,
		m.summary, m.result FROM agent_completion_markers m JOIN agents p ON p.agent_id = m.parent_agent_id
		WHERE p.workspace_id = ? ORDER BY m.created_at, m.child_agent_id, m.attempt`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []CompletionMarker
	for rows.Next() {
		var m CompletionMarker
		var summary, result sql.NullString
		if err := rows.Scan(&m.Parent, &m.Child, &m.Attempt, &m.Outcome, &m.Branch, &summary, &result); err != nil {
			return nil, err
		}
		m.Summary, m.Result, m.Backfilled = summary.String, result.String, !summary.Valid
		out = append(out, m)
	}
	return out, rows.Err()
}

// DeliverCompletion appends e, m's record on m.Parent, and deletes m, in one
// transaction. When m is no longer owed (already delivered, or dropped with
// its parent's Delete or history purge), or m.Parent is deleted or its
// history purged, it deletes m and appends nothing. It returns the saved
// event, if any.
func (s *Store) DeliverCompletion(ctx context.Context, m CompletionMarker, e Event) (saved []Event, err error) {
	err = s.tx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM agent_completion_markers WHERE child_agent_id = ? AND attempt = ?`,
			m.Child, m.Attempt)
		if err != nil {
			return err
		}
		var gone bool
		err = tx.QueryRowContext(ctx, `SELECT deleted_at IS NOT NULL OR history_purged_at IS NOT NULL FROM agents
			WHERE agent_id = ?`, m.Parent).Scan(&gone)
		if errors.Is(err, sql.ErrNoRows) {
			gone, err = true, nil
		}
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n > 0 && !gone {
			got, err := appendEvent(ctx, tx, e)
			if err != nil {
				return err
			}
			saved = []Event{got}
		}
		commitStateCrash()
		return nil
	})
	if err != nil {
		return nil, err
	}
	return saved, nil
}

// dropMarkers deletes every marker owed to parent, in tx.
func dropMarkers(ctx context.Context, tx *sql.Tx, parent string) error {
	_, err := tx.ExecContext(ctx, `DELETE FROM agent_completion_markers WHERE parent_agent_id = ?`, parent)
	return err
}
