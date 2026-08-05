package localgit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/connectors"
)

func TestExecutorClonesThroughHardenedLocalMaterializer(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	workspace := filepath.Join(root, "workspace")
	target := filepath.Join(workspace, "repo")
	executorRunGit(t, "", "init", "--bare", remote)
	executorRunGit(t, "", "init", "-b", "main", seed)
	executorRunGit(t, seed, "config", "user.name", "Test User")
	executorRunGit(t, seed, "config", "user.email", "test@example.test")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("bounded clone\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	executorRunGit(t, seed, "add", "README.md")
	executorRunGit(t, seed, "commit", "-m", "seed")
	executorRunGit(t, seed, "remote", "add", "origin", remote)
	executorRunGit(t, seed, "push", "origin", "main")
	executorRunGit(t, remote, "symbolic-ref", "HEAD", "refs/heads/main")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}

	executor := New(nil)
	err := executor.ExecuteGitRead(t.Context(), connectors.GitReadCommand{
		WorkspaceKey: "WS-1", OperationID: "materialize-1", RepositoryRef: "repo-1",
		Operation: connectors.GitReadClone, RemoteURL: remote, RemoteName: "upstream",
		WorkspacePath: workspace, TargetPath: target,
	})
	if err != nil {
		t.Fatalf("ExecuteGitRead: %v", err)
	}
	if got := strings.TrimSpace(executorGitOutput(t, target, "show", "HEAD:README.md")); got != "bounded clone" {
		t.Fatalf("cloned README = %q", got)
	}
	if got := strings.TrimSpace(executorGitOutput(t, target, "remote", "get-url", "upstream")); got != remote {
		t.Fatalf("stored remote = %q, want %q", got, remote)
	}
}

func TestExecutorFetchesOnlyExactSourceControlRef(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	seed := filepath.Join(root, "seed")
	workspace := filepath.Join(root, "workspace")
	target := filepath.Join(workspace, "repo")
	executorRunGit(t, "", "init", "--bare", remote)
	executorRunGit(t, "", "init", "-b", "main", seed)
	executorRunGit(t, seed, "config", "user.name", "Test User")
	executorRunGit(t, seed, "config", "user.email", "test@example.test")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	executorRunGit(t, seed, "add", "README.md")
	executorRunGit(t, seed, "commit", "-m", "base")
	executorRunGit(t, seed, "remote", "add", "origin", remote)
	executorRunGit(t, seed, "push", "origin", "main")
	executorRunGit(t, remote, "symbolic-ref", "HEAD", "refs/heads/main")
	executorRunGit(t, "", "clone", remote, target)

	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	executorRunGit(t, seed, "add", "README.md")
	executorRunGit(t, seed, "commit", "-m", "new")
	executorRunGit(t, seed, "push", "origin", "main")
	want := strings.TrimSpace(executorGitOutput(t, seed, "rev-parse", "HEAD"))

	executor := New(nil)
	command := connectors.GitReadCommand{
		WorkspaceKey: "WS-1", OperationID: "fetch-1", RepositoryRef: "repo-1",
		Operation: connectors.GitReadFetchRef, RemoteURL: remote,
		WorkspacePath: workspace, TargetPath: target, RemoteName: "origin",
		SourceRef: "refs/heads/main", DestinationRef: "refs/loom/tasks/run-1/base",
	}
	if err := executor.ExecuteGitRead(t.Context(), command); err != nil {
		t.Fatalf("ExecuteGitRead fetch: %v", err)
	}
	if got := strings.TrimSpace(executorGitOutput(
		t,
		target,
		"rev-parse",
		command.DestinationRef+"^{commit}",
	)); got != want {
		t.Fatalf("fetched commit = %q, want %q", got, want)
	}

	changedRemote := command
	changedRemote.OperationID = "fetch-2"
	changedRemote.RemoteURL = filepath.Join(root, "other.git")
	if err := executor.ExecuteGitRead(t.Context(), changedRemote); err == nil ||
		!strings.Contains(err.Error(), "does not match") {
		t.Fatalf("changed remote error = %v", err)
	}
}

