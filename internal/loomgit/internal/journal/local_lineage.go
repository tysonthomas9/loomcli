package journal

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"sort"
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

// LeadStackID is the stack a lead's applied layers publish as.
func LeadStackID(lead string) string {
	return fmt.Sprintf("lead-%x", sha256.Sum256([]byte(lead)))
}

// LeadRepoStackID is one repository's stack of a cross-repo lead.
func LeadRepoStackID(lead, repo string) string {
	return fmt.Sprintf("lead-%x", sha256.Sum256([]byte(lead+"\x00"+repo)))
}

// DependentsOf returns the task copies pinned to a predecessor change, and the
// task layers whose PR sits on its PR in a lead's published stack. Tasks
// approved one after another on a lead (D29) declare no predecessor; their
// stack order lives only in the publication records, which the publish
// transaction writes under the stack lease, so it is read from there rather
// than copied. Only layers still applied count, so Unapply drops a layer at
// once. A task with a declared lineage to the same change is listed once.
func (s *SQLite) DependentsOf(ctx context.Context, workspace, change string) ([]LocalLineage, error) {
	dependents, err := s.declaredDependents(ctx, workspace, change)
	if err != nil {
		return nil, err
	}
	stacked, err := s.leadStackDependents(ctx, workspace, change)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(dependents))
	for _, l := range dependents {
		seen[l.Task+"\x00"+l.Repo] = true
	}
	for _, l := range stacked {
		if !seen[l.Task+"\x00"+l.Repo] {
			seen[l.Task+"\x00"+l.Repo] = true
			dependents = append(dependents, l)
		}
	}
	sort.Slice(dependents, func(i, j int) bool {
		if dependents[i].Task != dependents[j].Task {
			return dependents[i].Task < dependents[j].Task
		}
		return dependents[i].Repo < dependents[j].Repo
	})
	return dependents, nil
}

func (s *SQLite) declaredDependents(ctx context.Context, workspace, change string) ([]LocalLineage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT task_id, repo, predecessor_revision, base_sha
		FROM local_lineage WHERE workspace = ? AND predecessor_change = ?
		ORDER BY task_id, repo`, workspace, change)
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

// leadStackDependents reads the applied task layers whose PR targets change's
// PR branch in the stack their lead publishes. A stack declared with
// `loom stack` is never a lead's stack, whatever its name.
func (s *SQLite) leadStackDependents(ctx context.Context, workspace, change string) ([]LocalLineage, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT d.task_id, d.repo, below.head_sha, below.stack_id, a.lead
		FROM change_publications below
		JOIN change_publications above ON above.workspace = below.workspace
			AND above.stack_id = below.stack_id AND above.trunk = below.branch
			AND above.change_id <> below.change_id
		JOIN driver_changes d ON d.workspace = above.workspace AND d.change_id = above.change_id
		JOIN applied_layers a ON a.workspace = above.workspace AND a.change_id = above.change_id
			AND a.phase = 'done'
		WHERE below.workspace = ? AND below.change_id = ? AND below.stack_id <> ''
		ORDER BY 1, 2`, workspace, change)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var dependents []LocalLineage
	for rows.Next() {
		l := LocalLineage{Workspace: workspace, PredecessorChange: change}
		var stack, lead string
		if err := rows.Scan(&l.Task, &l.Repo, &l.BaseSHA, &stack, &lead); err != nil {
			return nil, err
		}
		if stack == LeadStackID(lead) || stack == LeadRepoStackID(lead, l.Repo) {
			dependents = append(dependents, l)
		}
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
