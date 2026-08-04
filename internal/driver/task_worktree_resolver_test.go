package driver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestLocalTaskWorktreeResolverCreatesIsolatedTaskRunWorktree(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	ctx := context.Background()
	loomDir := t.TempDir()
	t.Setenv("LOOM_CONFIG_DIR", loomDir)

	workspacePath := filepath.Join(t.TempDir(), "workspace")
	repoPath := filepath.Join(workspacePath, "app")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}
	gitCmd(t, repoPath, "init")
	gitCmd(t, repoPath, "checkout", "-b", "main")
	gitCmd(t, repoPath, "config", "user.name", "Test User")
	gitCmd(t, repoPath, "config", "user.email", "test@example.test")
	writeTestFile(t, filepath.Join(repoPath, "src", "app.js"), "console.log('ok');\n")
	gitCmd(t, repoPath, "add", "src/app.js")
	gitCmd(t, repoPath, "commit", "-m", "base")
	head := strings.TrimSpace(testGitOutput(t, repoPath, "rev-parse", "HEAD"))

	if err := bootstrap.MutateWorkspaceLocalState("TEST", func(local *bootstrap.WorkspaceLocalState) error {
		local.Path = workspacePath
		local.Repos = map[string]string{"app": repoPath}
		return nil
	}); err != nil {
		t.Fatalf("write local state: %v", err)
	}

	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{
		WorkspaceKey:  "TEST",
		Name:          "app",
		DefaultBranch: "main",
		SourceRepoID:  "frontend",
	}); err != nil {
		t.Fatalf("create repo: %v", err)
	}

	resolved, err := (LocalTaskWorktreeResolver{Store: st}).ResolveTaskWorktree(ctx, TaskExecRequest{
		WorkspaceKey:     "TEST",
		TaskRunID:        "task/run:1",
		TaskID:           "TEST-1",
		SandboxPlacement: domain.TaskRunPlacement{RepoRef: "frontend"},
	}, t.TempDir())
	if err != nil {
		t.Fatalf("ResolveTaskWorktree: %v", err)
	}
	if resolved.Path == "" || resolved.Path == repoPath {
		t.Fatalf("resolved path = %q, want isolated task worktree distinct from repo %q", resolved.Path, repoPath)
	}
	if resolved.RepoName != "app" || resolved.SourceRepoID != "frontend" {
		t.Fatalf("resolved repo metadata = %+v, want app/frontend", resolved)
	}
	if _, err := os.Stat(filepath.Join(resolved.Path, ".git")); err != nil {
		t.Fatalf("resolved worktree .git missing: %v", err)
	}
	if got := strings.TrimSpace(testGitOutput(t, resolved.Path, "rev-parse", "HEAD")); got != head {
		t.Fatalf("resolved HEAD = %s, want %s", got, head)
	}
	if _, err := os.Stat(filepath.Join(resolved.Path, "src", "app.js")); err != nil {
		t.Fatalf("resolved worktree missing source file: %v", err)
	}
}

func gitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	_ = testGitOutput(t, dir, args...)
}

func testGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) //nolint:gosec // fixed test command. //nolint:norawexec
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out)
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
