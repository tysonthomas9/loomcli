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

// readOnlyGitOps resolves "agt_1" as an Agent API agent's worktree and
// records every git or gh call; Reset really resets, so a reset that gets
// through shows in the worktree.
type readOnlyGitOps struct {
	ops.GitOps
	wt    *ops.AgentWorktree
	calls *[]string
}

func (g readOnlyGitOps) ResolveAgentWorktree(_, name string) (*ops.AgentWorktree, error) {
	if name != g.wt.Name {
		return nil, ops.ErrAgentWorktreeNotFound
	}
	return g.wt, nil
}

func (g readOnlyGitOps) call(name string) { *g.calls = append(*g.calls, name) }

func (g readOnlyGitOps) Push(_, _, _, _ string) (*ops.GitPushResult, error) {
	g.call("push")
	return &ops.GitPushResult{}, nil
}

func (g readOnlyGitOps) Pull(_, _, _, _ string) (*ops.GitPullResult, error) {
	g.call("pull")
	return &ops.GitPullResult{}, nil
}

func (g readOnlyGitOps) GetCurrentBranch(string) (string, error) {
	g.call("current-branch")
	return g.wt.Branch, nil
}

func (g readOnlyGitOps) CheckGhInstalled() error {
	g.call("gh")
	return nil
}

func (g readOnlyGitOps) CreatePR(_, _, _, _ string) (*ops.GitPRResult, error) {
	g.call("pr")
	return &ops.GitPRResult{}, nil
}

func (g readOnlyGitOps) SetRepoDefaultBranch(_, _, _ string) error {
	g.call("target")
	return nil
}

func (g readOnlyGitOps) Reset(path, _, target string, _, _ bool) (*ops.GitResetResult, error) {
	g.call("reset")
	for _, args := range [][]string{{"reset", "-q", "--hard", target}, {"clean", "-qfd"}} {
		cmd := exec.Command("git", args...) //nolint:norawexec // Test fake really resets a temp repo so a reset that gets through shows.
		cmd.Dir = path
		if out, err := cmd.CombinedOutput(); err != nil {
			return nil, errors.New(string(out))
		}
	}
	return &ops.GitResetResult{}, nil
}

func (g readOnlyGitOps) Status(_, _ string) (*ops.GitStatusResult, error) {
	g.call("status")
	return &ops.GitStatusResult{}, nil
}

// An Agent API agent's worktree is read-only through the v5 agent git and
// file routes (3.2t): every mutating route answers 403 before any git or gh
// call and leaves the worktree's HEAD, branch and untracked files as they
// were.
func TestAgentAPIWorktreeIsReadOnly(t *testing.T) {
	ctx := context.Background()
	const id = "agt_1"
	wt := agentAPIGitWorktree(t, "loom/agent/"+id)
	var calls []string
	agents := NewAgentService(readOnlyGitOps{
		wt:    &ops.AgentWorktree{Name: id, Path: wt, Branch: "loom/agent/" + id, DefaultBranch: "main", RepoName: "repo-a", AgentAPI: true},
		calls: &calls,
	}, nil, nil, nil)
	wsRoot := t.TempDir()
	files := NewFileService(agentAPIFileOps{root: wt, scopedMockFileOps: scopedMockFileOps{wsRoot: wsRoot,
		wsData: &ops.WorkspaceData{ID: "WS1", Path: wsRoot, Agents: []ops.WorkspaceAgentInfo{{Name: "agent-a"}}}}})
	before := worktreeState(t, wt)

	routes := []struct {
		name string
		call func() error
	}{
		{"git/push", func() error { _, err := agents.GitPush(ctx, "WS1", id, "main"); return err }},
		{"git/pull", func() error { _, err := agents.GitPull(ctx, "WS1", id, "main"); return err }},
		{"git/sync", func() error { _, err := agents.GitSync(ctx, "WS1", id); return err }},
		{"git/pr", func() error { _, err := agents.CreatePR(ctx, "WS1", id, "main"); return err }},
		{"git/reset", func() error { _, err := agents.GitReset(ctx, "WS1", id, "main", true, false); return err }},
		{"git/target", func() error { return agents.SetTargetBranch(ctx, "WS1", id, "other") }},
		{"files write", func() error {
			_, err := files.WriteFileConditionalScoped(ctx, "WS1", service.ScopeAgent, id, "", "new.txt", "x", service.FileWritePreconditions{})
			return err
		}},
		{"files overwrite", func() error {
			_, err := files.WriteFileConditionalScoped(ctx, "WS1", service.ScopeAgent, id, "", "scratch.txt", "x", service.FileWritePreconditions{})
			return err
		}},
		{"files delete", func() error {
			return files.DeletePathVersionedScoped(ctx, "WS1", service.ScopeAgent, id, "", "scratch.txt", false, "v")
		}},
		{"files mkdir", func() error { return files.MkdirScoped(ctx, "WS1", service.ScopeAgent, id, "", "newdir") }},
		{"files move", func() error {
			_, err := files.MovePathVersionedScoped(ctx, "WS1", service.ScopeAgent, id, "", "scratch.txt", "moved.txt", false, "v", "")
			return err
		}},
		{"files repair", func() error {
			_, err := files.RepairCheckout(ctx, "WS1", service.FileCheckoutRepairRequest{Scope: "agent", Target: id, Force: true})
			return err
		}},
	}
	for _, r := range routes {
		t.Run(r.name, func(t *testing.T) {
			calls = nil
			if got := statusOf(r.call()); got != 403 {
				t.Fatalf("status = %d, want 403", got)
			}
			if len(calls) != 0 {
				t.Fatalf("git/gh calls before the refusal: %v", calls)
			}
			if after := worktreeState(t, wt); after != before {
				t.Fatalf("worktree changed:\nbefore %s\nafter  %s", before, after)
			}
		})
	}

	// Reads still serve the worktree.
	if _, err := agents.GitStatus(ctx, "WS1", id); err != nil {
		t.Fatalf("GitStatus: %v", err)
	}
	if got, err := files.ReadFileScoped(ctx, "WS1", service.ScopeAgent, id, "", "scratch.txt"); err != nil || got.Content != "keep\n" {
		t.Fatalf("ReadFileScoped = %+v, %v", got, err)
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

// worktreeState is HEAD, the branch, the porcelain status and the files on
// disk, so any write shows up.
func worktreeState(t *testing.T, dir string) string {
	t.Helper()
	run := func(args ...string) string {
		t.Helper()
		return strings.TrimSpace(gitOutput(t, dir, args...))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	scratch, _ := os.ReadFile(filepath.Join(dir, "scratch.txt"))
	return strings.Join([]string{
		run("rev-parse", "HEAD"),
		run("rev-parse", "--abbrev-ref", "HEAD"),
		run("status", "--porcelain", "--untracked-files=all"),
		strings.Join(names, ","),
		string(scratch),
	}, " | ")
}
