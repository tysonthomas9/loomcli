package driver

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/bootstrap"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/loomgit"
	"github.com/tysonthomas9/loomcli/internal/loomgit/taskcopy"
	loomworkspace "github.com/tysonthomas9/loomcli/internal/loomgit/workspace"
	"github.com/tysonthomas9/loomcli/internal/stackstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// reviewBases answers the code-review base lookup: task-c and task-l are built
// on task-a, whose code awaits review in their epic; other tasks are not.
func reviewBases(_ context.Context, workspace, task string) (string, bool, error) {
	if workspace == "TEST" && (task == "task-c" || task == "task-l") {
		return "task-a", true, nil
	}
	return "", false, nil
}

// withReviewBases is the lineage fixture with no local stack for the tasks
// asked about and FleetDB's code-review bases.
func withReviewBases(t *testing.T, lookup CodeReviewBaseLookup) lineageFixture {
	t.Helper()
	f := setupLineageFixture(t)
	f.resolver.Lineage = StackLineageLookup{Store: stackstore.New(f.loomDir), CodeReviewBase: lookup}
	return f
}

// leadSession gives the fixture a lead L1 with a commit of its own in its
// working area and returns the lead's tip.
func leadSession(t *testing.T, f lineageFixture) string {
	t.Helper()
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
	if _, err := f.resolver.Store.AgentSessions().Create(ctx, store.AgentSessionCreate{WorkspaceKey: "TEST", SessionID: "lead-session", AgentID: "L1", Kind: domain.AgentSessionKindOrchestration}); err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(testGitOutput(t, leadPath, "rev-parse", "HEAD"))
}

// delegated resolves a task copy the lead delegates, as the epic runner does:
// from the lead's current workspace state.
func delegated(t *testing.T, f lineageFixture, task string) (TaskWorktree, error) {
	t.Helper()
	ctx := context.Background()
	queued, err := createQueuedTaskRun(ctx, f.resolver.Store, TaskRunRequestOptions{
		WorkspaceKey: "TEST", TaskRunID: "task/" + task, TaskID: task, ParentSessionID: "lead-session",
	}, taskRunRequestRefs{TaskRunID: "task/" + task})
	if err != nil {
		t.Fatal(err)
	}
	return f.resolver.ResolveTaskWorktree(ctx, TaskExecRequest{WorkspaceKey: "TEST", TaskRunID: queued.TaskRunID, TaskID: task,
		ParentSessionID: "lead-session", Input: queued.Input, SandboxPlacement: domain.TaskRunPlacement{RepoRef: "frontend"}}, t.TempDir())
}

func pinnedTo(t *testing.T, task, sha string) {
	t.Helper()
	status, err := taskcopy.ReadLineageStatus(context.Background(), "TEST", task, "app")
	if err != nil || status.BasedOn.SHA != sha || status.BasedOn.Revision != 1 || status.State != "current" {
		t.Fatalf("%s lineage = %+v, %v; want pinned to revision 1 at %s", task, status, err, sha)
	}
}

// Option 1 (2026-10-09): a task whose only open blocker's code awaits review
// in its epic is built on that blocker's frozen revision with no local stack,
// and the base is pinned.
func TestTaskBehindCodeReviewBuildsOnTheBlockersRevision(t *testing.T) {
	f := withReviewBases(t, reviewBases)
	if got := resolveHead(t, f.resolver, "task-c", "task/run:c"); got != f.taskAH {
		t.Fatalf("task-c base = %s, want task-a's revision %s", got, f.taskAH)
	}
	pinnedTo(t, "task-c", f.taskAH)
	if got := resolveHead(t, f.resolver, "task-d", "task/run:d"); got != f.mainHead {
		t.Fatalf("task-d base = %s, want the default branch %s", got, f.mainHead)
	}
}

// A lead delegates from its working area, which lacks the blocker's code: a
// task behind code review is built on the blocker's revision instead.
func TestLeadDelegatedTaskBehindCodeReviewBuildsOnTheBlockersRevision(t *testing.T) {
	f := withReviewBases(t, reviewBases)
	leadTip := leadSession(t, f)
	copy, err := delegated(t, f, "task-l")
	if err != nil || copy.BaseSHA != f.taskAH {
		t.Fatalf("delegated task-l base = %s, %v; want task-a's revision %s", copy.BaseSHA, err, f.taskAH)
	}
	pinnedTo(t, "task-l", f.taskAH)
	other, err := delegated(t, f, "task-m")
	if err != nil || other.BaseSHA == f.taskAH {
		t.Fatalf("delegated task-m base = %s, %v; want the lead's (tip %s)", other.BaseSHA, err, leadTip)
	}
	if got := strings.TrimSpace(testGitOutput(t, other.Path, "show", "HEAD:lead.txt")); got != "lead" {
		t.Fatalf("delegated task-m lacks the lead's work: %q", got)
	}
}

// A failed lookup stops the copy: guessing would build the task without its
// blocker's code.
func TestTaskCopyStopsWhenTheCodeReviewLookupFails(t *testing.T) {
	f := withReviewBases(t, func(context.Context, string, string) (string, bool, error) {
		return "", false, errors.New("fleet down")
	})
	_, err := f.resolver.ResolveTaskWorktree(context.Background(), TaskExecRequest{WorkspaceKey: "TEST", TaskRunID: "task/run:x",
		TaskID: "task-x", SandboxPlacement: domain.TaskRunPlacement{RepoRef: "frontend"}}, t.TempDir())
	if !errors.Is(err, loomgit.NewError(loomgit.LineageUnresolved, "", nil)) {
		t.Fatalf("plain copy with a failed lookup = %v, want lineage_unresolved", err)
	}
	leadSession(t, f)
	if _, err := delegated(t, f, "task-y"); !errors.Is(err, loomgit.NewError(loomgit.LineageUnresolved, "", nil)) {
		t.Fatalf("delegated copy with a failed lookup = %v, want lineage_unresolved", err)
	}
}
