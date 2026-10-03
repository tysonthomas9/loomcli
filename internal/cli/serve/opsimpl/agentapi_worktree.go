package opsimpl

import (
	"context"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/ops"
)

// AgentAPIWorktree finds the worktree of Agent API agent id in workspace ws;
// ok is false when there is none.
type AgentAPIWorktree func(ctx context.Context, ws, id string) (wt *ops.AgentWorktree, ok bool)

// WithAgentAPI lets agent-scoped git, diff and file reads resolve Agent API
// agents too, so their agent page has Git, Diff and Files tabs. A v5 agent of
// the same name still wins.
func (g *GitOpsImpl) WithAgentAPI(fn AgentAPIWorktree) *GitOpsImpl {
	g.agentAPI = fn
	return g
}

// ResolveAgentWorktree resolves an agent name to its worktree info: a v5
// agent's, or else an Agent API agent's.
func (g *GitOpsImpl) ResolveAgentWorktree(workspaceID, name string) (*ops.AgentWorktree, error) {
	wt, err := g.resolveAgentWorktree(workspaceID, name)
	if err == nil {
		return wt, nil
	}
	if aw, ok := g.agentAPIWorktree(workspaceID, name, ""); ok {
		return aw, nil
	}
	return nil, err
}

// ResolveAgentWorktreeForRepo resolves one explicit agent+repo checkout: a v5
// agent's under <ws>/worktrees/<repo>/<agent>, or else an Agent API agent's
// when it is on repoName.
func (g *GitOpsImpl) ResolveAgentWorktreeForRepo(workspaceID, name, repoName string) (*ops.AgentWorktree, error) {
	wt, err := g.resolveAgentWorktreeForRepo(workspaceID, name, repoName)
	if err == nil {
		return wt, nil
	}
	if aw, ok := g.agentAPIWorktree(workspaceID, name, repoName); ok {
		return aw, nil
	}
	return nil, err
}

func (g *GitOpsImpl) agentAPIWorktree(workspaceID, name, repoName string) (*ops.AgentWorktree, bool) {
	if g == nil || g.agentAPI == nil || workspaceID == "" {
		return nil, false
	}
	wt, ok := g.agentAPI(context.Background(), workspaceID, name)
	if repoName = strings.TrimSpace(repoName); !ok || repoName != "" && wt.RepoName != repoName {
		return nil, false
	}
	return wt, true
}
