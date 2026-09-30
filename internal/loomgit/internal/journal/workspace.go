package journal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
	var planData []byte
	if err := tx.QueryRowContext(ctx, `SELECT plan FROM workspace_creations WHERE entry_id=?`, entry.ID).Scan(&planData); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var plan WorkspaceCreation
	if len(planData) > 0 {
		if err := json.Unmarshal(planData, &plan); err != nil {
			return err
		}
	}
	for _, repo := range repos {
		if _, err := tx.ExecContext(ctx, `INSERT INTO workspace_repos(workspace,repo,trunk,workspace_branch,base_sha) VALUES (?,?,?,?,?)`, repo.Workspace, repo.Repo, repo.Trunk, repo.WorkspaceBranch, repo.BaseSHA); err != nil {
			return fmt.Errorf("record workspace repo %q: %w", repo.Repo, err)
		}
		for _, source := range plan.Repos {
			if source.Name == repo.Repo && source.Path != "" {
				mode := source.Mode
				if mode == "" {
					mode = "worktree"
				}
				if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO working_areas(workspace,lead,repo,path,branch,base_sha,mode)
				VALUES (?,?,?,?,?,?,?)`, repo.Workspace, "lead", repo.Repo, source.Path, repo.WorkspaceBranch, repo.BaseSHA, mode); err != nil {
					return err
				}
				break
			}
		}
	}
	r, err := tx.ExecContext(ctx, `UPDATE journal_entries SET phase='done',version=version+1 WHERE id=? AND version=? AND fence=? AND (phase='rows_written' OR (operation='attach_workspace_repos' AND phase='started'))`, entry.ID, entry.Version, entry.Fence)
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
	r, err := s.db.ExecContext(ctx, `DELETE FROM journal_entries WHERE id=? AND version=? AND fence=? AND phase<>'done'`, entry.ID, entry.Version, entry.Fence)
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
