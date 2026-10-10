package opsimpl

import (
	"context"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// An Agent API agent is in no v5 agent list, so the agent git, diff and file
// routes find its worktree through the Agent API instead (3.2t).
func TestResolveAgentWorktree_FallsBackToAgentAPI(t *testing.T) {
	ctx := context.Background()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "Workspace One"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	want := &ops.AgentWorktree{Name: "agt_1", Path: "/wt/api/agt_1", RepoName: "api", AgentAPI: true}
	var asked []string
	g := NewGitOps().WithStore(st).WithAgentAPI(func(_ context.Context, ws, id string) (*ops.AgentWorktree, bool) {
		asked = append(asked, ws+"/"+id)
		return want, id == "agt_1"
	})

	if got, err := g.ResolveAgentWorktree("WS1", "agt_1"); err != nil || got != want {
		t.Fatalf("ResolveAgentWorktree = %v, %v; want the Agent API worktree", got, err)
	}
	if got, err := g.ResolveAgentWorktreeForRepo("WS1", "agt_1", " api "); err != nil || got != want {
		t.Fatalf("ResolveAgentWorktreeForRepo(api) = %v, %v; want the Agent API worktree", got, err)
	}
	if _, err := g.ResolveAgentWorktreeForRepo("WS1", "agt_1", "docs"); err == nil {
		t.Fatal("ResolveAgentWorktreeForRepo(docs) found the agent on another repo")
	}
	if _, err := g.ResolveAgentWorktree("WS1", "nobody"); err == nil {
		t.Fatal("ResolveAgentWorktree found an agent neither side knows")
	}
	if len(asked) != 4 || asked[0] != "WS1/agt_1" {
		t.Fatalf("Agent API asked %v", asked)
	}
}

// The Files browser lists Agent API agents' worktrees through the lister the
// serve wiring sets; without it there are none (GT1).
func TestListAgentAPIWorktrees(t *testing.T) {
	ctx := context.Background()
	if got := NewGitOps().ListAgentAPIWorktrees(ctx, "WS1"); got != nil {
		t.Fatalf("unwired ListAgentAPIWorktrees = %v", got)
	}
	want := &ops.AgentWorktree{Name: "agt_1", Path: "/wt/api/agt_1", RepoName: "api", AgentAPI: true}
	g := NewGitOps().WithAgentAPIWorktrees(func(_ context.Context, ws string) []*ops.AgentWorktree {
		if ws != "WS1" {
			return nil
		}
		return []*ops.AgentWorktree{want}
	})
	if got := g.ListAgentAPIWorktrees(ctx, "WS1"); len(got) != 1 || got[0] != want {
		t.Fatalf("ListAgentAPIWorktrees(WS1) = %v", got)
	}
	if got := g.ListAgentAPIWorktrees(ctx, ""); got != nil {
		t.Fatalf("ListAgentAPIWorktrees(\"\") = %v", got)
	}
}
