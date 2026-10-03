package agentwire

import (
	"context"
	"os"
	"path/filepath"

	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/ops"
)

// Worktree is the working copy of Agent API agent id in workspace ws, which
// the agent page's Git, Diff and Files tabs read through the v5 agent routes.
// ok is false when ws has no such agent or its worktree is not checked out.
func (a *API) Worktree(ctx context.Context, ws, id string) (*ops.AgentWorktree, bool) {
	ag, err := a.store.GetAgent(ctx, id)
	if err != nil || ag.WorkspaceID != ws {
		return nil, false
	}
	return worktreeOf(ag)
}

// Worktrees lists the checked-out working copies of workspace ws's Agent API
// agents, so the Files browser can count and group their changes.
func (a *API) Worktrees(ctx context.Context, ws string) []*ops.AgentWorktree {
	ags, _, err := a.store.ListAgents(ctx, loomstore.AgentFilter{WorkspaceID: ws})
	if err != nil {
		return nil
	}
	var out []*ops.AgentWorktree
	for _, ag := range ags {
		if wt, ok := worktreeOf(ag); ok {
			out = append(out, wt)
		}
	}
	return out
}

func worktreeOf(ag loomstore.Agent) (*ops.AgentWorktree, bool) {
	if ag.DeletedAt != nil || ag.WorktreePath == nil {
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
	return &ops.AgentWorktree{Name: ag.AgentID, Path: *ag.WorktreePath, Branch: branch, DefaultBranch: base,
		RepoName: filepath.Base(ag.Repo), AgentAPI: true}, true
}
