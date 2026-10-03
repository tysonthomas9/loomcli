package svcimpl

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/cli/serve/opsimpl"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/ops"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/webui/server/handler"
	"github.com/tysonthomas9/loomcli/internal/webui/service"
)

// An Agent API agent's worktree is read-only through the v5 agent git and
// file routes (3.2t): every mutating route answers 403 and leaves the
// worktree's HEAD, branch and untracked files as they were.
func TestAgentAPIWorktreeIsReadOnly(t *testing.T) {
	ctx := context.Background()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	wt := agentAPIGitWorktree(t)
	pathWithoutGh(t)
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS1", Name: "Workspace One"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	const id = "agt_0123456789abcdef0123456789abcdef"
	g := opsimpl.NewGitOps().WithStore(st).WithAgentAPI(func(_ context.Context, ws, name string) (*ops.AgentWorktree, bool) {
		if ws != "WS1" || name != id {
			return nil, false
		}
		return &ops.AgentWorktree{Name: id, Path: wt, Branch: "loom/agent/" + id, DefaultBranch: "main", RepoName: "repo", AgentAPI: true}, true
	})
	agents := NewAgentService(g, nil, nil, st)
	files := NewFileService(g)
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
			if got := statusOf(r.call()); got != 403 {
				t.Fatalf("status = %d, want 403", got)
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
func agentAPIGitWorktree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	writeTestFile(t, filepath.Join(dir, "a.txt"), "a\n")
	git("add", "a.txt")
	git("commit", "-q", "-m", "base")
	git("checkout", "-q", "-b", "loom/agent/agt_0123456789abcdef0123456789abcdef")
	writeTestFile(t, filepath.Join(dir, "b.txt"), "b\n")
	git("add", "b.txt")
	git("commit", "-q", "-m", "work")
	writeTestFile(t, filepath.Join(dir, "scratch.txt"), "keep\n")
	return dir
}

// pathWithoutGh leaves git on PATH and drops gh, as in the local-mode
// container, so a gh check ahead of the guard shows up as a 503.
func pathWithoutGh(t *testing.T) {
	t.Helper()
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(gitBin, filepath.Join(bin, "git")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
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
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
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
