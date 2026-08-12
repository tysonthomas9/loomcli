package agent

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
)

// newPlanCmd creates a fresh cobra.Command wired to runPlan with given deps.
func newPlanCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:  "plan [worktree|workspace]",
		Args: cobra.MaximumNArgs(1),
		Run:  runPlan,
	}
	cmd.SetContext(WithDeps(context.Background(), deps))
	return cmd
}

// setupPlanTracker configures deps.WorkItems with the given issues and installs
// it as the global default tracker. Cleanup restores the original tracker.
func setupPlanTracker(t *testing.T, deps *Deps, issues []workitems.IssueSummary) {
	t.Helper()
	tracker := deps.WorkItems.(*MockWorkItems)
	tracker.ReadyResult = issues
	resetDefaultWorkItems()
	setDefaultWorkItems(tracker)
	t.Cleanup(resetDefaultWorkItems)
}

func TestRunPlan_SingleTask_NoTasksAvailable(t *testing.T) {
	// not parallel: uses global default tracker, os.Stdout capture

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	deps, _, _, _, _ := NewTestDeps(t)
	setupPlanTracker(t, deps, nil)

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := newPlanCmd(deps)
	cmd.Run(cmd, []string{tmpDir})

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "No tasks available for planning") {
		t.Errorf("expected 'No tasks available for planning' in output, got: %s", output)
	}
}

func TestRunPlan_SingleTask_Success(t *testing.T) {
	// not parallel: uses global default tracker, os.Stdout capture

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	deps, _, _, _, _ := NewTestDeps(t)
	setupPlanTracker(t, deps, []workitems.IssueSummary{
		{ID: "loom-123", Status: "open", IssueType: "task", Title: "Test task"},
	})

	recorder := SetupMockAgentInvokerOn(t, deps, nil)

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := newPlanCmd(deps)
	cmd.Run(cmd, []string{tmpDir})

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "Running PLANNING agent") {
		t.Errorf("expected 'Running PLANNING agent' banner in output, got: %s", output)
	}
	if len(recorder.InteractiveCalls) != 1 {
		t.Fatalf("expected 1 agent invocation, got %d", len(recorder.InteractiveCalls))
	}

	_, err := os.Stat(filepath.Join(tmpDir, LockFileName))
	if err == nil {
		t.Error("lock file should be released after runPlan completes")
	}
}

func TestRunPlan_SkipsEpics(t *testing.T) {
	// not parallel: uses global default tracker, os.Stdout capture

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	deps, _, _, _, _ := NewTestDeps(t)
	setupPlanTracker(t, deps, []workitems.IssueSummary{
		{ID: "loom-123", Status: "open", IssueType: "epic", Title: "Test epic"},
	})

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := newPlanCmd(deps)
	cmd.Run(cmd, []string{tmpDir})

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "No tasks available for planning") {
		t.Errorf("expected 'No tasks available for planning' when only epics exist, got: %s", output)
	}
}

func TestRunPlan_SkipsInProgress(t *testing.T) {
	// not parallel: uses global default tracker, os.Stdout capture

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	deps, _, _, _, _ := NewTestDeps(t)
	setupPlanTracker(t, deps, []workitems.IssueSummary{
		{ID: "loom-123", Status: "in_progress", IssueType: "task", Title: "Test task"},
	})

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	cmd := newPlanCmd(deps)
	cmd.Run(cmd, []string{tmpDir})

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	if !strings.Contains(output, "No tasks available for planning") {
		t.Errorf("expected 'No tasks available for planning' when all tasks in_progress, got: %s", output)
	}
}

func TestHasAvailablePlanningTasks_Success(t *testing.T) {
	// not parallel: uses mock.Install() which mutates global defaultDeps.Exec
	taskJSON := `[{"id":"loom-1","status":"open","issue_type":"task","design":""}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "issue-store", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
	})
	mock.Install()

	available, err := HasAvailablePlanningTasks(t.Context(), "", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !available {
		t.Error("expected tasks to be available for planning")
	}
}

func TestHasAvailablePlanningTasks_SkipsTasksWithDesign(t *testing.T) {
	// not parallel: uses mock.Install() which mutates global defaultDeps.Exec
	taskJSON := `[{"id":"loom-1","status":"open","issue_type":"task","design":"Some plan here"}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "issue-store", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
	})
	mock.Install()

	available, err := HasAvailablePlanningTasks(t.Context(), "", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if available {
		t.Error("tasks with existing design should not be available for planning")
	}
}

func TestHasAvailablePlanningTasks_ReadyError(t *testing.T) {
	// not parallel: uses mock.Install() which mutates global defaultDeps.Exec
	mock := NewCommandMock(t, []CommandStub{
		{Name: "issue-store", Args: []string{"ready", "--json", "--limit", "100"}, Err: os.ErrNotExist},
	})
	mock.Install()

	_, err := HasAvailablePlanningTasks(t.Context(), "", "")
	if err == nil {
		t.Error("expected error when issue-store command fails")
	}
}

// ============================================================================
// GetAvailablePlanningTasks Tests (CommandMock-based)
// ============================================================================

func TestGetAvailablePlanningTasks_Success(t *testing.T) {
	// not parallel: uses mock.Install() which mutates global defaultDeps.Exec
	taskJSON := `[{"id":"loom-1","status":"open","issue_type":"task","design":""}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "issue-store", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
	})
	mock.Install()

	tasks, err := GetAvailablePlanningTasks(t.Context(), "", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].ID != "loom-1" {
		t.Errorf("expected task ID 'loom-1', got %q", tasks[0].ID)
	}
}

func TestGetAvailablePlanningTasks_ReturnsEmpty(t *testing.T) {
	// not parallel: uses mock.Install() which mutates global defaultDeps.Exec
	taskJSON := `[{"id":"loom-1","status":"open","issue_type":"task","design":"Some plan here"}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "issue-store", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
	})
	mock.Install()

	tasks, err := GetAvailablePlanningTasks(t.Context(), "", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestGetAvailablePlanningTasks_ReadyError(t *testing.T) {
	// not parallel: uses mock.Install() which mutates global defaultDeps.Exec
	mock := NewCommandMock(t, []CommandStub{
		{Name: "issue-store", Args: []string{"ready", "--json", "--limit", "100"}, Err: os.ErrNotExist},
	})
	mock.Install()

	_, err := GetAvailablePlanningTasks(t.Context(), "", "")
	if err == nil {
		t.Error("expected error when issue-store command fails")
	}
}
