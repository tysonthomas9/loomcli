package monitor

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
)

// TestCollectMonitorDataScoped_EmptyPathReturnsZero verifies that an unknown
// or empty workspace path yields a zero-value MonitorData with just a
// Timestamp — no silent fall-back to cross-workspace data.
func TestCollectMonitorDataScoped_EmptyPathReturnsZero(t *testing.T) {
	data := CollectMonitorDataScoped("", "", nil, 10, "")
	if data == nil {
		t.Fatal("expected non-nil MonitorData, got nil")
	}
	if data.Timestamp.IsZero() {
		t.Error("expected Timestamp to be set")
	}
	if len(data.Agents) != 0 {
		t.Errorf("expected no agents for empty wsPath, got %d", len(data.Agents))
	}
	if data.Stats.Total != 0 || data.Stats.Closed != 0 {
		t.Errorf("expected zero-value Stats, got %+v", data.Stats)
	}
	if data.Tasks.InProgress != 0 || data.Tasks.NeedsPlanning != 0 {
		t.Errorf("expected zero-value Tasks, got %+v", data.Tasks)
	}
}

// TestCollectMonitorDataDepsScoped_DistinctWorkspaces verifies that two
// distinct deps (simulating two workspaces with different bd daemons) yield
// distinct stats/tasks — the invariant the scoped API exists to preserve.
func TestCollectMonitorDataDepsScoped_DistinctWorkspaces(t *testing.T) {
	// Workspace A has 7 closed and one in-progress task.
	depsA, _, execA, _, mockA := NewTestDeps(t)
	execA.RunFunc = func(dir, name string, args ...string) CommandResult {
		return CommandResult{Stdout: "ok"}
	}
	mockA.StatsResult = &backend.StatsData{TotalIssues: 10, OpenIssues: 2, ClosedIssues: 7, InProgressIssues: 1}
	mockA.ListFn = func(_ context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
		if opts.Status == "in_progress" {
			return []backend.IssueData{{ID: "A-1", Title: "Task from A", Status: "in_progress"}}, nil
		}
		return nil, nil
	}
	depsA.IssueBackend = mockA

	// Workspace B has no closed, three in-progress tasks.
	depsB, _, execB, _, mockB := NewTestDeps(t)
	execB.RunFunc = func(dir, name string, args ...string) CommandResult {
		return CommandResult{Stdout: "ok"}
	}
	mockB.StatsResult = &backend.StatsData{TotalIssues: 5, OpenIssues: 2, ClosedIssues: 0, InProgressIssues: 3}
	mockB.ListFn = func(_ context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
		if opts.Status == "in_progress" {
			return []backend.IssueData{
				{ID: "B-1", Title: "B task 1", Status: "in_progress"},
				{ID: "B-2", Title: "B task 2", Status: "in_progress"},
				{ID: "B-3", Title: "B task 3", Status: "in_progress"},
			}, nil
		}
		return nil, nil
	}
	depsB.IssueBackend = mockB

	// Point workspaces at separate (empty) dirs so worktree discovery returns
	// nothing and the test doesn't pick up host state.
	wsA := filepath.Join(t.TempDir(), "ws-a")
	wsB := filepath.Join(t.TempDir(), "ws-b")
	if err := os.MkdirAll(wsA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wsB, 0o755); err != nil {
		t.Fatal(err)
	}

	dataA := collectMonitorDataDepsScoped(depsA, wsA, nil, 10, "")
	dataB := collectMonitorDataDepsScoped(depsB, wsB, nil, 10, "")

	if dataA.Stats.Closed != 7 {
		t.Errorf("workspace A: Stats.Closed = %d, want 7", dataA.Stats.Closed)
	}
	if dataB.Stats.Closed != 0 {
		t.Errorf("workspace B: Stats.Closed = %d, want 0", dataB.Stats.Closed)
	}
	if dataA.Tasks.InProgress != 1 {
		t.Errorf("workspace A: Tasks.InProgress = %d, want 1", dataA.Tasks.InProgress)
	}
	if dataB.Tasks.InProgress != 3 {
		t.Errorf("workspace B: Tasks.InProgress = %d, want 3", dataB.Tasks.InProgress)
	}

	if len(dataA.InProgressTasks) != 1 || dataA.InProgressTasks[0].ID != "A-1" {
		t.Errorf("workspace A: InProgressTasks = %+v, want [A-1]", dataA.InProgressTasks)
	}
	if len(dataB.InProgressTasks) != 3 || dataB.InProgressTasks[0].ID != "B-1" {
		t.Errorf("workspace B: InProgressTasks = %+v, want [B-1, B-2, B-3]", dataB.InProgressTasks)
	}
}

// TestBuildSingleAgentStatusCollector_EmptyWorktreePath verifies the
// collector reports an error (and does no further work) when the input
// WorktreePath is empty.
func TestBuildSingleAgentStatusCollector_EmptyWorktreePath(t *testing.T) {
	collect := BuildSingleAgentStatusCollector()
	res := collect(SingleAgentStatusInput{
		WorktreePath:  "",
		AgentName:     "x",
		Repo:          "r",
		DefaultBranch: "main",
	})
	if res.Err == nil {
		t.Fatalf("expected Err, got nil result=%+v", res)
	}
}

// TestBuildSingleAgentStatusCollector_DefaultBranchFallback verifies that an
// empty DefaultBranch on the input does not panic — the collector applies its
// internal "main" fallback. We don't set up a real worktree, so the observable
// effect is just zero values + no crash.
func TestBuildSingleAgentStatusCollector_DefaultBranchFallback(t *testing.T) {
	tmp := t.TempDir()
	fakeWT := filepath.Join(tmp, "no-such-worktree")
	collect := BuildSingleAgentStatusCollector()
	res := collect(SingleAgentStatusInput{
		WorktreePath:  fakeWT,
		AgentName:     "agent",
		Repo:          "repo",
		DefaultBranch: "", // exercise internal fallback to "main"
	})
	// No assertion on values — collector must simply return without panicking.
	_ = res
}

// TestCollectSyncBdStatusDepsScoped_UsesWorkspacePath verifies that the
// scoped sync invocation passes wsPath as the exec working directory rather
// than cli.GetBeadsDir() (the launch workspace).
func TestCollectSyncBdStatusDepsScoped_UsesWorkspacePath(t *testing.T) {
	deps, _, exec, _, _ := NewTestDeps(t)
	var gotDir string
	exec.RunFunc = func(dir, name string, args ...string) CommandResult {
		gotDir = dir
		return CommandResult{Stdout: "ok"}
	}
	deps.Exec = exec

	wsPath := filepath.Join(t.TempDir(), "target-workspace")
	_ = collectSyncBdStatusDepsScoped(deps, wsPath)

	if gotDir != wsPath {
		t.Errorf("bd sync was invoked in %q, want %q (scoped path must override cli.GetBeadsDir)", gotDir, wsPath)
	}
}
