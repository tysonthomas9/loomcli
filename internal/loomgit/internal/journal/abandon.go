package journal

import (
	"context"
	"database/sql"
	"errors"
)

type Abandonment struct {
	Workspace, Change, Reason, Repo, Branch, Slug, Head string
	Task                                                string
	Worktree, SourceRepo                                string
	ClaimActor                                          string
	RequestedBy                                         string
	PRNumber                                            int
	RetentionEligible                                   bool
	ClaimReleased                                       bool
	ClosePR, DeleteRemote, PRClosed, RemoteDeleted      bool
}

func createAbandonSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS change_abandonments (
		workspace TEXT NOT NULL, change_id TEXT NOT NULL, task_id TEXT NOT NULL, reason TEXT NOT NULL,
		repo TEXT NOT NULL, branch TEXT NOT NULL, slug TEXT NOT NULL,
		head_sha TEXT NOT NULL, worktree TEXT NOT NULL, source_repo TEXT NOT NULL,
		claim_actor TEXT NOT NULL, requested_by TEXT NOT NULL, claim_released INTEGER NOT NULL DEFAULT 0,
		retention_eligible INTEGER NOT NULL, pr_number INTEGER NOT NULL,
		close_pr INTEGER NOT NULL, delete_remote INTEGER NOT NULL,
		pr_closed INTEGER NOT NULL DEFAULT 0, remote_deleted INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY(workspace, change_id)
	)`)
	return err
}

func (s *SQLite) BeginAbandonment(ctx context.Context, row Abandonment) error {
	if row.Workspace == "" || row.Change == "" || row.Reason == "" {
		return errors.New("workspace, change and reason are required")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO change_abandonments
		(workspace,change_id,task_id,reason,repo,branch,slug,head_sha,worktree,source_repo,claim_actor,requested_by,
		 retention_eligible,pr_number,close_pr,delete_remote)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(workspace,change_id) DO NOTHING`,
		row.Workspace, row.Change, row.Task, row.Reason, row.Repo, row.Branch, row.Slug, row.Head,
		row.Worktree, row.SourceRepo, row.ClaimActor, row.RequestedBy, row.RetentionEligible, row.PRNumber, row.ClosePR, row.DeleteRemote)
	return err
}

func (s *SQLite) Abandonment(ctx context.Context, workspace, change string) (Abandonment, bool, error) {
	var row Abandonment
	err := s.db.QueryRowContext(ctx, `SELECT workspace,change_id,task_id,reason,repo,branch,slug,head_sha,
		worktree,source_repo,claim_actor,requested_by,claim_released,retention_eligible,pr_number,
		close_pr,delete_remote,pr_closed,remote_deleted FROM change_abandonments
		WHERE workspace=? AND change_id=?`, workspace, change).Scan(&row.Workspace, &row.Change, &row.Task,
		&row.Reason, &row.Repo, &row.Branch, &row.Slug, &row.Head,
		&row.Worktree, &row.SourceRepo, &row.ClaimActor, &row.RequestedBy, &row.ClaimReleased,
		&row.RetentionEligible, &row.PRNumber,
		&row.ClosePR, &row.DeleteRemote, &row.PRClosed, &row.RemoteDeleted)
	if errors.Is(err, sql.ErrNoRows) {
		return Abandonment{}, false, nil
	}
	return row, err == nil, err
}

func (s *SQLite) OpenAbandonments(ctx context.Context) ([]Abandonment, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT a.workspace,a.change_id FROM change_abandonments a
		LEFT JOIN abandoned_changes c ON c.workspace=a.workspace AND c.change_id=a.change_id
		WHERE c.change_id IS NULL OR a.claim_released=0 OR
		(a.close_pr=1 AND a.pr_closed=0) OR (a.delete_remote=1 AND a.remote_deleted=0)
		ORDER BY a.workspace,a.change_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []Abandonment
	for rows.Next() {
		var workspace, change string
		if err := rows.Scan(&workspace, &change); err != nil {
			return nil, err
		}
		row, _, err := s.Abandonment(ctx, workspace, change)
		if err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func (s *SQLite) MarkAbandonmentClaimReleased(ctx context.Context, workspace, change string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE change_abandonments SET claim_released=1
		WHERE workspace=? AND change_id=?`, workspace, change)
	return err
}

func (s *SQLite) AdvanceAbandonment(ctx context.Context, workspace, change string, closed, deleted bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE change_abandonments SET
		pr_closed=pr_closed OR ?, remote_deleted=remote_deleted OR ?
		WHERE workspace=? AND change_id=?`, closed, deleted, workspace, change)
	return err
}

func (s *SQLite) EnableAbandonmentActions(ctx context.Context, workspace, change string, closePR, deleteRemote bool) error {
	_, err := s.db.ExecContext(ctx, `UPDATE change_abandonments SET
		close_pr=close_pr OR ?, delete_remote=delete_remote OR ?
		WHERE workspace=? AND change_id=?`, closePR, deleteRemote, workspace, change)
	return err
}
