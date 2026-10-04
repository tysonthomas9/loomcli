package svcimpl

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// agentAPIFileOps resolves "agt_1" as an Agent API agent's worktree and
// "stale" as a v5-looking worktree no listed agent owns.
type agentAPIFileOps struct {
	scopedMockFileOps
	root string
}

func (m agentAPIFileOps) ResolveAgentWorktree(_, name string) (*ops.AgentWorktree, error) {
	if name != "agt_1" && name != "stale" {
		return nil, ops.ErrAgentWorktreeNotFound
	}
	return &ops.AgentWorktree{Name: name, Path: m.root, RepoName: "repo-a", AgentAPI: name == "agt_1"}, nil
}

// An Agent API agent is not in the workspace's agent list, yet its Files tab
// browses its own worktree (3.2t). Other unlisted names stay not found.
func TestFileServiceImpl_AgentScopeServesAgentAPIWorktree(t *testing.T) {
	wsRoot, root := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.js"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := NewFileService(agentAPIFileOps{root: root, scopedMockFileOps: scopedMockFileOps{wsRoot: wsRoot,
		wsData: &ops.WorkspaceData{ID: "ws", Path: wsRoot, Agents: []ops.WorkspaceAgentInfo{{Name: "agent-a"}}}}})

	index, err := svc.IndexFilesScoped(context.Background(), "ws", service.ScopeAgent, "agt_1", "")
	if err != nil {
		t.Fatalf("IndexFilesScoped agt_1: %v", err)
	}
	if !containsPath(index.Paths, "app.js") {
		t.Fatalf("agt_1 index = %+v, missing app.js", index.Paths)
	}
	if _, err := svc.IndexFilesScoped(context.Background(), "ws", service.ScopeAgent, "stale", ""); err == nil {
		t.Fatal("an unlisted v5 worktree was served")
	}
}

// ListAgentAPIWorktrees lists agt_1's worktree as the workspace's only Agent
// API agent.
func (m agentAPIFileOps) ListAgentAPIWorktrees(_ context.Context, ws string) []*ops.AgentWorktree {
	if ws != "ws" {
		return nil
	}
	return []*ops.AgentWorktree{{Name: "agt_1", Path: m.root, RepoName: "repo-a", AgentAPI: true}}
}

// The Files browser's checkout list includes an Agent API agent's worktree,
// outside the workspace folder, with its uncommitted change count, so its
// Changes badge and Working tree show its edits (GT1).
func TestFileServiceImpl_ListFileCheckouts_IncludesAgentAPIAgents(t *testing.T) {
	ctx := context.Background()
	wsRoot, root := t.TempDir(), t.TempDir()
	initGitRepo(t, root)
	mustWrite(t, filepath.Join(root, "README.md"), "one\n")
	commitAll(t, root)
	mustWrite(t, filepath.Join(root, "README.md"), "two\n")
	mustWrite(t, filepath.Join(root, "notes.txt"), "new\n")
	svc := NewFileService(agentAPIFileOps{root: root, scopedMockFileOps: scopedMockFileOps{wsRoot: wsRoot,
		wsData: &ops.WorkspaceData{ID: "ws", Path: wsRoot, Repos: []ops.WorkspaceRepo{{Name: "repo-a"}},
			Agents: []ops.WorkspaceAgentInfo{{Name: "agent-a"}}}}})

	result, err := svc.ListFileCheckouts(ctx, "ws")
	if err != nil {
		t.Fatalf("ListFileCheckouts: %v", err)
	}
	var got *service.FileCheckout
	for i, c := range result.Checkouts {
		if c.Agent == "agt_1" {
			got = &result.Checkouts[i]
		}
	}
	if got == nil || got.Kind != "agent" || got.Repo != "repo-a" || !got.Exists || got.ChangeCount != 2 || got.Branch == "" {
		t.Fatalf("agt_1 checkout = %+v in %+v", got, result.Checkouts)
	}
}