func TestExecutorFetchesFromAdmittedLocalSourceWithoutMutatingRemoteConfig(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	root := t.TempDir()
	source := filepath.Join(root, "source")
	workspace := filepath.Join(root, "workspace")
	target := filepath.Join(workspace, "repo")
	executorRunGit(t, "", "init", "-b", "main", source)
	executorRunGit(t, source, "config", "user.name", "Test User")
	executorRunGit(t, source, "config", "user.email", "test@example.test")
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("local source\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	executorRunGit(t, source, "add", "README.md")
	executorRunGit(t, source, "commit", "-m", "seed")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	executorRunGit(t, source, "worktree", "add", "--detach", target, "HEAD")
	want := strings.TrimSpace(executorGitOutput(t, source, "rev-parse", "main"))

	command := connectors.GitReadCommand{
		WorkspaceKey: "WS-1", OperationID: "fetch-local-1", RepositoryRef: "repo-1",
		Operation: connectors.GitReadFetchRef, RemoteURL: source,
		WorkspacePath: workspace, TargetPath: target, RemoteName: "origin",
		SourceRef: "refs/heads/main", DestinationRef: "refs/loom/tasks/run-local/base",
	}
	if err := New(nil).ExecuteGitRead(t.Context(), command); err != nil {
		t.Fatalf("ExecuteGitRead local fetch: %v", err)
	}
	if got := strings.TrimSpace(executorGitOutput(t, target, "rev-parse", command.DestinationRef)); got != want {
		t.Fatalf("fetched commit = %q, want %q", got, want)
	}
	if output := strings.TrimSpace(executorGitOutput(t, target, "remote")); output != "" {
		t.Fatalf("local fetch persisted remote configuration %q", output)
	}
}

func TestExecutorRejectsUnboundedOrMissingOperation(t *testing.T) {
	if err := (*Executor)(nil).ExecuteGitRead(t.Context(), connectors.GitReadCommand{}); err == nil {
		t.Fatal("nil executor succeeded")
	}
	if err := New(nil).ExecuteGitRead(t.Context(), connectors.GitReadCommand{Operation: "push"}); err == nil {
		t.Fatal("mutating Git operation succeeded")
	}
}

func TestExecutorRejectsSymlinkedWorkspaceParentAndTarget(t *testing.T) {
	root := t.TempDir()
	realWorkspace := filepath.Join(root, "workspace")
	if err := os.Mkdir(realWorkspace, 0o700); err != nil {
		t.Fatal(err)
	}
	executor := New(nil)
	command := connectors.GitReadCommand{
		WorkspaceKey: "WS-1", OperationID: "materialize-1", RepositoryRef: "repo-1",
		Operation: connectors.GitReadClone, RemoteURL: "/srv/repo.git",
		WorkspacePath: realWorkspace, TargetPath: filepath.Join(realWorkspace, "repo"),
	}
	if _, err := executor.ValidateGitRead(t.Context(), command); err != nil {
		t.Fatalf("valid target: %v", err)
	}

	workspaceLink := filepath.Join(root, "workspace-link")
	if err := os.Symlink(realWorkspace, workspaceLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	symlinkedWorkspace := command
	symlinkedWorkspace.WorkspacePath = workspaceLink
	symlinkedWorkspace.TargetPath = filepath.Join(workspaceLink, "repo")
	if _, err := executor.ValidateGitRead(t.Context(), symlinkedWorkspace); err == nil {
		t.Fatal("symlinked workspace passed containment validation")
	}

	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	parentLink := filepath.Join(realWorkspace, "parent-link")
	if err := os.Symlink(outside, parentLink); err != nil {
		t.Fatal(err)
	}
	symlinkedParent := command
	symlinkedParent.TargetPath = filepath.Join(parentLink, "repo")
	if _, err := executor.ValidateGitRead(t.Context(), symlinkedParent); err == nil {
		t.Fatal("symlinked target parent passed containment validation")
	}

	outsideTarget := filepath.Join(outside, "repo")
	if err := os.Mkdir(outsideTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	targetLink := filepath.Join(realWorkspace, "target-link")
	if err := os.Symlink(outsideTarget, targetLink); err != nil {
		t.Fatal(err)
	}
	symlinkedTarget := command
	symlinkedTarget.TargetPath = targetLink
	if _, err := executor.ValidateGitRead(t.Context(), symlinkedTarget); err == nil {
		t.Fatal("symlinked target passed containment validation")
	}
}

func executorRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...) //nolint:norawexec,gosec // test helper.
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func executorGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...) //nolint:norawexec,gosec // test helper.
	command.Dir = dir
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return string(output)
}
