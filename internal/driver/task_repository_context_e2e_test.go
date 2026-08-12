package driver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/taskroot"
)

// TestTaskRepositoryContextEndToEnd is the deterministic Task 1 vertical
// proof. Narrower Fleet admission/writer-lease and Root Manager rollback tests
// exercise their failure matrices; this proves the successful physical
// implementation -> publication -> immutable review sequence over two repos.
func TestTaskRepositoryContextEndToEnd(t *testing.T) {
	ctx := t.Context()
	testRoot := t.TempDir()
	manager := taskroot.NewLocalGitManager(filepath.Join(testRoot, "workspace"))
	recorder := newHandoffRecorderFake()

	sources := map[string]string{}
	baseSHAs := map[string]string{}
	implementationSpecs := make([]taskroot.RepositorySpec, 0, 2)
	for _, name := range []string{"repo-a", "repo-b"} {
		source := filepath.Join(testRoot, "sources", name)
		remote := filepath.Join(testRoot, "remotes", name+".git")
		newGitWorktree(t, source)
		if err := os.MkdirAll(remote, 0o755); err != nil {
			t.Fatal(err)
		}
		gitCmd(t, remote, "init", "--bare", "-q")
		gitCmd(t, source, "remote", "add", "origin", remote)
		base := strings.TrimSpace(testGitOutput(t, source, "rev-parse", "HEAD"))
		sources[name] = source
		baseSHAs[name] = base
		implementationSpecs = append(implementationSpecs, taskroot.RepositorySpec{
			Name: name, SourcePath: source, BranchName: stableTaskBranch("TEST-1", name), BaseSHA: base,
		})
	}

	implementationManifest, err := manager.Prepare(ctx, taskroot.RootSpec{
		TaskRunID: "implementation-1", Generation: 1, FencingToken: 11, Repositories: implementationSpecs,
	})
	if err != nil {
		t.Fatalf("prepare implementation root: %v", err)
	}
	implementationRoot := taskRootFromManifest(implementationManifest)
	backendCommand := `for repo in repo-a repo-b; do
  printf 'changed by backend\n' > "$repo/README.md"
  git -C "$repo" -c user.name='Task Backend' -c user.email='backend@example.test' add README.md
  git -C "$repo" -c user.name='Task Backend' -c user.email='backend@example.test' commit -m "implement $repo" >/dev/null
done
printf '%s\n' '{"status":"completed","exit_code":0,"session_id":"implementation-session","runtime_metadata":{"backend":"controlled"}}'`
	implementationExecutor := HostBridgeTaskExecutor{
		Store: memstore.New(), WorktreePath: t.TempDir(), Command: []string{"sh", "-c", backendCommand},
		RootResolver: fixedTaskRootResolver{root: implementationRoot}, CompletionFinalizer: GitPushProxy{Recorder: recorder},
	}
	request := hostBridgeTaskExecRequest()
	request.TaskID = "TEST-1"
	request.TaskRunID = "implementation-1"
	request.ExecutionClass = domain.TaskRunExecutionImplementation
	request.RepositorySet = []string{"repo-a", "repo-b"}
	request.RunnerEntrypoint = LocalTaskRunnerEntrypoint
	request.RunnerTrustLevel = domain.DriverTrustTrusted
	implementationResult, err := implementationExecutor.ExecuteTask(ctx, request)
	if err != nil {
		t.Fatalf("execute implementation: %v", err)
	}
	if implementationResult.Status != domain.TaskRunCompleted || recorder.changeSet == nil || len(recorder.changeSet.Entries) != 2 {
		t.Fatalf("implementation result/change set = %+v / %+v", implementationResult, recorder.changeSet)
	}
	if implementationResult.RuntimeMetadata["backend_session_ref"] != "implementation-session" {
		t.Fatalf("implementation session = %q", implementationResult.RuntimeMetadata["backend_session_ref"])
	}
	for _, entry := range recorder.changeSet.Entries {
		if entry.BaseSHA != baseSHAs[entry.RepoName] || entry.HeadSHA == entry.BaseSHA || entry.PublicationStatus != domain.TaskChangePublicationConfirmed {
			t.Fatalf("invalid immutable entry: %+v", entry)
		}
		if got := strings.TrimSpace(testGitOutput(t, sources[entry.RepoName], "show", "-s", "--format=%ae", entry.HeadSHA)); got != "backend@example.test" {
			t.Fatalf("%s commit author = %q", entry.RepoName, got)
		}
		remoteHead := strings.TrimSpace(testGitOutput(t, sources[entry.RepoName], "ls-remote", "origin", "refs/heads/"+entry.BranchName))
		if !strings.HasPrefix(remoteHead, entry.HeadSHA+"\t") {
			t.Fatalf("%s remote head = %q, want %s", entry.RepoName, remoteHead, entry.HeadSHA)
		}
	}

	reviewSpecs := make([]taskroot.RepositorySpec, 0, 2)
	for _, entry := range recorder.changeSet.Entries {
		reviewSpecs = append(reviewSpecs, taskroot.RepositorySpec{Name: entry.RepoName, SourcePath: sources[entry.RepoName], BaseSHA: entry.HeadSHA, Detached: true})
	}
	reviewManifest, err := manager.Prepare(ctx, taskroot.RootSpec{TaskRunID: "review-1", Generation: 1, FencingToken: 12, Repositories: reviewSpecs})
	if err != nil {
		t.Fatalf("prepare review root: %v", err)
	}
	if reviewManifest.RootPath == implementationManifest.RootPath {
		t.Fatal("review reused implementation root")
	}
	reviewExecutor := HostBridgeTaskExecutor{
		Store: memstore.New(), WorktreePath: t.TempDir(),
		Command:      []string{"sh", "-c", `printf '%s\n' '{"status":"completed","exit_code":0,"session_id":"review-session","runtime_metadata":{"backend":"controlled","review_verdict":"pass","review.repository.repo-a.verdict":"pass","review.repository.repo-b.verdict":"pass"}}'`},
		RootResolver: fixedTaskRootResolver{root: taskRootFromManifest(reviewManifest)},
	}
	reviewRequest := request
	reviewRequest.TaskRunID = "review-1"
	reviewRequest.ExecutionClass = domain.TaskRunExecutionReview
	reviewRequest.ChangeSetVersion = 1
	reviewRequest.BackendSessionRef = ""
	reviewResult, err := reviewExecutor.ExecuteTask(ctx, reviewRequest)
	if err != nil {
		t.Fatalf("execute review: %v", err)
	}
	if reviewResult.Status != domain.TaskRunCompleted || reviewResult.RuntimeMetadata["review_verdict"] != "pass" {
		t.Fatalf("review result = %+v", reviewResult)
	}
	if reviewResult.RuntimeMetadata["backend_session_ref"] == implementationResult.RuntimeMetadata["backend_session_ref"] {
		t.Fatal("review reused implementation backend session")
	}

	if err := manager.Release(ctx, taskroot.RootLease{TaskRunID: "review-1", Generation: 1, FencingToken: 12}, taskroot.RetentionPolicy{}); err != nil {
		t.Fatalf("release review root: %v", err)
	}
	if err := manager.Release(ctx, taskroot.RootLease{TaskRunID: "implementation-1", Generation: 1, FencingToken: 11}, taskroot.RetentionPolicy{}); err != nil {
		t.Fatalf("release implementation root: %v", err)
	}
	if err := manager.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	inventory, err := manager.Inventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if inventory.Roots != 0 || inventory.Worktrees != 0 {
		t.Fatalf("inventory after cleanup = %+v", inventory)
	}
}

func taskRootFromManifest(manifest taskroot.RootManifest) TaskRoot {
	root := TaskRoot{Path: manifest.RootPath, ManifestPath: filepath.Join(manifest.RootPath, "manifest.json")}
	for _, repository := range manifest.Repositories {
		root.Repositories = append(root.Repositories, TaskRootRepository{
			Name: repository.Name, Path: repository.Path, BranchName: repository.BranchName, BaseSHA: repository.BaseSHA, Detached: repository.Detached,
		})
	}
	return root
}
