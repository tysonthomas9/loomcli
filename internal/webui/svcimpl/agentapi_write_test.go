package svcimpl

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// agentAPIGitOps resolves "agt_1" as an Agent API agent's worktree and
// records every git or gh call; Reset really resets, so the reset shows in
// the worktree.
type agentAPIGitOps struct {
	ops.GitOps
	wt    *ops.AgentWorktree
	calls *[]string
}

func (g agentAPIGitOps) ResolveAgentWorktree(_, name string) (*ops.AgentWorktree, error) {
	if name != g.wt.Name {
		return nil, ops.ErrAgentWorktreeNotFound
	}
	return g.wt, nil
}

func (g agentAPIGitOps) call(name string) { *g.calls = append(*g.calls, name) }

func (g agentAPIGitOps) Push(_, _, _, _ string) (*ops.GitPushResult, error) {
	g.call("push")
	return &ops.GitPushResult{}, nil
}

func (g agentAPIGitOps) Pull(_, _, _, _ string) (*ops.GitPullResult, error) {
	g.call("pull")
	return &ops.GitPullResult{}, nil
}

func (g agentAPIGitOps) GetCurrentBranch(string) (string, error) {
	g.call("current-branch")
	return g.wt.Branch, nil
}

func (g agentAPIGitOps) CheckGhInstalled() error {
	g.call("gh")
	return nil
}

func (g agentAPIGitOps) CreatePR(_, _, _, _ string) (*ops.GitPRResult, error) {
	g.call("pr")
	return &ops.GitPRResult{}, nil
}

func (g agentAPIGitOps) SetRepoDefaultBranch(_, _, _ string) error {
	g.call("target")
	return nil
}

func (g agentAPIGitOps) Reset(path, _, target string, _, _ bool) (*ops.GitResetResult, error) {
	g.call("reset")
	for _, args := range [][]string{{"reset", "-q", "--hard", target}, {"clean", "-qfd"}} {
		cmd := exec.Command("git", args...) //nolint:norawexec // Test fake really resets a temp repo so the reset shows.
		cmd.Dir = path
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, errors.New(string(out))
		}
	}
	return &ops.GitResetResult{}, nil
}

