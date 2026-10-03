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

func (s *SQLite) DependencyForChange(ctx context.Context, workspace, change string) (string, error) {
	var predecessor string
	err := s.db.QueryRowContext(ctx, `SELECT l.predecessor_change FROM driver_changes d
		JOIN local_lineage l ON l.workspace=d.workspace AND l.task_id=d.task_id AND l.repo=d.repo
		WHERE d.workspace=? AND d.change_id=?`, workspace, change).Scan(&predecessor)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return predecessor, err
}

// DependentsOf returns the task copies pinned to a predecessor change, and the
// layers Approve stacked on it. A task in both keeps its declared lineage.
func (s *SQLite) DependentsOf(ctx context.Context, workspace, change string) ([]LocalLineage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT task_id, repo, predecessor_revision, base_sha
		FROM local_lineage WHERE workspace = ? AND predecessor_change = ?
		UNION ALL
		SELECT a.task_id, a.repo, a.predecessor_revision, a.base_sha FROM approval_lineage a
		WHERE a.workspace = ? AND a.predecessor_change = ? AND NOT EXISTS (SELECT 1 FROM local_lineage l
			WHERE l.workspace = a.workspace AND l.task_id = a.task_id AND l.repo = a.repo
			AND l.predecessor_change = a.predecessor_change)
		ORDER BY 1, 2`, workspace, change, workspace, change)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var dependents []LocalLineage
	for rows.Next() {
		l := LocalLineage{Workspace: workspace, PredecessorChange: change}
		if err := rows.Scan(&l.Task, &l.Repo, &l.PredecessorRevision, &l.BaseSHA); err != nil {
			return nil, err
		}
		dependents = append(dependents, l)
	}
	return dependents, rows.Err()
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

// approval_lineage records which change each layer of a lead's published stack
// sits on. Tasks approved one after another on a lead have no declared
// predecessor, so local_lineage stays empty for them; landing reads this table
// too, so the layer above a merged PR is restacked onto trunk the same way a
// declared dependent is. It is kept apart from local_lineage because that
// table also pins a task copy's base, holds approval following and holds
// trunk-mode publishing, none of which apply to a stack built by Approve.
func createApprovalLineageSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS approval_lineage (
		workspace TEXT NOT NULL, task_id TEXT NOT NULL, repo TEXT NOT NULL, lead TEXT NOT NULL,
		change_id TEXT NOT NULL, predecessor_change TEXT NOT NULL,
		predecessor_revision INTEGER NOT NULL, base_sha TEXT NOT NULL,
		PRIMARY KEY(workspace, task_id, repo)
	);
	CREATE INDEX IF NOT EXISTS approval_lineage_predecessor ON approval_lineage(workspace, predecessor_change);`)
	return err
}

// StackLayer is one applied layer of a lead's published stack, bottom first.
type StackLayer struct {
	Change   string
	Revision int
	Head     string
}

