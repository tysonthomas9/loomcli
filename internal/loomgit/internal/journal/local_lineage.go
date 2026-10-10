package journal

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
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

// TaskHasChange reports whether task has a change in repo, ready or not.
func (s *SQLite) TaskHasChange(ctx context.Context, workspace, task, repo string) (bool, error) {
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM driver_changes
		WHERE workspace = ? AND task_id = ? AND repo = ? LIMIT 1`, workspace, task, repo).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
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

// LineageState is a dependent's standing against the predecessor revision it
// was built on (P3.1): "current", "stale" or "dependency_abandoned".
type LineageState struct {
	Pinned LocalLineage
	State  string
	// Rejected says the pinned predecessor revision was rejected.
	Rejected bool
	// Available is the predecessor's newest source revision after the pinned
	// one that is not rejected: what a rebuild would build on; 0 if none.
	Available int
}

// LineageStatus reads a dependent's lineage without moving its base. It is
// stale when the predecessor revision it was built on was rejected or a newer
// one exists (Tyson, 2026-10-09); a rebuild is never automatic.
func (s *SQLite) LineageStatus(ctx context.Context, workspace, task, repo string) (LineageState, error) {
	pinned, err := s.LocalLineage(ctx, workspace, task, repo)
	if err != nil {
		return LineageState{}, err
	}
	out := LineageState{Pinned: pinned, State: "current"}
	abandoned, err := s.ChangeAbandoned(ctx, workspace, pinned.PredecessorChange)
	if err != nil || abandoned {
		if abandoned {
			out.State = "dependency_abandoned"
		}
		return out, err
	}
	if out.Rejected, err = s.revisionRejected(ctx, workspace, pinned.PredecessorChange, pinned.PredecessorRevision); err != nil {
		return LineageState{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT number FROM change_revisions
		WHERE workspace=? AND change_id=? AND kind='source' AND ready=1 AND derived_from_change='' AND number>?
		ORDER BY number DESC`, workspace, pinned.PredecessorChange, pinned.PredecessorRevision)
	if err != nil {
		return LineageState{}, err
	}
	var newer []int
	for rows.Next() {
		var number int
		if err := rows.Scan(&number); err != nil {
			_ = rows.Close()
			return LineageState{}, err
		}
		newer = append(newer, number)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return LineageState{}, err
	}
	for _, number := range newer {
		rejected, err := s.revisionRejected(ctx, workspace, pinned.PredecessorChange, number)
		if err != nil {
			return LineageState{}, err
		}
		if !rejected {
			out.Available = number
			break
		}
	}
	if out.Rejected || len(newer) > 0 {
		out.State = "stale"
	}
	return out, nil
}

// Reason says, for a reviewer, why a dependent built on predecessor (a task
// ID) is not current and what a rebuild would build on. It is "" when current.
func (st LineageState) Reason(predecessor string) string {
	switch {
	case st.State == "dependency_abandoned":
		return fmt.Sprintf("%s was abandoned; this code was built on it", predecessor)
	case st.State != "stale":
		return ""
	}
	// Plain words for the reviewer: the API carries the revision numbers.
	base := fmt.Sprintf("built on an older version of %s's code", predecessor)
	if st.Rejected {
		base = fmt.Sprintf("built on %s's code, which was rejected", predecessor)
	}
	if st.Available > 0 {
		return fmt.Sprintf("%s: rebuild it on %s's new code", base, predecessor)
	}
	return fmt.Sprintf("%s: rebuild it once %s has new code", base, predecessor)
}

// DependentLineage reads the lineage of the task behind change: its state and
// the predecessor's task. found is false for a change built on no predecessor.
func (s *SQLite) DependentLineage(ctx context.Context, workspace, change string) (LineageState, string, bool, error) {
	var task, repo string
	err := s.db.QueryRowContext(ctx, `SELECT task_id, repo FROM driver_changes WHERE workspace=? AND change_id=?`,
		workspace, change).Scan(&task, &repo)
	if errors.Is(err, sql.ErrNoRows) {
		return LineageState{}, "", false, nil
	}
	if err != nil {
		return LineageState{}, "", false, err
	}
	state, err := s.LineageStatus(ctx, workspace, task, repo)
	if errors.Is(err, ErrNotFound) {
		return LineageState{}, "", false, nil
	}
	if err != nil {
		return LineageState{}, "", false, err
	}
	// A published dependent is rebuilt on its predecessor's new layer by the
	// stack's own restack, so its local base is never stale.
	if _, published, err := s.Publication(ctx, workspace, change); err != nil {
		return LineageState{}, "", false, err
	} else if published && state.State == "stale" {
		state.State, state.Rejected, state.Available = "current", false, 0
	}
	predecessor, err := s.TaskForChange(ctx, workspace, state.Pinned.PredecessorChange)
	if predecessor == "" {
		predecessor = state.Pinned.PredecessorChange
	}
	return state, predecessor, true, err
}

func (s *SQLite) revisionRejected(ctx context.Context, workspace, change string, number int) (bool, error) {
	verdict, err := s.LatestVerdict(ctx, loomgit.Revision{Workspace: workspace, Change: change, Number: number})
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return verdict.Kind == "reject", err
}

// ClearLocalLineage drops a dependent's pin, so its next attempt is built on
// the predecessor's newest revision. Only an explicit rebuild calls it.
func (s *SQLite) ClearLocalLineage(ctx context.Context, workspace, task, repo string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM local_lineage WHERE workspace=? AND task_id=? AND repo=?`,
		workspace, task, repo)
	return err
}

// TaskPredecessor is the change a dependent task's copy was pinned to start
// from, or "" when the task has no local lineage.
func (s *SQLite) TaskPredecessor(ctx context.Context, workspace, task string) (string, error) {
	var predecessor string
	err := s.db.QueryRowContext(ctx, `SELECT predecessor_change FROM local_lineage
		WHERE workspace = ? AND task_id = ? ORDER BY repo LIMIT 1`, workspace, task).Scan(&predecessor)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return predecessor, err
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
