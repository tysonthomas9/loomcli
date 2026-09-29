package journal

import (
	"context"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/loomgit/internal/layout/refname"
)

// EnsureDeletionSchema keeps deletion's retention record independent of the
// core journal schema so it can be landed and migrated separately.
func (s *SQLite) EnsureDeletionSchema(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS workspace_ref_tombstones (
		workspace TEXT PRIMARY KEY, ref_prefix TEXT NOT NULL,
		created_at TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
	)`)
	return err
}

// FinishWorkspaceDeletion records the ref prefix for retention and removes
// local creation records in one transaction after every worktree is gone.
func (s *SQLite) FinishWorkspaceDeletion(ctx context.Context, workspace string) error {
	prefix, err := refname.WorkspacePrefix(workspace)
	if err != nil {
		return err
	}
	if err := s.EnsureDeletionSchema(ctx); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO workspace_ref_tombstones(workspace,ref_prefix) VALUES(?,?)`, workspace, prefix); err != nil {
		return err
	}
	for _, query := range []string{
		`DELETE FROM workspace_repos WHERE workspace=?`,
		`DELETE FROM workspace_settings WHERE workspace=?`,
	} {
		if _, err = tx.ExecContext(ctx, query, workspace); err != nil {
			// Older stores have no workspace_settings table.
			if query == `DELETE FROM workspace_settings WHERE workspace=?` && strings.Contains(err.Error(), "no such table") {
				continue
			}
			return err
		}
	}
	attachPrefix := "workspace-add:" + workspace + ":"
	entryPredicate := `request_id=? OR substr(request_id,1,length(?))=?`
	if _, err = tx.ExecContext(ctx, `DELETE FROM event_outbox WHERE entry_id IN (SELECT id FROM journal_entries WHERE `+entryPredicate+`)`, "workspace-create:"+workspace, attachPrefix, attachPrefix); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM journal_results WHERE `+entryPredicate, "workspace-create:"+workspace, attachPrefix, attachPrefix); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM journal_entries WHERE `+entryPredicate, "workspace-create:"+workspace, attachPrefix, attachPrefix); err != nil {
		return err
	}
	return tx.Commit()
}
