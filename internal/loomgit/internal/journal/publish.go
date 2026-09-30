package journal

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/loomgit"
)

// RevisionByHead finds the recorded revision represented by an applied layer.
func (s *SQLite) RevisionByHead(ctx context.Context, workspace, change, head string) (loomgit.Revision, error) {
	return scanRevision(s.db.QueryRowContext(ctx, `SELECT `+revisionColumns+` FROM change_revisions
		WHERE workspace = ? AND change_id = ? AND head_sha = ? AND ready = 1
		ORDER BY number DESC LIMIT 1`, workspace, change, head))
}
