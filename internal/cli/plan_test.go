package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
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

// setupPlanTracker configures deps.Tracker with the given issues and installs
// it as the global default tracker. Cleanup restores the original tracker.
func setupPlanTracker(t *testing.T, deps *Deps, issues []BdIssue) {
	t.Helper()
	tracker := deps.Tracker.(*MockIssueTracker)
	tracker.ReadyResult = issues
	tracker.ListResult = issues
	resetDefaultTracker()
	setDefaultTracker(tracker)
	t.Cleanup(resetDefaultTracker)
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
	setupPlanTracker(t, deps, []BdIssue{
		{ID: "bd-123", Status: "open", IssueType: "task", Title: "Test task"},
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
	if len(recorder.Invocations) != 1 {
		t.Fatalf("expected 1 agent invocation, got %d", len(recorder.Invocations))
	}

	_, err := os.Stat(filepath.Join(tmpDir, LockFileName))
	if err == nil {
		t.Error("lock file should be released after runPlan completes")
	}
}

func TestRunPlan_DaemonMode_AcquiresLock(t *testing.T) {
	// not parallel: mutates global planDaemonMode, os.Chdir, os.Stdout capture

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	deps, _, _, _, _ := NewTestDeps(t)
	recorder := SetupMockAgentInvokerOn(t, deps, nil)

	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	origDaemon := planDaemonMode
	planDaemonMode = true
	t.Cleanup(func() { planDaemonMode = origDaemon })

	cmd := newPlanCmd(deps)
	cmd.Run(cmd, nil)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)

	if len(recorder.Invocations) != 1 {
		t.Fatalf("expected 1 agent invocation in daemon mode, got %d", len(recorder.Invocations))
	}

	lockPath := filepath.Join(tmpDir, LockFileName)
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("expected lock file to exist in daemon mode, got error: %v", err)
	}

	var info LockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("failed to parse lock file: %v", err)
	}
	if info.Command != "plan" {
		t.Errorf("expected lock command 'plan', got %q", info.Command)
	}
}

func TestRunPlan_SkipsEpics(t *testing.T) {
	// not parallel: uses global default tracker, os.Stdout capture

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	deps, _, _, _, _ := NewTestDeps(t)
	setupPlanTracker(t, deps, []BdIssue{
		{ID: "bd-123", Status: "open", IssueType: "epic", Title: "Test epic"},
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
	setupPlanTracker(t, deps, []BdIssue{
		{ID: "bd-123", Status: "in_progress", IssueType: "task", Title: "Test task"},
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
	taskJSON := `[{"id":"bd-1","status":"open","issue_type":"task","design":""}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
		{Name: "bd", Args: []string{"list", "--json", "--limit", "500"}, Stdout: taskJSON},
	})
	mock.Install()

	available, err := HasAvailablePlanningTasks("", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !available {
		t.Error("expected tasks to be available for planning")
	}
}

func TestHasAvailablePlanningTasks_SkipsTasksWithDesign(t *testing.T) {
	// not parallel: uses mock.Install() which mutates global defaultDeps.Exec
	taskJSON := `[{"id":"bd-1","status":"open","issue_type":"task","design":"Some plan here"}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
		{Name: "bd", Args: []string{"list", "--json", "--limit", "500"}, Stdout: taskJSON},
	})
	mock.Install()

	available, err := HasAvailablePlanningTasks("", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if available {
		t.Error("tasks with existing design should not be available for planning")
	}
}

func TestHasAvailablePlanningTasks_BdError(t *testing.T) {
	// not parallel: uses mock.Install() which mutates global defaultDeps.Exec
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Err: os.ErrNotExist},
	})
	mock.Install()

	_, err := HasAvailablePlanningTasks("", "")
	if err == nil {
		t.Error("expected error when bd command fails")
	}
}

// ============================================================================
// GetAvailablePlanningTasks Tests (CommandMock-based)
// ============================================================================

func TestGetAvailablePlanningTasks_Success(t *testing.T) {
	// not parallel: uses mock.Install() which mutates global defaultDeps.Exec
	taskJSON := `[{"id":"bd-1","status":"open","issue_type":"task","design":""}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
		{Name: "bd", Args: []string{"list", "--json", "--limit", "500"}, Stdout: taskJSON},
	})
	mock.Install()

	tasks, err := GetAvailablePlanningTasks("", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].ID != "bd-1" {
		t.Errorf("expected task ID 'bd-1', got %q", tasks[0].ID)
	}
}

func TestGetAvailablePlanningTasks_ReturnsEmpty(t *testing.T) {
	// not parallel: uses mock.Install() which mutates global defaultDeps.Exec
	taskJSON := `[{"id":"bd-1","status":"open","issue_type":"task","design":"Some plan here"}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
		{Name: "bd", Args: []string{"list", "--json", "--limit", "500"}, Stdout: taskJSON},
	})
	mock.Install()

	tasks, err := GetAvailablePlanningTasks("", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestGetAvailablePlanningTasks_BdError(t *testing.T) {
	// not parallel: uses mock.Install() which mutates global defaultDeps.Exec
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Err: os.ErrNotExist},
	})
	mock.Install()

	_, err := GetAvailablePlanningTasks("", "")
	if err == nil {
		t.Error("expected error when bd command fails")
	}
}
