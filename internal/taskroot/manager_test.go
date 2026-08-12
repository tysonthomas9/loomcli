package taskroot_test

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/taskroot"
)

func TestLocalGitManagerPreparePublishesAtomicTwoRepositoryManifest(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	workspace := t.TempDir()
	repoA, shaA := createRepository(t, workspace, "repo-a")
	repoB, shaB := createRepository(t, workspace, "repo-b")

	manager := taskroot.NewLocalGitManager(workspace)
	manifest, err := manager.Prepare(ctx, taskroot.RootSpec{
		TaskRunID:    "task-run-1",
		Generation:   1,
		FencingToken: 7,
		Repositories: []taskroot.RepositorySpec{
			{Name: "repo-b", SourcePath: repoB, BranchName: "loom/task-1/repo-b", BaseSHA: shaB},
			{Name: "repo-a", SourcePath: repoA, BranchName: "loom/task-1/repo-a", BaseSHA: shaA},
		},
	})
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}

	wantRoot := filepath.Join(workspace, ".loom", "task-roots", "task-run-1")
	if manifest.RootPath != wantRoot {
		t.Fatalf("RootPath = %q, want %q", manifest.RootPath, wantRoot)
	}
	if len(manifest.Repositories) != 2 || manifest.Repositories[0].Name != "repo-a" || manifest.Repositories[1].Name != "repo-b" {
		t.Fatalf("Repositories = %#v, want canonical repo-a/repo-b order", manifest.Repositories)
	}
	for _, repository := range manifest.Repositories {
		if got := gitOutput(t, repository.Path, "branch", "--show-current"); got != repository.BranchName {
			t.Fatalf("%s branch = %q, want %q", repository.Name, got, repository.BranchName)
		}
		if got := gitOutput(t, repository.Path, "rev-parse", "HEAD"); got != repository.BaseSHA {
			t.Fatalf("%s HEAD = %q, want %q", repository.Name, got, repository.BaseSHA)
		}
	}

	manifestBytes, err := os.ReadFile(filepath.Join(wantRoot, "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var persisted taskroot.RootManifest
	if err := json.Unmarshal(manifestBytes, &persisted); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if persisted.TaskRunID != "task-run-1" || persisted.Generation != 1 || persisted.FencingToken != 7 {
		t.Fatalf("persisted manifest identity = %#v", persisted)
	}

	inventory, err := manager.Inventory(ctx)
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	if inventory.Roots != 1 || inventory.Worktrees != 2 {
		t.Fatalf("Inventory = %#v, want one root and two worktrees", inventory)
	}
}

func TestLocalGitManagerPrepareReviewRootDetachedAtImmutableHead(t *testing.T) {
	ctx := t.Context()
	workspace := t.TempDir()
	repo, sha := createRepository(t, workspace, "repo-a")
	gitOutput(t, repo, "branch", "loom/task/TEST-1/repo-a")
	writerPath := filepath.Join(workspace, "writer")
	gitOutput(t, repo, "worktree", "add", writerPath, "loom/task/TEST-1/repo-a")

	manager := taskroot.NewLocalGitManager(workspace)
	manifest, err := manager.Prepare(ctx, taskroot.RootSpec{
		TaskRunID: "review-run-1", Generation: 1, FencingToken: 9,
		Repositories: []taskroot.RepositorySpec{{Name: "repo-a", SourcePath: repo, BaseSHA: sha, Detached: true}},
	})
	if err != nil {
		t.Fatalf("Prepare detached review root while writer branch is checked out: %v", err)
	}
	entry := manifest.Repositories[0]
	if !entry.Detached || gitOutput(t, entry.Path, "branch", "--show-current") != "" || gitOutput(t, entry.Path, "rev-parse", "HEAD") != sha {
		t.Fatalf("review entry = %+v branch=%q head=%q", entry, gitOutput(t, entry.Path, "branch", "--show-current"), gitOutput(t, entry.Path, "rev-parse", "HEAD"))
	}
}

func TestLocalGitManagerPrepareRollsBackEveryChildWhenSecondRepositoryFails(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	workspace := t.TempDir()
	repoA, shaA := createRepository(t, workspace, "repo-a")
	repoB, _ := createRepository(t, workspace, "repo-b")

	manager := taskroot.NewLocalGitManager(workspace)
	_, err := manager.Prepare(ctx, taskroot.RootSpec{
		TaskRunID:    "task-run-rollback",
		Generation:   1,
		FencingToken: 11,
		Repositories: []taskroot.RepositorySpec{
			{Name: "repo-a", SourcePath: repoA, BranchName: "loom/task-rollback/repo-a", BaseSHA: shaA},
			{Name: "repo-b", SourcePath: repoB, BranchName: "loom/task-rollback/repo-b", BaseSHA: "0000000000000000000000000000000000000000"},
		},
	})
	if err == nil {
		t.Fatal("Prepare succeeded, want second-repository failure")
	}

	root := filepath.Join(workspace, ".loom", "task-roots", "task-run-rollback")
	if _, statErr := os.Stat(root); !os.IsNotExist(statErr) {
		t.Fatalf("partial root still exists after rollback: %v", statErr)
	}
	worktrees := gitOutput(t, repoA, "worktree", "list", "--porcelain")
	if contains(worktrees, filepath.Join(root, "repo-a")) {
		t.Fatalf("repo-a worktree registration survived rollback:\n%s", worktrees)
	}
	inventory, inventoryErr := manager.Inventory(ctx)
	if inventoryErr != nil {
		t.Fatal(inventoryErr)
	}
	if inventory.Roots != 0 || inventory.Worktrees != 0 {
		t.Fatalf("Inventory after rollback = %#v, want empty", inventory)
	}
}

func TestLocalGitManagerPrepareReusesMatchingTaskRunRoot(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	workspace := t.TempDir()
	repoA, shaA := createRepository(t, workspace, "repo-a")
	spec := taskroot.RootSpec{
		TaskRunID:    "task-run-retry",
		Generation:   3,
		FencingToken: 19,
		Repositories: []taskroot.RepositorySpec{
			{Name: "repo-a", SourcePath: repoA, BranchName: "loom/task-retry/repo-a", BaseSHA: shaA},
		},
	}
	manager := taskroot.NewLocalGitManager(workspace)
	first, err := manager.Prepare(ctx, spec)
	if err != nil {
		t.Fatalf("first Prepare: %v", err)
	}
	second, err := manager.Prepare(ctx, spec)
	if err != nil {
		t.Fatalf("retry Prepare: %v", err)
	}
	if second.RootPath != first.RootPath || len(second.Repositories) != 1 || second.Repositories[0].Path != first.Repositories[0].Path {
		t.Fatalf("retry manifest = %#v, want reused %#v", second, first)
	}
	if got := gitOutput(t, repoA, "worktree", "list", "--porcelain"); count(got, first.Repositories[0].Path) != 1 {
		t.Fatalf("retry created duplicate worktree registration:\n%s", got)
	}
}

func TestLocalGitManagerPrepareRefencesMatchingTaskRunRootForRetry(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	workspace := t.TempDir()
	repo, sha := createRepository(t, workspace, "repo-a")
	manager := taskroot.NewLocalGitManager(workspace)
	firstSpec := taskroot.RootSpec{
		TaskRunID: "task-run-refence", Generation: 1, FencingToken: 41,
		Repositories: []taskroot.RepositorySpec{{Name: "repo-a", SourcePath: repo, BranchName: "loom/task-refence/repo-a", BaseSHA: sha}},
	}
	first, err := manager.Prepare(ctx, firstSpec)
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(first.Repositories[0].Path, "retry-marker.txt")
	if err := os.WriteFile(marker, []byte("preserve retry state\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	retrySpec := firstSpec
	retrySpec.FencingToken = 42
	retried, err := manager.Prepare(ctx, retrySpec)
	if err != nil {
		t.Fatalf("Prepare with newer retry fence: %v", err)
	}
	if retried.RootPath != first.RootPath || retried.FencingToken != 42 {
		t.Fatalf("retried manifest = %+v, want same root with fence 42", retried)
	}
	if got, err := os.ReadFile(marker); err != nil || string(got) != "preserve retry state\n" {
		t.Fatalf("retry marker = %q, %v; want preserved", got, err)
	}
	if err := manager.Release(ctx, taskroot.RootLease{TaskRunID: firstSpec.TaskRunID, Generation: 1, FencingToken: 41}, taskroot.RetentionPolicy{}); !errors.Is(err, taskroot.ErrStaleLease) {
		t.Fatalf("old owner Release error = %v, want ErrStaleLease", err)
	}
}

func TestLocalGitManagerPrepareRejectsOlderRetryFence(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	workspace := t.TempDir()
	repo, sha := createRepository(t, workspace, "repo-a")
	manager := taskroot.NewLocalGitManager(workspace)
	spec := taskroot.RootSpec{
		TaskRunID: "task-run-stale-retry", Generation: 1, FencingToken: 51,
		Repositories: []taskroot.RepositorySpec{{Name: "repo-a", SourcePath: repo, BranchName: "loom/task-stale-retry/repo-a", BaseSHA: sha}},
	}
	if _, err := manager.Prepare(ctx, spec); err != nil {
		t.Fatal(err)
	}
	spec.FencingToken = 50
	if _, err := manager.Prepare(ctx, spec); !errors.Is(err, taskroot.ErrStaleLease) {
		t.Fatalf("Prepare with older fence error = %v, want ErrStaleLease", err)
	}
}

func TestLocalGitManagerPersistsRetentionAndRetryReactivatesSameRoot(t *testing.T) {
	ctx := t.Context()
	workspace := t.TempDir()
	repo, sha := createRepository(t, workspace, "repo-a")
	spec := taskroot.RootSpec{TaskRunID: "task-run-retained", Generation: 1, FencingToken: 29,
		Repositories: []taskroot.RepositorySpec{{Name: "repo-a", SourcePath: repo, BranchName: "loom/task-retained/repo-a", BaseSHA: sha}}}
	manager := taskroot.NewLocalGitManager(workspace)
	manifest, err := manager.Prepare(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	retainUntil := time.Now().UTC().Add(time.Hour)
	if err := manager.Release(ctx, taskroot.RootLease{TaskRunID: spec.TaskRunID, Generation: 1, FencingToken: 29}, taskroot.RetentionPolicy{RetainUntil: retainUntil}); err != nil {
		t.Fatal(err)
	}
	retainedBytes, err := os.ReadFile(filepath.Join(manifest.RootPath, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var retained taskroot.RootManifest
	if err := json.Unmarshal(retainedBytes, &retained); err != nil {
		t.Fatal(err)
	}
	if retained.State != "retained" || retained.RetainUntil == nil || retained.RetainUntil.Before(retainUntil.Add(-time.Second)) {
		t.Fatalf("retained manifest = %+v", retained)
	}
	retried, err := manager.Prepare(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if retried.RootPath != manifest.RootPath || retried.State != "ready" || retried.RetainUntil != nil {
		t.Fatalf("retried manifest = %+v, want same ready root", retried)
	}
}

func TestLocalGitManagerReleaseIsFencedAndIdempotent(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	workspace := t.TempDir()
	repoA, shaA := createRepository(t, workspace, "repo-a")
	manager := taskroot.NewLocalGitManager(workspace)
	manifest, err := manager.Prepare(ctx, taskroot.RootSpec{
		TaskRunID:    "task-run-release",
		Generation:   2,
		FencingToken: 23,
		Repositories: []taskroot.RepositorySpec{
			{Name: "repo-a", SourcePath: repoA, BranchName: "loom/task-release/repo-a", BaseSHA: shaA},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	err = manager.Release(ctx, taskroot.RootLease{TaskRunID: "task-run-release", Generation: 1, FencingToken: 22}, taskroot.RetentionPolicy{})
	if !errors.Is(err, taskroot.ErrStaleLease) {
		t.Fatalf("stale Release error = %v, want ErrStaleLease", err)
	}
	if _, err := os.Stat(manifest.RootPath); err != nil {
		t.Fatalf("stale Release removed live root: %v", err)
	}

	lease := taskroot.RootLease{TaskRunID: "task-run-release", Generation: 2, FencingToken: 23}
	if err := manager.Release(ctx, lease, taskroot.RetentionPolicy{}); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if _, err := os.Stat(manifest.RootPath); !os.IsNotExist(err) {
		t.Fatalf("released root still exists: %v", err)
	}
	if got := gitOutput(t, repoA, "worktree", "list", "--porcelain"); contains(got, manifest.Repositories[0].Path) {
		t.Fatalf("worktree registration survived Release:\n%s", got)
	}
	if err := manager.Release(ctx, lease, taskroot.RetentionPolicy{}); err != nil {
		t.Fatalf("idempotent Release: %v", err)
	}
}

func TestLocalGitManagerReconcileRemovesUnpublishedRootAndPrunesRegistrations(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	workspace := t.TempDir()
	repoA, shaA := createRepository(t, workspace, "repo-a")
	manager := taskroot.NewLocalGitManager(workspace)
	manifest, err := manager.Prepare(ctx, taskroot.RootSpec{
		TaskRunID:    "task-run-orphan",
		Generation:   1,
		FencingToken: 29,
		Repositories: []taskroot.RepositorySpec{
			{Name: "repo-a", SourcePath: repoA, BranchName: "loom/task-orphan/repo-a", BaseSHA: shaA},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(manifest.RootPath, "manifest.json"), filepath.Join(manifest.RootPath, ".provisioning.json")); err != nil {
		t.Fatalf("simulate pre-publication crash: %v", err)
	}

	if err := manager.Reconcile(ctx); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if _, err := os.Stat(manifest.RootPath); !os.IsNotExist(err) {
		t.Fatalf("unpublished root survived reconciliation: %v", err)
	}
	if got := gitOutput(t, repoA, "worktree", "list", "--porcelain"); contains(got, manifest.Repositories[0].Path) {
		t.Fatalf("orphan registration survived reconciliation:\n%s", got)
	}
	if err := manager.Reconcile(ctx); err != nil {
		t.Fatalf("idempotent Reconcile: %v", err)
	}
}

func createRepository(t *testing.T, workspace, name string) (string, string) {
	t.Helper()
	remote := filepath.Join(workspace, "remotes", name+".git")
	run(t, workspace, "git", "init", "--bare", "--initial-branch=main", remote)
	repository := filepath.Join(workspace, "sources", name)
	run(t, workspace, "git", "clone", remote, repository)
	run(t, repository, "git", "config", "user.name", "Task Root Test")
	run(t, repository, "git", "config", "user.email", "task-root@example.test")
	if err := os.WriteFile(filepath.Join(repository, "README.md"), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, repository, "git", "add", "README.md")
	run(t, repository, "git", "commit", "-m", "initial")
	run(t, repository, "git", "push", "-u", "origin", "main")
	return repository, gitOutput(t, repository, "rev-parse", "HEAD")
}

func run(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, output)
	}
}

func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, output)
	}
	return string(bytesTrimSpace(output))
}

func bytesTrimSpace(value []byte) []byte {
	start, end := 0, len(value)
	for start < end && (value[start] == ' ' || value[start] == '\n' || value[start] == '\r' || value[start] == '\t') {
		start++
	}
	for end > start && (value[end-1] == ' ' || value[end-1] == '\n' || value[end-1] == '\r' || value[end-1] == '\t') {
		end--
	}
	return value[start:end]
}

func contains(value, substring string) bool {
	if substring == "" {
		return true
	}
	for index := 0; index+len(substring) <= len(value); index++ {
		if value[index:index+len(substring)] == substring {
			return true
		}
	}
	return false
}

func count(value, substring string) int {
	if substring == "" {
		return 0
	}
	total := 0
	for offset := 0; offset+len(substring) <= len(value); {
		if value[offset:offset+len(substring)] == substring {
			total++
			offset += len(substring)
			continue
		}
		offset++
	}
	return total
}
