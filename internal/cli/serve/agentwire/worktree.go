package agentwire

import (
	"context"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/ops"
)

// Worktree is the working copy of Agent API agent id in workspace ws, which
// the agent page's Git, Diff and Files tabs read through the v5 agent routes.
// ok is false when ws has no such agent or its worktree is not checked out.
func (a *API) Worktree(ctx context.Context, ws, id string) (*ops.AgentWorktree, bool) {
	ag, err := a.store.GetAgent(ctx, id)
	if err != nil || ag.WorkspaceID != ws || ag.DeletedAt != nil || ag.WorktreePath == nil {
		return nil, false
	}
	if _, err := os.Stat(filepath.Join(*ag.WorktreePath, ".git")); err != nil {
		return nil, false
	}
	base := "main"
	if ag.BaseRef != nil && *ag.BaseRef != "" {
		base = *ag.BaseRef
	}
	branch := ""
	if ag.Branch != nil {
		branch = *ag.Branch
	}
	return &ops.AgentWorktree{Name: id, Path: *ag.WorktreePath, Branch: branch, DefaultBranch: base,
		RepoName: filepath.Base(ag.Repo), AgentAPI: true}, true
}
