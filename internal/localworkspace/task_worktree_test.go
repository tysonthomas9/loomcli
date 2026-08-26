package localworkspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareTaskWorktreeReusesOneTaskOwnedCheckoutAcrossAgents(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	git(t, "", "init", "-b", "main", repo)
	git(t, repo, "config", "user.name", "Test User")
	git(t, repo, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(repo, "base.txt"), "base\n")
	git(t, repo, "add", "base.txt")
	git(t, repo, "commit", "-m", "base")

	req := TaskWorktreeRequest{
		WorkspacePath: root,
		WorkspaceKey:  "TEAM",
		RepoName:      "repo",
		RepoPath:      repo,
		TaskID:        "TEAM-42",
		DefaultBranch: "main",
	}
	backend, err := PrepareTaskWorktree(context.Background(), req)
	if err != nil {
		t.Fatalf("prepare backend task worktree: %v", err)
	}
	writeFile(t, filepath.Join(backend.Path, "implemented.txt"), "backend output\n")
	git(t, backend.Path, "add", "implemented.txt")
	git(t, backend.Path, "commit", "-m", "implement task")
	backendHead := gitOut(t, backend.Path, "rev-parse", "HEAD")
	if _, err := (TaskWorktreeManager{}).Publish(context.Background(), TaskWorktreePublishRequest{
		WorkspaceKey: req.WorkspaceKey, RepoPath: repo, TaskID: req.TaskID,
		Path: backend.Path, Branch: backend.Branch, InputSHA: backend.InputSHA,
	}); err != nil {
		t.Fatalf("publish backend delivery: %v", err)
	}

	qa, err := PrepareTaskWorktree(context.Background(), req)
	if err != nil {
		t.Fatalf("prepare QA task worktree: %v", err)
	}
	if qa.Path != backend.Path {
		t.Fatalf("QA path = %q, want backend task path %q", qa.Path, backend.Path)
	}
	if qa.Branch != backend.Branch {
		t.Fatalf("QA branch = %q, want backend task branch %q", qa.Branch, backend.Branch)
	}
	if got := gitOut(t, qa.Path, "rev-parse", "HEAD"); got != backendHead {
		t.Fatalf("QA HEAD = %s, want backend delivery %s", got, backendHead)
	}
	if got, err := os.ReadFile(filepath.Join(qa.Path, "implemented.txt")); err != nil || string(got) != "backend output\n" {
		t.Fatalf("QA implementation = %q, %v", got, err)
	}
}

