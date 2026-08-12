package driver

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestLocalTaskRootResolverProvisionsExactRepositorySet(t *testing.T) {
	ctx := context.Background()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	localRepos := map[string]string{}
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"repo-a", "repo-b"} {
		repoPath := filepath.Join(workspacePath, name)
		if err := os.MkdirAll(repoPath, 0o755); err != nil {
			t.Fatal(err)
		}
		gitCmd(t, repoPath, "init", "--initial-branch=main")
		gitCmd(t, repoPath, "config", "user.name", "Task Root Test")
		gitCmd(t, repoPath, "config", "user.email", "task-root@example.test")
		writeTestFile(t, filepath.Join(repoPath, "README.md"), name+"\n")
		gitCmd(t, repoPath, "add", "README.md")
		gitCmd(t, repoPath, "commit", "-m", "base")
		localRepos[name] = repoPath
		if _, err := st.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "TEST", Name: name, DefaultBranch: "main"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := bootstrap.MutateWorkspaceLocalState("TEST", func(local *bootstrap.WorkspaceLocalState) error {
		local.Path = workspacePath
		local.Repos = localRepos
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	root, err := (LocalTaskRootResolver{Store: st}).ResolveTaskRoot(ctx, TaskExecRequest{
		WorkspaceKey:   "TEST",
		TaskRunID:      "task-run-1",
		TaskID:         "TEST-1",
		RepositorySet:  []string{"repo-b", "repo-a"},
		RootGeneration: 1,
		FencingToken:   7,
	})
	if err != nil {
		t.Fatalf("ResolveTaskRoot: %v", err)
	}
	if root.Path != filepath.Join(workspacePath, ".loom", "task-roots", "task-run-1") {
		t.Fatalf("root path = %q", root.Path)
	}
	if len(root.Repositories) != 2 || root.Repositories[0].Name != "repo-a" || root.Repositories[1].Name != "repo-b" {
		t.Fatalf("repositories = %#v, want exact canonical repo-a/repo-b set", root.Repositories)
	}
	for _, repository := range root.Repositories {
		if _, err := os.Stat(filepath.Join(repository.Path, ".git")); err != nil {
			t.Fatalf("repository %s worktree missing: %v", repository.Name, err)
		}
	}
}

func TestLocalTaskRootResolverDoesNotFallbackFromUnknownRepository(t *testing.T) {
	ctx := context.Background()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	repoPath := filepath.Join(workspacePath, "repo-a")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, repoPath, "init", "--initial-branch=main")
	gitCmd(t, repoPath, "config", "user.name", "Task Root Test")
	gitCmd(t, repoPath, "config", "user.email", "task-root@example.test")
	writeTestFile(t, filepath.Join(repoPath, "README.md"), "repo-a\n")
	gitCmd(t, repoPath, "add", "README.md")
	gitCmd(t, repoPath, "commit", "-m", "base")
	if err := bootstrap.MutateWorkspaceLocalState("TEST", func(local *bootstrap.WorkspaceLocalState) error {
		local.Path = workspacePath
		local.Repos = map[string]string{"repo-a": repoPath}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "TEST", Name: "repo-a", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}

	_, err := (LocalTaskRootResolver{Store: st}).ResolveTaskRoot(ctx, TaskExecRequest{
		WorkspaceKey:   "TEST",
		TaskRunID:      "task-run-no-fallback",
		TaskID:         "TEST-1",
		RepositorySet:  []string{"missing"},
		RootGeneration: 1,
		FencingToken:   31,
		Input:          []byte(`{"repo":"repo-a"}`),
	})
	if err == nil {
		t.Fatal("ResolveTaskRoot succeeded via opaque-input or first-repository fallback")
	}
	if _, statErr := os.Stat(filepath.Join(workspacePath, ".loom", "task-roots", "task-run-no-fallback")); !os.IsNotExist(statErr) {
		t.Fatalf("fallback root was provisioned: %v", statErr)
	}
}
