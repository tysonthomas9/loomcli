package journal

import (
	"context"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

// CommitWorkspace writes every repo record and completes the journal entry in
// one SQLite transaction. A failed insert cannot leave a partial workspace.
func (s *SQLite) CommitWorkspace(ctx context.Context, entry loomgit.JournalEntry, repos []loomgit.WorkspaceRepo) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, repo := range repos {
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_repos(workspace,repo,trunk,workspace_branch,base_sha) VALUES (?,?,?,?,?)`, repo.Workspace, repo.Repo, repo.Trunk, repo.WorkspaceBranch, repo.BaseSHA); err != nil {
			return fmt.Errorf("record workspace repo %q: %w", repo.Repo, err)
		}
	}
	r, err := tx.ExecContext(ctx, `UPDATE journal_entries SET phase='done',version=version+1 WHERE id=? AND version=? AND fence=? AND phase='started'`, entry.ID, entry.Version, entry.Fence)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStale
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO journal_results(request_id,result) VALUES (?,?)`, entry.RequestID, []byte{}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLite) AbortWorkspace(ctx context.Context, entry loomgit.JournalEntry) error {
	r, err := s.db.ExecContext(ctx, `DELETE FROM journal_entries WHERE id=? AND version=? AND fence=? AND phase='started'`, entry.ID, entry.Version, entry.Fence)
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrStale
	}
	return nil
}

func (s *SQLite) WorkspaceRepos(ctx context.Context, workspace string) ([]loomgit.WorkspaceRepo, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT workspace,repo,trunk,workspace_branch,base_sha FROM workspace_repos WHERE workspace=? ORDER BY repo`, workspace)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var repos []loomgit.WorkspaceRepo
	for rows.Next() {
		var repo loomgit.WorkspaceRepo
		if err := rows.Scan(&repo.Workspace, &repo.Repo, &repo.Trunk, &repo.WorkspaceBranch, &repo.BaseSHA); err != nil {
			return nil, err
		}
		repos = append(repos, repo)
	}
	return repos, rows.Err()
}

var _ loomgit.WorkspaceStore = (*SQLite)(nil)
