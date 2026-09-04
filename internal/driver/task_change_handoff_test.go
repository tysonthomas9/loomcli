package driver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

type handoffRecorderFake struct {
	branches  map[string]domain.TaskBranch
	puts      map[string]int
	changeSet *domain.TaskChangeSet
}

func newHandoffRecorderFake() *handoffRecorderFake {
	return &handoffRecorderFake{branches: map[string]domain.TaskBranch{}, puts: map[string]int{}}
}

func (f *handoffRecorderFake) PutTaskBranch(_ context.Context, branch domain.TaskBranch) (*domain.TaskBranch, error) {
	key := branch.TaskID + "/" + branch.RepoName
	if existing, ok := f.branches[key]; ok && (existing.BranchName != branch.BranchName || existing.AdmittedBaseSHA != branch.AdmittedBaseSHA) {
		return nil, domain.ErrInvalidTransition
	}
	f.branches[key] = branch
	f.puts[key]++
	copy := branch
	return &copy, nil
}

func (f *handoffRecorderFake) GetTaskBranch(_ context.Context, _, taskID, repoName string) (*domain.TaskBranch, error) {
	branch, ok := f.branches[taskID+"/"+repoName]
	if !ok {
		return nil, domain.ErrNotFound
	}
	copy := branch
	return &copy, nil
}

func (f *handoffRecorderFake) CreateTaskChangeSet(_ context.Context, changeSet domain.TaskChangeSet) (*domain.TaskChangeSet, error) {
	if f.changeSet != nil {
		return nil, domain.ErrAlreadyExists
	}
	f.changeSet = &changeSet
	return &changeSet, nil
}

func (f *handoffRecorderFake) GetTaskChangeSet(_ context.Context, _, _ string, _ int) (*domain.TaskChangeSet, error) {
	if f.changeSet == nil {
		return nil, domain.ErrNotFound
	}
	return f.changeSet, nil
}

func TestInspectCompositeCommitsRequiresBackendToCommitEveryChange(t *testing.T) {
	repoA := filepath.Join(t.TempDir(), "repo-a")
	repoB := filepath.Join(t.TempDir(), "repo-b")
	newGitWorktree(t, repoA)
	newGitWorktree(t, repoB)
	baseA := strings.TrimSpace(testGitOutput(t, repoA, "rev-parse", "HEAD"))
	baseB := strings.TrimSpace(testGitOutput(t, repoB, "rev-parse", "HEAD"))
	gitCmd(t, repoA, "checkout", "-b", "loom/task/TEST-1/repo-a")
	gitCmd(t, repoB, "checkout", "-b", "loom/task/TEST-1/repo-b")
	root := TaskRoot{Repositories: []TaskRootRepository{
		{Name: "repo-a", Path: repoA, BranchName: "loom/task/TEST-1/repo-a", BaseSHA: baseA},
		{Name: "repo-b", Path: repoB, BranchName: "loom/task/TEST-1/repo-b", BaseSHA: baseB},
	}}

	if err := os.WriteFile(filepath.Join(repoA, "README.md"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	inspection, err := inspectCompositeCommits(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Outcome != changeHandoffContinuationRequired || len(inspection.DirtyRepositories) != 1 || inspection.DirtyRepositories[0] != "repo-a" {
		t.Fatalf("dirty inspection = %+v, want repo-a continuation", inspection)
	}

	gitCmd(t, repoA, "add", "README.md")
	gitCmd(t, repoA, "commit", "-m", "backend-authored change")
	inspection, err = inspectCompositeCommits(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Outcome != changeHandoffReadyToReview || len(inspection.Repositories) != 1 {
		t.Fatalf("clean inspection = %+v, want one changed repository", inspection)
	}
	if inspection.Repositories[0].Name != "repo-a" || inspection.Repositories[0].BaseSHA != baseA || inspection.Repositories[0].HeadSHA == baseA {
		t.Fatalf("changed repository = %+v", inspection.Repositories[0])
	}
}

func TestGitPushProxyPreservesConfirmedReposAcrossPartialFailure(t *testing.T) {
	root := t.TempDir()
	recorder := newHandoffRecorderFake()
	repositories := make([]TaskRootRepository, 0, 2)
	remotes := map[string]string{}
	for _, name := range []string{"repo-a", "repo-b"} {
		repo := filepath.Join(root, name)
		remote := filepath.Join(root, name+".git")
		newGitWorktree(t, repo)
		if err := os.MkdirAll(remote, 0o755); err != nil {
			t.Fatal(err)
		}
		gitCmd(t, remote, "init", "--bare", "-q")
		base := strings.TrimSpace(testGitOutput(t, repo, "rev-parse", "HEAD"))
		branch := stableTaskBranch("TEST-1", name)
		gitCmd(t, repo, "checkout", "-b", branch)
		if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte(name+" changed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitCmd(t, repo, "add", "README.md")
		gitCmd(t, repo, "commit", "-m", "backend "+name)
		remoteURL := remote
		if name == "repo-b" {
			remoteURL = filepath.Join(root, "missing.git")
		}
		gitCmd(t, repo, "remote", "add", "origin", remoteURL)
		remotes[name] = remote
		repositories = append(repositories, TaskRootRepository{Name: name, Path: repo, BranchName: branch, BaseSHA: base})
	}
	inspection, err := inspectCompositeCommits(t.Context(), TaskRoot{Repositories: repositories})
	if err != nil {
		t.Fatal(err)
	}
	proxy := GitPushProxy{Recorder: recorder}
	req := TaskExecRequest{WorkspaceKey: "TEST", TaskID: "TEST-1", ExecutionClass: domain.TaskRunExecutionImplementation}
	claim := CompletionClaim{Request: req, Inspection: inspection, ArtifactRefs: []string{"transcript-1"}}
	if _, err := proxy.Finalize(t.Context(), claim); err == nil {
		t.Fatal("first publication succeeded despite repo-b remote failure")
	}
	confirmedA := recorder.branches["TEST-1/repo-a"]
	if confirmedA.ConfirmedRemoteHeadSHA == "" || recorder.changeSet != nil {
		t.Fatalf("partial state = branch %+v changeSet %+v", confirmedA, recorder.changeSet)
	}
	putsA := recorder.puts["TEST-1/repo-a"]

	repoB := repositories[1].Path
	gitCmd(t, repoB, "remote", "set-url", "origin", remotes["repo-b"])
	outcome, err := proxy.Finalize(t.Context(), claim)
	if err != nil {
		t.Fatalf("retry publication: %v", err)
	}
	if outcome.ChangeSet == nil || outcome.ChangeSet.Version != 1 || len(outcome.ChangeSet.Entries) != 2 {
		t.Fatalf("change set = %+v, want version 1 with two entries", outcome.ChangeSet)
	}
	if recorder.puts["TEST-1/repo-a"] != putsA {
		t.Fatalf("retry rewrote already-confirmed repo-a: puts %d -> %d", putsA, recorder.puts["TEST-1/repo-a"])
	}
	if _, err := proxyGit(t.Context(), repositories[1].Path, "", "ls-remote", "--exit-code", "origin", "refs/heads/"+repositories[1].BranchName); err != nil {
		t.Fatalf("repo-b remote branch missing: %v", err)
	}
}
