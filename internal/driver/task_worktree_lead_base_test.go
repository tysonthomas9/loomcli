package driver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/driverfreeze"
	"github.com/tysonthomas9/loomcli/internal/loomgit/review"
	loomworkspace "github.com/tysonthomas9/loomcli/internal/loomgit/workspace"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestDelegatedTaskBasesOnLeadTipWithUnresolvedLineage(t *testing.T) {
	f := setupLineageFixture(t)
	f.resolver.Lineage = unresolvedLineageLookup(t)
	ctx := context.Background()
	local, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatal(err)
	}
	source := local.Workspaces["TEST"].Repos["app"]
	wsDir := filepath.Join(t.TempDir(), "lead-workspace")
	session, err := loomworkspace.Ensure(ctx, "TEST", "main", wsDir, []loomworkspace.Source{{Name: "app", Path: source}})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.RowsWritten(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	areas, err := loomworkspace.EnsureWorkingArea(ctx, "TEST", "L1", wsDir, []loomworkspace.WorkingAreaSource{{Name: "app", Path: source}})
	if err != nil {
		t.Fatal(err)
	}
	leadPath := areas[0].Path
	writeTestFile(t, filepath.Join(leadPath, "lead.txt"), "lead\n")
	gitCmd(t, leadPath, "add", "lead.txt")
	gitCmd(t, leadPath, "commit", "-m", "lead")
	leadTip := strings.TrimSpace(testGitOutput(t, leadPath, "rev-parse", "HEAD"))
	if _, err := f.resolver.Store.AgentSessions().Create(ctx, store.AgentSessionCreate{WorkspaceKey: "TEST", SessionID: "lead-session", AgentID: "L1", Kind: domain.AgentSessionKindOrchestration}); err != nil {
		t.Fatal(err)
	}
	request := TaskExecRequest{WorkspaceKey: "TEST", TaskRunID: "task/own", TaskID: "independent", ParentSessionID: "lead-session", SandboxPlacement: domain.TaskRunPlacement{RepoRef: "frontend"}}
	request.Input, err = WithLineage(request.Input, TaskLineage{StackID: "unreadable", BaseRef: "missing"})
	if err != nil {
		t.Fatal(err)
	}
	copy, err := f.resolver.ResolveTaskWorktree(ctx, request, t.TempDir())
	if err != nil || copy.BaseSHA != leadTip {
		t.Fatalf("independent base = %s, want %s: %v", copy.BaseSHA, leadTip, err)
	}
}

