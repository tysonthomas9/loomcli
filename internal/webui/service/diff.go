package service

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/ops"
)

// DiffService defines business logic for git diff operations on agent worktrees.
// Handlers call this interface and map returned errors to HTTP responses.
type DiffService interface {
	// DiffCommits returns commit history between from (merge-base if empty) and HEAD.
	DiffCommits(ctx context.Context, wsID, agentName, from string, limit int) ([]ops.DiffCommitResult, error)

	// DiffFiles returns file-level diff summary between from and to refs.
	DiffFiles(ctx context.Context, wsID, agentName, from, to string) ([]ops.DiffFileResult, error)

	// DiffFilePatch returns the patch for a specific file between two refs.
	DiffFilePatch(ctx context.Context, wsID, agentName, from, to, filePath string) (*ops.DiffFilePatchResult, error)

	// GetIssueDiffStat returns diff statistics for an issue's assigned agent worktree.
	GetIssueDiffStat(ctx context.Context, wsID, issueID string) (*IssueDiffStatResult, error)
}

// IssueDiffStatResult contains diff statistics for an issue's assigned agent worktree.
type IssueDiffStatResult struct {
	Branch  string
	Added   int
	Removed int
}
