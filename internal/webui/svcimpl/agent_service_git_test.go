package svcimpl

import (
	"context"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/ops"
)

func TestAgentServiceGitOperations(t *testing.T) {
	fake := &fakeGitOps{
		wt: &ops.AgentWorktree{
			Name: "agent", Path: "/repo", Branch: "agent-branch",
			DefaultBranch: "main", Remote: "origin", RepoName: "api", IsWorkspace: true,
		},
		push: &ops.GitPushResult{Success: true, Message: "pushed"},
		pull: &ops.GitPullResult{Success: true, Message: "pulled"},
		pr:   &ops.GitPRResult{URL: "https://example/pr/1", Created: true},
		reset: &ops.GitResetResult{
			Success: true, Message: "reset", PreviousBranch: "old", Pushed: true,
		},
		status:     &ops.GitStatusResult{Branch: "agent-branch"},
		current:    "agent-branch",
		diff:       ops.DiffStatResult{LinesAdded: 5, LinesRemoved: 2},
		worktrees:  []ops.AgentWorktree{{Name: "ok", Path: "/ok", Branch: "b", DefaultBranch: "main"}, {Name: "uptodate", Path: "/up", Branch: "b", DefaultBranch: "main"}, {Name: "fail", Path: "/fail", Branch: "b", DefaultBranch: "main"}},
		pushByPath: map[string]*ops.GitPushResult{"/up": {Success: true, AlreadyUpToDate: true}, "/fail": {Success: false, Message: "conflict"}},
	}
	svc := NewAgentService(fake, nil, nil, nil)
	ctx := context.Background()

	diff, err := svc.GetDiffStat(ctx, "WS", "agent")
	if err != nil || diff.Added != 5 || diff.Removed != 2 || diff.Branch != "agent-branch" {
		t.Fatalf("diff = %+v err=%v", diff, err)
	}
	push, err := svc.GitPush(ctx, "WS", "agent", "")
	if err != nil || !push.Success || fake.lastPushTarget != "main" {
		t.Fatalf("push = %+v target=%q err=%v", push, fake.lastPushTarget, err)
	}
	pull, err := svc.GitPull(ctx, "WS", "agent", "")
	if err != nil || !pull.Success || fake.lastPullCurrent != "agent-branch" || fake.lastPullSource != "main" {
		t.Fatalf("pull = %+v current=%q source=%q err=%v", pull, fake.lastPullCurrent, fake.lastPullSource, err)
	}
	sync, err := svc.GitSync(ctx, "WS", "agent")
	if err != nil || sync.PushResult == nil || sync.PullResult == nil {
		t.Fatalf("sync = %+v err=%v", sync, err)
	}
	pr, err := svc.CreatePR(ctx, "WS", "agent", "")
	if err != nil || !pr.Created {
		t.Fatalf("pr = %+v err=%v", pr, err)
	}
	reset, err := svc.GitReset(ctx, "WS", "agent", "", true, true)
	if err != nil || !reset.Success || fake.lastResetBranch != "main" {
		t.Fatalf("reset = %+v branch=%q err=%v", reset, fake.lastResetBranch, err)
	}
	status, err := svc.GitStatus(ctx, "WS", "agent")
	if err != nil || status.Branch != "agent-branch" {
		t.Fatalf("status = %+v err=%v", status, err)
	}
	if err := svc.SetTargetBranch(ctx, "WS", "agent", "develop"); err != nil || fake.lastSetBranch != "develop" {
		t.Fatalf("set target err=%v branch=%q", err, fake.lastSetBranch)
	}
	all, err := svc.GitPushAll(ctx, "WS")
	if err != nil || all.Pushed != 1 || all.Failed != 1 || len(all.Results) != 3 {
		t.Fatalf("push all = %+v err=%v", all, err)
	}
}