// The v5 agent git and file routes write to an Agent API agent's worktree
// just as they do for a v5 agent (Tyson, 2026-10-04): each one reaches git,
// gh or the disk and answers 200.
func TestAgentAPIWorktreeIsWritable(t *testing.T) {
	ctx := context.Background()
	const id = "agt_1"
	wt := agentAPIGitWorktree(t, "loom/agent/"+id)
	var calls []string
	agents := NewAgentService(agentAPIGitOps{
		wt:    &ops.AgentWorktree{Name: id, Path: wt, Branch: "loom/agent/" + id, DefaultBranch: "main", RepoName: "repo-a", AgentAPI: true},
		calls: &calls,
	}, nil, nil, nil)
	wsRoot := t.TempDir()
	files := NewFileService(agentAPIFileOps{root: wt, scopedMockFileOps: scopedMockFileOps{wsRoot: wsRoot,
		wsData: &ops.WorkspaceData{ID: "WS1", Path: wsRoot, Agents: []ops.WorkspaceAgentInfo{{Name: "agent-a"}}}}})
	version := func(path string) string {
		t.Helper()
		got, err := files.ReadFileScoped(ctx, "WS1", service.ScopeAgent, id, "", path)
		if err != nil {
			t.Fatalf("ReadFileScoped %s: %v", path, err)
		}
		return got.Version
	}
	exists := func(path string) bool {
		_, err := os.Stat(filepath.Join(wt, path))
		return err == nil
	}

	git := []struct {
		name string
		call func() error
		want []string
	}{
		{"git/push", func() error { _, err := agents.GitPush(ctx, "WS1", id, "main"); return err }, []string{"push"}},
		{"git/pull", func() error { _, err := agents.GitPull(ctx, "WS1", id, "main"); return err }, []string{"current-branch", "pull"}},
		{"git/sync", func() error { _, err := agents.GitSync(ctx, "WS1", id); return err }, []string{"push", "current-branch", "pull"}},
		{"git/pr", func() error { _, err := agents.CreatePR(ctx, "WS1", id, "main"); return err }, []string{"gh", "pr"}},
	}
	for _, r := range git {
		calls = nil
		if got := statusOf(r.call()); got != 200 {
			t.Fatalf("%s: status = %d, want 200", r.name, got)
		}
		if strings.Join(calls, ",") != strings.Join(r.want, ",") {
			t.Fatalf("%s: git/gh calls = %v, want %v", r.name, calls, r.want)
		}
	}

	// git/target is no longer refused as an Agent API agent; it answers 400
	// only because an Agent API worktree is not in workspace mode, as before
	// 3.2t.
	if err := agents.SetTargetBranch(ctx, "WS1", id, "other"); statusOf(err) != 400 || !strings.Contains(err.Error(), "workspace mode") {
		t.Fatalf("git/target: %v (status %d), want the workspace-mode 400", err, statusOf(err))
	}

	if _, err := files.WriteFileConditionalScoped(ctx, "WS1", service.ScopeAgent, id, "", "new.txt", "x", service.FileWritePreconditions{}); err != nil || !exists("new.txt") {
		t.Fatalf("files write: %v (status %d)", err, statusOf(err))
	}
	if _, err := files.WriteFileConditionalScoped(ctx, "WS1", service.ScopeAgent, id, "", "scratch.txt", "x", service.FileWritePreconditions{}); err != nil {
		t.Fatalf("files overwrite: %v (status %d)", err, statusOf(err))
	}
	if b, _ := os.ReadFile(filepath.Join(wt, "scratch.txt")); string(b) != "x" {
		t.Fatalf("scratch.txt = %q after overwrite", b)
	}
	if err := files.MkdirScoped(ctx, "WS1", service.ScopeAgent, id, "", "newdir"); err != nil || !exists("newdir") {
		t.Fatalf("files mkdir: %v (status %d)", err, statusOf(err))
	}
	if _, err := files.MovePathVersionedScoped(ctx, "WS1", service.ScopeAgent, id, "", "scratch.txt", "moved.txt", false, version("scratch.txt"), ""); err != nil || !exists("moved.txt") || exists("scratch.txt") {
		t.Fatalf("files move: %v (status %d)", err, statusOf(err))
	}
	if err := files.DeletePathVersionedScoped(ctx, "WS1", service.ScopeAgent, id, "", "moved.txt", false, version("moved.txt")); err != nil || exists("moved.txt") {
		t.Fatalf("files delete: %v (status %d)", err, statusOf(err))
	}
	if _, err := files.RepairCheckout(ctx, "WS1", service.FileCheckoutRepairRequest{Scope: "agent", Target: id, Force: true}); err != nil {
		t.Fatalf("files repair: %v (status %d)", err, statusOf(err))
	}

	calls = nil
	if _, err := agents.GitReset(ctx, "WS1", id, "main", true, false); err != nil {
		t.Fatalf("git/reset: %v (status %d)", err, statusOf(err))
	}
	if got, want := strings.TrimSpace(gitOutput(t, wt, "rev-parse", "HEAD")), strings.TrimSpace(gitOutput(t, wt, "rev-parse", "main")); got != want || exists("b.txt") || exists("new.txt") {
		t.Fatalf("git/reset left HEAD %s (main %s), b.txt %v, new.txt %v", got, want, exists("b.txt"), exists("new.txt"))
	}
}

func statusOf(err error) int {
	var se *service.ServiceError
	if errors.As(err, &se) {
		return handler.StatusForKind(se.Kind)
	}
	if err != nil {
		return 500
	}
	return 200
}

// agentAPIGitWorktree is a repo on the agent branch one commit ahead of
// main, with an untracked file.
func agentAPIGitWorktree(t *testing.T, branch string) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		mustGit(t, dir, append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	}
	git("init", "-q", "-b", "main")
	writeTestFile(t, filepath.Join(dir, "a.txt"), "a\n")
	git("add", "a.txt")
	git("commit", "-q", "-m", "base")
	git("checkout", "-q", "-b", branch)
	writeTestFile(t, filepath.Join(dir, "b.txt"), "b\n")
	git("add", "b.txt")
	git("commit", "-q", "-m", "work")
	writeTestFile(t, filepath.Join(dir, "scratch.txt"), "keep\n")
	return dir
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