func TestPrepareTaskWorktreeKeepsDifferentTasksIsolated(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	git(t, "", "init", "-b", "main", repo)
	git(t, repo, "config", "user.name", "Test User")
	git(t, repo, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(repo, "base.txt"), "base\n")
	git(t, repo, "add", "base.txt")
	git(t, repo, "commit", "-m", "base")

	base := TaskWorktreeRequest{WorkspacePath: root, WorkspaceKey: "TEAM", RepoName: "repo", RepoPath: repo, DefaultBranch: "main"}
	base.TaskID = "TEAM-1"
	first, err := PrepareTaskWorktree(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	base.TaskID = "TEAM-2"
	second, err := PrepareTaskWorktree(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	if first.Path == second.Path || first.Branch == second.Branch {
		t.Fatalf("different tasks share Git state: first=%+v second=%+v", first, second)
	}
}

func TestTaskWorktreeManagerFencesConcurrentAttemptsUntilLeaseRelease(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	git(t, "", "init", "-b", "main", repo)
	git(t, repo, "config", "user.name", "Test User")
	git(t, repo, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(repo, "base.txt"), "base\n")
	git(t, repo, "add", "base.txt")
	git(t, repo, "commit", "-m", "base")

	req := TaskWorktreeRequest{
		WorkspacePath: root,
		WorkspaceKey:  "TEAM",
		RepoName:      "repo",
		RepoPath:      repo,
		TaskID:        "TEAM-LOCKED",
		DefaultBranch: "main",
	}
	manager := TaskWorktreeManager{}
	first, err := manager.Prepare(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Lease == nil {
		t.Fatal("prepared task worktree has no ownership lease")
	}
	if _, err := manager.Prepare(context.Background(), req); err == nil {
		t.Fatal("concurrent attempt acquired an already-owned task worktree")
	}
	if err := first.Lease.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := manager.Prepare(context.Background(), req)
	if err != nil {
		t.Fatalf("prepare after lease release: %v", err)
	}
	if err := second.Lease.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestPrepareTaskWorktreeBasesDependentTaskOnPublishedTaskBranch(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	git(t, "", "init", "-b", "main", repo)
	git(t, repo, "config", "user.name", "Test User")
	git(t, repo, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(repo, "base.txt"), "base\n")
	git(t, repo, "add", "base.txt")
	git(t, repo, "commit", "-m", "base")

	base := TaskWorktreeRequest{WorkspacePath: root, WorkspaceKey: "TEAM", RepoName: "repo", RepoPath: repo, DefaultBranch: "main"}
	base.TaskID = "TEAM-A"
	upstream, err := PrepareTaskWorktree(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(upstream.Path, "from-a.txt"), "task A\n")
	git(t, upstream.Path, "add", "from-a.txt")
	git(t, upstream.Path, "commit", "-m", "task A delivery")
	upstreamHead := gitOut(t, upstream.Path, "rev-parse", "HEAD")
	if _, err := (TaskWorktreeManager{}).Publish(context.Background(), TaskWorktreePublishRequest{
		WorkspaceKey: base.WorkspaceKey, RepoPath: repo, TaskID: base.TaskID,
		Path: upstream.Path, Branch: upstream.Branch, InputSHA: upstream.InputSHA,
	}); err != nil {
		t.Fatal(err)
	}

	base.TaskID = "TEAM-B"
	base.DependencyTaskIDs = []string{"TEAM-A"}
	dependent, err := PrepareTaskWorktree(context.Background(), base)
	if err != nil {
		t.Fatalf("prepare dependent task: %v", err)
	}
	if got := gitOut(t, dependent.Path, "rev-parse", "HEAD"); got != upstreamHead {
		t.Fatalf("task B HEAD = %s, want task A delivery %s", got, upstreamHead)
	}
	if got, err := os.ReadFile(filepath.Join(dependent.Path, "from-a.txt")); err != nil || string(got) != "task A\n" {
		t.Fatalf("task B inherited file = %q, %v", got, err)
	}
}

func TestPrepareTaskWorktreeInheritsAvailableCandidateDeliveryAndIgnoresGateOnlyDependency(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	git(t, "", "init", "-b", "main", repo)
	git(t, repo, "config", "user.name", "Test User")
	git(t, repo, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(repo, "base.txt"), "base\n")
	git(t, repo, "add", "base.txt")
	git(t, repo, "commit", "-m", "base")

	base := TaskWorktreeRequest{WorkspacePath: root, WorkspaceKey: "TEAM", RepoName: "repo", RepoPath: repo, DefaultBranch: "main"}
	base.TaskID = "TEAM-A"
	upstream, err := PrepareTaskWorktree(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(upstream.Path, "from-a.txt"), "task A\n")
	git(t, upstream.Path, "add", "from-a.txt")
	git(t, upstream.Path, "commit", "-m", "task A delivery")
	if _, err := (TaskWorktreeManager{}).Publish(context.Background(), TaskWorktreePublishRequest{
		WorkspaceKey: base.WorkspaceKey, RepoPath: repo, TaskID: base.TaskID,
		Path: upstream.Path, Branch: upstream.Branch, InputSHA: upstream.InputSHA,
	}); err != nil {
		t.Fatal(err)
	}

	base.TaskID = "TEAM-B"
	base.CandidateDependencyTaskIDs = []string{"TEAM-RESEARCH", "TEAM-A"}
	dependent, err := PrepareTaskWorktree(context.Background(), base)
	if err != nil {
		t.Fatalf("prepare dependent task: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dependent.Path, "from-a.txt")); err != nil {
		t.Fatalf("task B did not inherit available code delivery: %v", err)
	}
}

func TestSnapshotTaskWorktreePublishesExactCleanRevision(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	git(t, "", "init", "-b", "main", repo)
	git(t, repo, "config", "user.name", "Test User")
	git(t, repo, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(repo, "delivery.txt"), "published\n")
	git(t, repo, "add", "delivery.txt")
	git(t, repo, "commit", "-m", "delivery")

	got, err := SnapshotTaskWorktree(context.Background(), repo, "main", "")
	if err != nil {
		t.Fatal(err)
	}
	if got.HeadSHA != gitOut(t, repo, "rev-parse", "HEAD") {
		t.Fatalf("HeadSHA = %q", got.HeadSHA)
	}
	if got.TreeSHA != gitOut(t, repo, "rev-parse", "HEAD^{tree}") {
		t.Fatalf("TreeSHA = %q", got.TreeSHA)
	}
}

func TestSnapshotTaskWorktreeRejectsUncommittedDelivery(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	git(t, "", "init", "-b", "main", repo)
	git(t, repo, "config", "user.name", "Test User")
	git(t, repo, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(repo, "base.txt"), "base\n")
	git(t, repo, "add", "base.txt")
	git(t, repo, "commit", "-m", "base")
	writeFile(t, filepath.Join(repo, "dirty.txt"), "not committed\n")

	if _, err := SnapshotTaskWorktree(context.Background(), repo, "main", ""); err == nil {
		t.Fatal("SnapshotTaskWorktree accepted an uncommitted delivery")
	}
}

func TestSnapshotTaskWorktreeIgnoresOnlyHarnessCoordinationFiles(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	git(t, "", "init", "-b", "main", repo)
	git(t, repo, "config", "user.name", "Test User")
	git(t, repo, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(repo, "base.txt"), "base\n")
	git(t, repo, "add", "base.txt")
	git(t, repo, "commit", "-m", "base")
	writeFile(t, filepath.Join(repo, ".agent.lock"), "runtime\n")
	writeFile(t, filepath.Join(repo, ".agent.lock.flock"), "")
	writeFile(t, filepath.Join(repo, ".agent.checkpoint.json"), "{}\n")

	if _, err := SnapshotTaskWorktree(context.Background(), repo, "main", ""); err != nil {
		t.Fatalf("harness coordination files blocked clean delivery: %v", err)
	}
	writeFile(t, filepath.Join(repo, ".other-runtime-file"), "must remain visible\n")
	if _, err := SnapshotTaskWorktree(context.Background(), repo, "main", ""); err == nil {
		t.Fatal("cleanliness check ignored an unrelated untracked file")
	}
}

func TestSnapshotTaskWorktreeRejectsOutputThatDropsPreparedInput(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	git(t, "", "init", "-b", "main", repo)
	git(t, repo, "config", "user.name", "Test User")
	git(t, repo, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(repo, "base.txt"), "base\n")
	git(t, repo, "add", "base.txt")
	git(t, repo, "commit", "-m", "base")
	baseSHA := gitOut(t, repo, "rev-parse", "HEAD")
	writeFile(t, filepath.Join(repo, "input.txt"), "input\n")
	git(t, repo, "add", "input.txt")
	git(t, repo, "commit", "-m", "prepared input")
	inputSHA := gitOut(t, repo, "rev-parse", "HEAD")
	git(t, repo, "reset", "--hard", baseSHA)
	if _, err := SnapshotTaskWorktree(context.Background(), repo, "main", inputSHA); err == nil {
		t.Fatal("publication accepted output that dropped its prepared input")
	}
}

func TestPrepareTaskWorktreeRejectsDirtyCheckoutForNextStage(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	git(t, "", "init", "-b", "main", repo)
	git(t, repo, "config", "user.name", "Test User")
	git(t, repo, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(repo, "base.txt"), "base\n")
	git(t, repo, "add", "base.txt")
	git(t, repo, "commit", "-m", "base")
	req := TaskWorktreeRequest{WorkspacePath: root, WorkspaceKey: "TEAM", RepoName: "repo", RepoPath: repo, TaskID: "TEAM-7", DefaultBranch: "main"}
	prepared, err := PrepareTaskWorktree(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(prepared.Path, "dirty.txt"), "uncommitted\n")
	if _, err := PrepareTaskWorktree(context.Background(), req); err == nil {
		t.Fatal("next stage accepted dirty task checkout")
	}

	req.AllowDirtyResume = true
	if _, err := PrepareTaskWorktree(context.Background(), req); err != nil {
		t.Fatalf("same-attempt resume should preserve dirty checkout: %v", err)
	}
}

func TestPrepareTaskWorktreeRejectsUnpublishedRequiredDependency(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	git(t, "", "init", "-b", "main", repo)
	git(t, repo, "config", "user.name", "Test User")
	git(t, repo, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(repo, "base.txt"), "base\n")
	git(t, repo, "add", "base.txt")
	git(t, repo, "commit", "-m", "base")
	base := TaskWorktreeRequest{WorkspacePath: root, WorkspaceKey: "TEAM", RepoName: "repo", RepoPath: repo, DefaultBranch: "main"}
	base.TaskID = "TEAM-A"
	upstream, err := PrepareTaskWorktree(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(upstream.Path, "partial.txt"), "not published\n")
	git(t, upstream.Path, "add", "partial.txt")
	git(t, upstream.Path, "commit", "-m", "partial")

	base.TaskID = "TEAM-B"
	base.DependencyTaskIDs = []string{"TEAM-A"}
	if _, err := PrepareTaskWorktree(context.Background(), base); err == nil {
		t.Fatal("required dependency accepted a task branch that was never published")
	}
}

func TestPrepareTaskWorktreeRejectsChangedPublishedDependencyReceipt(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	git(t, "", "init", "-b", "main", repo)
	git(t, repo, "config", "user.name", "Test User")
	git(t, repo, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(repo, "base.txt"), "base\n")
	git(t, repo, "add", "base.txt")
	git(t, repo, "commit", "-m", "base")
	mainSHA := gitOut(t, repo, "rev-parse", "HEAD")
	base := TaskWorktreeRequest{WorkspacePath: root, WorkspaceKey: "TEAM", RepoName: "repo", RepoPath: repo, DefaultBranch: "main"}
	base.TaskID = "TEAM-A"
	upstream, err := PrepareTaskWorktree(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(upstream.Path, "a.txt"), "a1\n")
	git(t, upstream.Path, "add", "a.txt")
	git(t, upstream.Path, "commit", "-m", "A1")
	publish := TaskWorktreePublishRequest{WorkspaceKey: base.WorkspaceKey, RepoPath: repo, TaskID: base.TaskID, Path: upstream.Path, Branch: upstream.Branch, InputSHA: upstream.InputSHA}
	if _, err := (TaskWorktreeManager{}).Publish(context.Background(), publish); err != nil {
		t.Fatal(err)
	}

	base.TaskID = "TEAM-B"
	base.DependencyTaskIDs = []string{"TEAM-A"}
	if _, err := PrepareTaskWorktree(context.Background(), base); err != nil {
		t.Fatal(err)
	}

	git(t, upstream.Path, "reset", "--hard", mainSHA)
	publish.InputSHA = mainSHA
	if _, err := (TaskWorktreeManager{}).Publish(context.Background(), publish); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareTaskWorktree(context.Background(), base); err == nil {
		t.Fatal("task B accepted a changed dependency revision despite its exact input receipt")
	}
}

func TestPublishRejectsDependencyThatAdvancedDuringAttempt(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	git(t, "", "init", "-b", "main", repo)
	git(t, repo, "config", "user.name", "Test User")
	git(t, repo, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(repo, "base.txt"), "base\n")
	git(t, repo, "add", "base.txt")
	git(t, repo, "commit", "-m", "base")
	manager := TaskWorktreeManager{}
	base := TaskWorktreeRequest{WorkspacePath: root, WorkspaceKey: "TEAM", RepoName: "repo", RepoPath: repo, DefaultBranch: "main"}

	base.TaskID = "TEAM-A"
	a, err := PrepareTaskWorktree(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(a.Path, "a.txt"), "a1\n")
	git(t, a.Path, "add", "a.txt")
	git(t, a.Path, "commit", "-m", "A1")
	aPublish := TaskWorktreePublishRequest{WorkspaceKey: base.WorkspaceKey, RepoPath: repo, TaskID: base.TaskID, Path: a.Path, Branch: a.Branch, InputSHA: a.InputSHA}
	if _, err := manager.Publish(context.Background(), aPublish); err != nil {
		t.Fatal(err)
	}

	base.TaskID = "TEAM-B"
	base.DependencyTaskIDs = []string{"TEAM-A"}
	b, err := PrepareTaskWorktree(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(b.Path, "b.txt"), "built on a1\n")
	git(t, b.Path, "add", "b.txt")
	git(t, b.Path, "commit", "-m", "B")

	writeFile(t, filepath.Join(a.Path, "a.txt"), "a2\n")
	git(t, a.Path, "add", "a.txt")
	git(t, a.Path, "commit", "-m", "A2")
	if _, err := manager.Publish(context.Background(), aPublish); err != nil {
		t.Fatal(err)
	}
	bPublish := TaskWorktreePublishRequest{WorkspaceKey: base.WorkspaceKey, RepoPath: repo, TaskID: base.TaskID, Path: b.Path, Branch: b.Branch, InputSHA: b.InputSHA}
	if _, err := manager.Publish(context.Background(), bPublish); err == nil {
		t.Fatal("published task B after its dependency advanced during the attempt")
	}
	if _, err := resolveTaskDelivery(context.Background(), repo, base.WorkspaceKey, "TEAM-B"); err == nil {
		t.Fatal("failed stale publication still activated task B delivery ref")
	}
}

func TestPrepareRejectsRemovedDependencyReceipt(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	git(t, "", "init", "-b", "main", repo)
	git(t, repo, "config", "user.name", "Test User")
	git(t, repo, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(repo, "base.txt"), "base\n")
	git(t, repo, "add", "base.txt")
	git(t, repo, "commit", "-m", "base")
	manager := TaskWorktreeManager{}
	base := TaskWorktreeRequest{WorkspacePath: root, WorkspaceKey: "TEAM", RepoName: "repo", RepoPath: repo, DefaultBranch: "main"}
	base.TaskID = "TEAM-A"
	a, err := PrepareTaskWorktree(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(a.Path, "a.txt"), "a\n")
	git(t, a.Path, "add", "a.txt")
	git(t, a.Path, "commit", "-m", "A")
	if _, err := manager.Publish(context.Background(), TaskWorktreePublishRequest{WorkspaceKey: base.WorkspaceKey, RepoPath: repo, TaskID: base.TaskID, Path: a.Path, Branch: a.Branch, InputSHA: a.InputSHA}); err != nil {
		t.Fatal(err)
	}
	base.TaskID = "TEAM-B"
	base.DependencyTaskIDs = []string{"TEAM-A"}
	if _, err := PrepareTaskWorktree(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	base.DependencyTaskIDs = nil
	if _, err := PrepareTaskWorktree(context.Background(), base); err == nil {
		t.Fatal("accepted an existing task after removing its recorded dependency")
	}
}

func TestPrepareRejectsCleanCommittedTaskBranchWithoutPublishedDelivery(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	git(t, "", "init", "-b", "main", repo)
	git(t, repo, "config", "user.name", "Test User")
	git(t, repo, "config", "user.email", "test@example.test")
	writeFile(t, filepath.Join(repo, "base.txt"), "base\n")
	git(t, repo, "add", "base.txt")
	git(t, repo, "commit", "-m", "base")
	req := TaskWorktreeRequest{WorkspacePath: root, WorkspaceKey: "TEAM", RepoName: "repo", RepoPath: repo, TaskID: "TEAM-WIP", DefaultBranch: "main"}
	first, err := PrepareTaskWorktree(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(first.Path, "wip.txt"), "clean but uncertified\n")
	git(t, first.Path, "add", "wip.txt")
	git(t, first.Path, "commit", "-m", "uncertified WIP")
	if _, err := PrepareTaskWorktree(context.Background(), req); err == nil {
		t.Fatal("new stage accepted committed task state without a published delivery")
	}
}