func TestAgentServiceGitOperationErrors(t *testing.T) {
	ctx := context.Background()
	svc := NewAgentService(&fakeGitOps{resolveErr: errors.New("missing")}, nil, nil, nil)
	if _, err := svc.GetDiffStat(ctx, "WS", "agent"); err == nil {
		t.Fatal("missing worktree returned nil error")
	}
	if _, err := svc.GetDiffStat(ctx, "WS", "../bad"); err == nil {
		t.Fatal("invalid agent returned nil error")
	}

	fake := &fakeGitOps{wt: &ops.AgentWorktree{Name: "agent", Path: "/repo", Branch: "b", DefaultBranch: "main"}, ghErr: errors.New("no gh")}
	svc = NewAgentService(fake, nil, nil, nil)
	if _, err := svc.CreatePR(ctx, "WS", "agent", ""); err == nil {
		t.Fatal("missing gh returned nil error")
	}
	fake.ghErr = nil
	fake.currentErr = errors.New("branch failed")
	if _, err := svc.GitPull(ctx, "WS", "agent", "main"); err == nil {
		t.Fatal("current branch failure returned nil error")
	}
	if _, err := svc.GitSync(ctx, "WS", "agent"); err == nil {
		t.Fatal("sync current branch failure returned nil error")
	}
	fake.currentErr = nil
	fake.push = &ops.GitPushResult{Success: false, ConflictedFiles: []string{"file.go"}}
	sync, err := svc.GitSync(ctx, "WS", "agent")
	if err != nil || sync.PullResult != nil || len(sync.PushResult.ConflictedFiles) != 1 {
		t.Fatalf("conflict sync = %+v err=%v", sync, err)
	}
	fake.wt.IsWorkspace = false
	if err := svc.SetTargetBranch(ctx, "WS", "agent", "main"); err == nil {
		t.Fatal("non-workspace SetTargetBranch returned nil error")
	}
}

type fakeGitOps struct {
	wt         *ops.AgentWorktree
	resolveErr error
	push       *ops.GitPushResult
	pull       *ops.GitPullResult
	pr         *ops.GitPRResult
	reset      *ops.GitResetResult
	status     *ops.GitStatusResult
	diff       ops.DiffStatResult
	current    string
	currentErr error
	ghErr      error
	mergeBase  string
	mergeErr   error
	commits    []ops.DiffCommitResult
	commitErr  error
	files      []ops.DiffFileResult
	filesErr   error
	patch      *ops.DiffFilePatchResult
	patchErr   error
	worktrees  []ops.AgentWorktree
	pushByPath map[string]*ops.GitPushResult

	lastPushTarget  string
	lastPullCurrent string
	lastPullSource  string
	lastResetBranch string
	lastSetBranch   string
}

func (f *fakeGitOps) ResolveAgentWorktree(_, _ string) (*ops.AgentWorktree, error) {
	if f.resolveErr != nil {
		return nil, f.resolveErr
	}
	return f.wt, nil
}

func (f *fakeGitOps) Push(path, _, target, _ string) (*ops.GitPushResult, error) {
	f.lastPushTarget = target
	if r := f.pushByPath[path]; r != nil {
		return r, nil
	}
	if f.push != nil {
		return f.push, nil
	}
	return &ops.GitPushResult{Success: true}, nil
}

func (f *fakeGitOps) Pull(_, current, source, _ string) (*ops.GitPullResult, error) {
	f.lastPullCurrent = current
	f.lastPullSource = source
	if f.pull != nil {
		return f.pull, nil
	}
	return &ops.GitPullResult{Success: true}, nil
}

func (f *fakeGitOps) CreatePR(_, _, _, _ string) (*ops.GitPRResult, error) { return f.pr, nil }
func (f *fakeGitOps) Reset(_, _, branch string, _, _ bool) (*ops.GitResetResult, error) {
	f.lastResetBranch = branch
	return f.reset, nil
}
func (f *fakeGitOps) Status(_, _ string) (*ops.GitStatusResult, error) { return f.status, nil }
func (f *fakeGitOps) GetCurrentBranch(_ string) (string, error) {
	if f.currentErr != nil {
		return "", f.currentErr
	}
	return f.current, nil
}
func (f *fakeGitOps) CheckGhInstalled() error { return f.ghErr }
func (f *fakeGitOps) SetRepoDefaultBranch(_, _, branch string) error {
	f.lastSetBranch = branch
	return nil
}
func (f *fakeGitOps) ListAgentWorktrees(_ string) ([]ops.AgentWorktree, error) {
	return f.worktrees, nil
}
func (f *fakeGitOps) DiffStat(_, _ string) ops.DiffStatResult { return f.diff }
func (f *fakeGitOps) ResolveMergeBase(_, _ string) (string, error) {
	if f.mergeErr != nil {
		return "", f.mergeErr
	}
	return f.mergeBase, nil
}
func (f *fakeGitOps) DiffCommits(context.Context, string, string, int) ([]ops.DiffCommitResult, error) {
	if f.commitErr != nil {
		return nil, f.commitErr
	}
	return f.commits, nil
}
func (f *fakeGitOps) DiffFiles(context.Context, string, string, string) ([]ops.DiffFileResult, error) {
	if f.filesErr != nil {
		return nil, f.filesErr
	}
	return f.files, nil
}
func (f *fakeGitOps) DiffFilePatch(context.Context, string, string, string, string) (*ops.DiffFilePatchResult, error) {
	if f.patchErr != nil {
		return nil, f.patchErr
	}
	return f.patch, nil
}
