package driver

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/localworkspace"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/agentcapture"
	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
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
	if got := strings.TrimSpace(testGitOutput(t, repoPath, "rev-parse", "refs/loom/ws/TEST/attempt/"+resolved.AttemptID+"/base")); got != head {
		t.Fatalf("attempt base ref = %s, want %s", got, head)
	}
	gitCmd(t, resolved.Path, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "--allow-empty", "-m", "attempt one")
	firstHead := strings.TrimSpace(testGitOutput(t, resolved.Path, "rev-parse", "HEAD"))
	gitCmd(t, repoPath, "-c", "user.name=Test", "-c", "user.email=test@example.test", "commit", "--allow-empty", "-m", "trunk advanced")
	retry, err := (LocalTaskWorktreeResolver{Store: st}).ResolveTaskWorktree(ctx, TaskExecRequest{
		WorkspaceKey: "TEST", TaskRunID: "task/run:1", TaskID: "TEST-1",
		SchedulerAttempt: 1, PreviousAttemptID: resolved.AttemptID,
	}, t.TempDir())
	if err != nil {
		t.Fatalf("retry ResolveTaskWorktree: %v", err)
	}
	if retry.Path == resolved.Path || retry.AttemptID == resolved.AttemptID || retry.BaseSHA != head {
		t.Fatalf("retry copy = %+v, want distinct copy at original base %s", retry, head)
	}
	if got := strings.TrimSpace(testGitOutput(t, retry.Path, "rev-parse", "HEAD")); got != head {
		t.Fatalf("retry HEAD = %s, want original base %s", got, head)
	}
	if got := strings.TrimSpace(testGitOutput(t, resolved.Path, "rev-parse", "HEAD")); got != firstHead {
		t.Fatalf("first attempt changed: %s -> %s", firstHead, got)
	}
	if got := strings.TrimSpace(testGitOutput(t, repoPath, "rev-parse", "refs/loom/ws/TEST/attempt/"+retry.AttemptID+"/base")); got != head {
		t.Fatalf("retry base ref = %s, want %s", got, head)
	}
	writeTestFile(t, filepath.Join(resolved.Path, "untracked.txt"), "crash work\n")
	captured, err := agentcapture.Capture(ctx, resolved.Path, "TEST", resolved.AttemptID, "TEST-1", "task")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := driverfreeze.FreezeCapture(ctx, driverfreeze.CaptureRequest{
		Workspace: "TEST", Task: "TEST-1", Repo: "app", Attempt: resolved.AttemptID,
		Worktree: resolved.Path, Base: head, CaptureSHA: captured.SHA, SourceRepo: repoPath,
		Outcome: "failed", Complete: captured.Complete,
	}); err != nil {
		t.Fatal(err)
	}
	resumed, err := (LocalTaskWorktreeResolver{Store: st}).ResolveTaskWorktree(ctx, TaskExecRequest{
		WorkspaceKey: "TEST", TaskRunID: "resume-run", TaskID: "TEST-1",
		ResumeAttemptID: resolved.AttemptID,
	}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if resumed.Path == resolved.Path || resumed.BaseSHA != head {
		t.Fatalf("resume copy = %+v, want fresh copy with original base %s", resumed, head)
	}
	if got := strings.TrimSpace(testGitOutput(t, resumed.Path, "show", "HEAD:untracked.txt")); got != "crash work" {
		t.Fatalf("resume lost captured work: %q", got)
	}
	if got := strings.TrimSpace(testGitOutput(t, resolved.Path, "rev-parse", "HEAD")); got != firstHead {
		t.Fatalf("resume changed crash-dirty copy: %s", got)
	}
	path, err := localworkspace.TaskCopyPath(workspacePath, "app", taskCopyAttemptID("task/run:1", 2))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = (LocalTaskWorktreeResolver{Store: st}).ResolveTaskWorktree(ctx, TaskExecRequest{
		WorkspaceKey: "TEST", TaskRunID: "task/run:1", TaskID: "TEST-1",
		SchedulerAttempt: 2, PreviousAttemptID: retry.AttemptID,
	}, t.TempDir())
	var coded *loomgit.Error
	if !errors.As(err, &coded) || coded.Code() != string(loomgit.TaskCopyCreateFailed) {
		t.Fatalf("existing task copy error = %v, want task_copy_create_failed", err)
	}
}

func TestEnsureRepoCheckout_MissingCheckoutDoesNotClone(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, source, "init")
	checkout := filepath.Join(root, "missing-checkout")
	local := bootstrap.WorkspaceLocalState{
		Path:  root,
		Repos: map[string]string{"app": checkout},
	}
	_, err := (LocalTaskWorktreeResolver{}).ensureRepoCheckout(context.Background(), "TEST", local,
		&domain.Repo{Name: "app", RemoteURL: source})
	if err == nil || !strings.Contains(err.Error(), "missing-checkout") {
		t.Fatalf("missing checkout error = %v", err)
	}
	if _, statErr := os.Stat(checkout); !os.IsNotExist(statErr) {
		t.Fatalf("missing checkout was created: %v", statErr)
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
