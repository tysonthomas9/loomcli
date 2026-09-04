package driver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type taskRootReviewStore struct {
	store.Store
	changeSet domain.TaskChangeSet
}

func (s taskRootReviewStore) PutTaskBranch(context.Context, domain.TaskBranch) (*domain.TaskBranch, error) {
	return nil, domain.ErrInvalid
}
func (s taskRootReviewStore) GetTaskBranch(context.Context, string, string, string) (*domain.TaskBranch, error) {
	return nil, domain.ErrNotFound
}
func (s taskRootReviewStore) CreateTaskChangeSet(context.Context, domain.TaskChangeSet) (*domain.TaskChangeSet, error) {
	return nil, domain.ErrInvalid
}
func (s taskRootReviewStore) GetTaskChangeSet(_ context.Context, _, _ string, version int) (*domain.TaskChangeSet, error) {
	if version != s.changeSet.Version {
		return nil, domain.ErrNotFound
	}
	copy := s.changeSet
	return &copy, nil
}

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

func TestLocalTaskRootResolverProvisionsIndependentReviewRootAtChangeSetHead(t *testing.T) {
	ctx := t.Context()
	t.Setenv("LOOM_CONFIG_DIR", t.TempDir())
	workspacePath := filepath.Join(t.TempDir(), "workspace")
	repoPath := filepath.Join(workspacePath, "repo-a")
	if err := os.MkdirAll(repoPath, 0o755); err != nil {
		t.Fatal(err)
	}
	newGitWorktree(t, repoPath)
	if err := bootstrap.MutateWorkspaceLocalState("TEST", func(local *bootstrap.WorkspaceLocalState) error {
		local.Path = workspacePath
		local.Repos = map[string]string{"repo-a": repoPath}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	baseStore := memstore.New()
	if _, err := baseStore.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := baseStore.Repos().Create(ctx, store.RepoCreate{WorkspaceKey: "TEST", Name: "repo-a", DefaultBranch: "main"}); err != nil {
		t.Fatal(err)
	}
	implementation, err := (LocalTaskRootResolver{Store: baseStore}).ResolveTaskRoot(ctx, TaskExecRequest{
		WorkspaceKey: "TEST", TaskRunID: "implementation-run", TaskID: "TEST-1",
		ExecutionClass: domain.TaskRunExecutionImplementation, RepositorySet: []string{"repo-a"}, RootGeneration: 1, FencingToken: 7,
	})
	if err != nil {
		t.Fatal(err)
	}
	writerRepo := implementation.Repositories[0]
	writeTestFile(t, filepath.Join(writerRepo.Path, "README.md"), "implemented\n")
	gitCmd(t, writerRepo.Path, "add", "README.md")
	gitCmd(t, writerRepo.Path, "commit", "-m", "implementation")
	head := strings.TrimSpace(testGitOutput(t, writerRepo.Path, "rev-parse", "HEAD"))
	reviewStore := taskRootReviewStore{Store: baseStore, changeSet: domain.TaskChangeSet{
		WorkspaceKey: "TEST", TaskID: "TEST-1", Version: 1,
		Entries: []domain.TaskChangeSetEntry{{RepoName: "repo-a", BaseSHA: writerRepo.BaseSHA, HeadSHA: head, BranchName: writerRepo.BranchName, RemoteName: "origin", PublicationStatus: domain.TaskChangePublicationConfirmed}},
	}}
	review, err := (LocalTaskRootResolver{Store: reviewStore}).ResolveTaskRoot(ctx, TaskExecRequest{
		WorkspaceKey: "TEST", TaskRunID: "review-run", TaskID: "TEST-1", ExecutionClass: domain.TaskRunExecutionReview,
		ChangeSetVersion: 1, RepositorySet: []string{"repo-a"}, RootGeneration: 1, FencingToken: 8,
	})
	if err != nil {
		t.Fatalf("Resolve review root: %v", err)
	}
	if review.Path == implementation.Path || review.Repositories[0].Path == writerRepo.Path || !review.Repositories[0].Detached {
		t.Fatalf("review root reused implementation state: implementation=%+v review=%+v", implementation, review)
	}
	if got := strings.TrimSpace(testGitOutput(t, review.Repositories[0].Path, "rev-parse", "HEAD")); got != head {
		t.Fatalf("review HEAD = %s, want %s", got, head)
	}
	if got := strings.TrimSpace(testGitOutput(t, review.Repositories[0].Path, "branch", "--show-current")); got != "" {
		t.Fatalf("review branch = %q, want detached", got)
	}
}