// SyncApprovalLineage makes lead's approval lineage in repo match its published
// stack: each task layer points at the layer below it, the bottom layer
// points at nothing, and tasks no longer in the stack lose their entry.
// Re-publishing the same stack leaves the rows unchanged.
func (s *SQLite) SyncApprovalLineage(ctx context.Context, workspace, lead, repo string, layers []StackLayer) error {
	if workspace == "" || lead == "" || repo == "" {
		return errors.New("workspace, lead and repo are required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	kept := make(map[string]bool, len(layers))
	var below StackLayer
	for index, layer := range layers {
		task, err := upsertApprovalLineage(ctx, tx, workspace, lead, repo, layer, below, index > 0)
		if err != nil {
			return err
		}
		if task != "" {
			kept[task] = true
		}
		below = layer
	}
	if err := pruneApprovalLineage(ctx, tx, workspace, lead, repo, kept); err != nil {
		return err
	}
	return tx.Commit()
}

// upsertApprovalLineage points layer's task at below and returns the task, or
// returns "" when layer has no task or is the bottom of the stack.
func upsertApprovalLineage(ctx context.Context, tx *sql.Tx, workspace, lead, repo string,
	layer, below StackLayer, hasBelow bool) (string, error) {
	var task string
	err := tx.QueryRowContext(ctx, `SELECT task_id FROM driver_changes
		WHERE workspace=? AND change_id=? AND repo=?`, workspace, layer.Change, repo).Scan(&task)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && !hasBelow) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if below.Change == "" || below.Head == "" {
		return "", errors.New("incomplete approval lineage")
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO approval_lineage
		(workspace, task_id, repo, lead, change_id, predecessor_change, predecessor_revision, base_sha)
		VALUES (?,?,?,?,?,?,?,?)
		ON CONFLICT(workspace, task_id, repo) DO UPDATE SET lead=excluded.lead,
		change_id=excluded.change_id, predecessor_change=excluded.predecessor_change,
		predecessor_revision=excluded.predecessor_revision, base_sha=excluded.base_sha`,
		workspace, task, repo, lead, layer.Change, below.Change, below.Revision, below.Head)
	return task, err
}

// pruneApprovalLineage deletes lead's rows in repo for tasks not kept.
func pruneApprovalLineage(ctx context.Context, tx *sql.Tx, workspace, lead, repo string, kept map[string]bool) error {
	rows, err := tx.QueryContext(ctx, `SELECT task_id FROM approval_lineage
		WHERE workspace=? AND lead=? AND repo=?`, workspace, lead, repo)
	if err != nil {
		return err
	}
	var stale []string
	for rows.Next() {
		var task string
		if err := rows.Scan(&task); err != nil {
			_ = rows.Close()
			return err
		}
		if !kept[task] {
			stale = append(stale, task)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, task := range stale {
		if _, err := tx.ExecContext(ctx, `DELETE FROM approval_lineage
			WHERE workspace=? AND task_id=? AND repo=?`, workspace, task, repo); err != nil {
			return err
		}
	}
	return nil
}

// spliceApprovalLineage removes an unapplied change from its lead's stack
// lineage inside the Unapply transaction: the layer above it now sits on the
// layer below it, or on nothing when it was the bottom.
func spliceApprovalLineage(ctx context.Context, tx *sql.Tx, workspace, lead, removed string) error {
	var task, repo, predecessor, base string
	var revision int
	err := tx.QueryRowContext(ctx, `SELECT task_id, repo, predecessor_change, predecessor_revision, base_sha
		FROM approval_lineage WHERE workspace=? AND lead=? AND change_id=?`, workspace, lead, removed).
		Scan(&task, &repo, &predecessor, &revision, &base)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `DELETE FROM approval_lineage
			WHERE workspace=? AND lead=? AND predecessor_change=?`, workspace, lead, removed)
		return err
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE approval_lineage SET predecessor_change=?,
		predecessor_revision=?, base_sha=? WHERE workspace=? AND lead=? AND predecessor_change=?`,
		predecessor, revision, base, workspace, lead, removed); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM approval_lineage WHERE workspace=? AND task_id=? AND repo=?`,
		workspace, task, repo)
	return err
}

// ApprovalLineage reads the stack lineage Approve recorded for one task.
func (s *SQLite) ApprovalLineage(ctx context.Context, workspace, task, repo string) (LocalLineage, error) {
	l := LocalLineage{Workspace: workspace, Task: task, Repo: repo}
	err := s.db.QueryRowContext(ctx, `SELECT predecessor_change, predecessor_revision, base_sha
		FROM approval_lineage WHERE workspace=? AND task_id=? AND repo=?`, workspace, task, repo).
		Scan(&l.PredecessorChange, &l.PredecessorRevision, &l.BaseSHA)
	if errors.Is(err, sql.ErrNoRows) {
		return LocalLineage{}, ErrNotFound
	}
	return l, err
}