func TestDelegatedTaskFromCurrentWorkspaceState(t *testing.T) {
	f := setupLineageFixture(t)
	ctx := context.Background()
	local, err := bootstrap.LoadStateCache()
	if err != nil {
		t.Fatal(err)
	}
	source := local.Workspaces["TEST"].Repos["app"]
	wsDir := filepath.Join(t.TempDir(), "lead-workspace")
	session, err := loomworkspace.Ensure(ctx, "TEST", "main", wsDir, []loomworkspace.Source{{Name: "app", Path: source}})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.RowsWritten(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	areas, err := loomworkspace.EnsureWorkingArea(ctx, "TEST", "L1", wsDir, []loomworkspace.WorkingAreaSource{{Name: "app", Path: source}})
	if err != nil {
		t.Fatal(err)
	}
	leadPath := areas[0].Path
	writeTestFile(t, filepath.Join(leadPath, "user.txt"), "uncommitted user work\n")
	if _, err := f.resolver.Store.AgentSessions().Create(ctx, store.AgentSessionCreate{WorkspaceKey: "TEST", SessionID: "lead-session", AgentID: "L1", Kind: domain.AgentSessionKindOrchestration}); err != nil {
		t.Fatal(err)
	}
	queued, err := createQueuedTaskRun(ctx, f.resolver.Store, TaskRunRequestOptions{
		WorkspaceKey: "TEST", TaskRunID: "task/current", TaskID: "current", ParentSessionID: "lead-session",
	}, taskRunRequestRefs{TaskRunID: "task/current"})
	if err != nil {
		t.Fatal(err)
	}
	request := TaskExecRequest{WorkspaceKey: "TEST", TaskRunID: queued.TaskRunID, TaskID: queued.TaskID, ParentSessionID: queued.RuntimeMetadata["parent_session_id"], Input: queued.Input, SandboxPlacement: domain.TaskRunPlacement{RepoRef: "frontend"}}
	if current, err := currentWorkspaceStateFromInput(request.Input); err != nil || !current {
		t.Fatalf("lead delegation did not request current state: %s, %v", request.Input, err)
	}
	copy, err := f.resolver.ResolveTaskWorktree(ctx, request, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(testGitOutput(t, copy.Path, "show", "HEAD:user.txt")); got != "uncommitted user work" {
		t.Fatalf("task copy user work = %q", got)
	}
	if got := strings.TrimSpace(testGitOutput(t, leadPath, "status", "--porcelain")); got != "?? user.txt" {
		t.Fatalf("lead worktree status = %q, want uncommitted user.txt", got)
	}
	if got := strings.TrimSpace(testGitOutput(t, leadPath, "for-each-ref", "--format=%(refname)", "refs/loom/ws/TEST/wip/L1")); !strings.HasPrefix(got, "refs/loom/ws/TEST/wip/L1/") {
		t.Fatalf("missing WIP ref: %q", got)
	}
	if got := strings.TrimSpace(testGitOutput(t, copy.Path, "rev-parse", "HEAD")); got != copy.BaseSHA {
		t.Fatalf("task copy HEAD = %s, base = %s", got, copy.BaseSHA)
	}
	if got := testGitOutput(t, copy.Path, "show", "-s", "--format=%s", "HEAD"); !strings.Contains(got, "delegation") {
		t.Fatalf("WIP commit does not record delegation: %q", got)
	}
	writeTestFile(t, filepath.Join(leadPath, "user.txt"), "later user work\n")
	request.SchedulerAttempt = 1
	request.PreviousAttemptID = copy.AttemptID
	retry, err := f.resolver.ResolveTaskWorktree(ctx, request, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if retry.BaseSHA != copy.BaseSHA || strings.TrimSpace(testGitOutput(t, retry.Path, "show", "HEAD:user.txt")) != "uncommitted user work" {
		t.Fatalf("retry did not preserve the original WIP base: %+v", retry)
	}
	writeTestFile(t, filepath.Join(leadPath, ".env"), "SECRET=untracked\n")
	request.TaskRunID = "task/incomplete"
	request.PreviousAttemptID = ""
	_, err = f.resolver.ResolveTaskWorktree(ctx, request, t.TempDir())
	if !errors.Is(err, loomgit.NewError(loomgit.CaptureIncomplete, "", nil)) {
		t.Fatalf("incomplete WIP capture was accepted: %v", err)
	}
	gitCmd(t, source, "checkout", "-b", "agent-conflict", f.mainHead)
	writeTestFile(t, filepath.Join(source, "src", "app.js"), "console.log('agent');\n")
	gitCmd(t, source, "add", "src/app.js")
	gitCmd(t, source, "commit", "-m", "agent change")
	patch := testGitOutput(t, source, "diff", "--binary", f.mainHead, "HEAD")
	journalPath := filepath.Join(os.Getenv("LOOM_CONFIG_DIR"), "loomgit", "store.db")
	revision, err := driverfreeze.FreezeAt(ctx, journalPath, driverfreeze.Request{
		Workspace: "TEST", Task: "current", Repo: "app", Attempt: "agent-conflict",
		Worktree: source, Base: f.mainHead, Patch: []byte(patch), Outcome: "completed",
	})
	if err != nil {
		t.Fatal(err)
	}
	reviewer, err := review.OpenLocal()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reviewer.Close() }()
	if _, err := reviewer.Submit(ctx, "TEST", revision.Change, revision.Number, revision.HeadSHA, "approve", "", review.Actor{Kind: "human", ID: "reviewer"}); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, source, "checkout", "main")
	writeTestFile(t, filepath.Join(leadPath, "src", "app.js"), "console.log('lead');\n")
	gitCmd(t, leadPath, "add", "src/app.js")
	gitCmd(t, leadPath, "commit", "-m", "lead change")
	leadTip := strings.TrimSpace(testGitOutput(t, leadPath, "rev-parse", "HEAD"))
	request.TaskRunID = "task/conflict"
	request.SchedulerAttempt = 0
	resolutionInput, err := WithConflictResolution(nil, BaseRevision{Change: revision.Change, Number: revision.Number})
	if err != nil {
		t.Fatal(err)
	}
	resolutionRun, err := createQueuedTaskRun(ctx, f.resolver.Store, TaskRunRequestOptions{
		WorkspaceKey: "TEST", TaskRunID: "task/conflict", TaskID: "current", ParentSessionID: "lead-session", Input: resolutionInput,
	}, taskRunRequestRefs{TaskRunID: "task/conflict"})
	if err != nil {
		t.Fatal(err)
	}
	request.Input = resolutionRun.Input
	if current, err := currentWorkspaceStateFromInput(request.Input); err != nil || current {
		t.Fatalf("conflict action was replaced by current-state delegation: %s, %v", request.Input, err)
	}
	resolution, err := f.resolver.ResolveTaskWorktree(ctx, request, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if resolution.BaseSHA != leadTip || !strings.Contains(testGitOutput(t, resolution.Path, "ls-files", "-u"), "src/app.js") {
		t.Fatalf("conflict resolution did not start at lead tip with conflicts: %+v", resolution)
	}
}

func unresolvedLineageLookup(t *testing.T) TaskLineageLookup {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "stacks.json"), []byte("invalid json"), 0o600); err != nil {
		t.Fatal(err)
	}
	return StackLineageLookup{Store: stackstore.New(dir)}
}
