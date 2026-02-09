package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPlan_SingleTask_NoTasksAvailable(t *testing.T) {
	// Setup temp worktree directory
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create .git directory to mark as git repo
	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	// Reset flags
	planAutoMode = false
	planDaemonMode = false

	// Mock bd ready returning empty array (no tasks)
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: "[]"},
	})
	mock.Install()

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runPlan(nil, nil)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Verify "no tasks available" message
	if !strings.Contains(output, "No tasks available for planning") {
		t.Errorf("expected 'No tasks available for planning' in output, got: %s", output)
	}
}

func TestRunPlan_SingleTask_Success(t *testing.T) {
	// Setup temp worktree directory
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create .git directory
	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	// Reset flags
	planAutoMode = false
	planDaemonMode = false

	// Mock bd ready with available task (status=open, no design)
	taskJSON := `[{"id":"bd-123","status":"open","issue_type":"task","title":"Test task"}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
	})
	mock.Install()

	// Mock Claude invoker
	recorder := SetupMockClaudeInvoker(t, nil)

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runPlan(nil, nil)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Verify banner was printed
	if !strings.Contains(output, "Running PLANNING agent") {
		t.Errorf("expected 'Running PLANNING agent' banner in output, got: %s", output)
	}

	// Verify Claude was invoked
	if len(recorder.Invocations) != 1 {
		t.Fatalf("expected 1 Claude invocation, got %d", len(recorder.Invocations))
	}

	// Verify lock was created and released
	_, err := os.Stat(filepath.Join(tmpDir, LockFileName))
	if err == nil {
		t.Error("lock file should be released after runPlan completes")
	}
}

func TestRunPlan_DaemonMode_AcquiresLock(t *testing.T) {
	// Setup temp worktree directory
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create .git directory
	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	// Set daemon mode
	planAutoMode = false
	planDaemonMode = true
	defer func() { planDaemonMode = false }()

	// Mock Claude invoker
	recorder := SetupMockClaudeInvoker(t, nil)

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runPlan(nil, nil)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)

	// Verify Claude was invoked
	if len(recorder.Invocations) != 1 {
		t.Fatalf("expected 1 Claude invocation in daemon mode, got %d", len(recorder.Invocations))
	}

	// In daemon mode, lock is intentionally NOT released (for parent to read)
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
	// Setup temp worktree directory
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create .git directory
	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	// Reset flags
	planAutoMode = false
	planDaemonMode = false

	// Mock bd ready with only an epic (should be skipped)
	taskJSON := `[{"id":"bd-123","status":"open","issue_type":"epic","title":"Test epic"}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
	})
	mock.Install()

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runPlan(nil, nil)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Should say no tasks available because epics are skipped
	if !strings.Contains(output, "No tasks available for planning") {
		t.Errorf("expected 'No tasks available for planning' when only epics exist, got: %s", output)
	}
}

func TestRunPlan_SkipsInProgress(t *testing.T) {
	// Setup temp worktree directory
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create .git directory
	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	// Reset flags
	planAutoMode = false
	planDaemonMode = false

	// Mock bd ready with only in_progress task (should be skipped)
	taskJSON := `[{"id":"bd-123","status":"in_progress","issue_type":"task","title":"Test task"}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
	})
	mock.Install()

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runPlan(nil, nil)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Should say no tasks available because in_progress are skipped
	if !strings.Contains(output, "No tasks available for planning") {
		t.Errorf("expected 'No tasks available for planning' when all tasks in_progress, got: %s", output)
	}
}

func TestHasAvailablePlanningTasks_Success(t *testing.T) {
	// Task with no design and open status is available for planning
	taskJSON := `[{"id":"bd-1","status":"open","issue_type":"task","design":""}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
	})
	mock.Install()

	available, err := HasAvailablePlanningTasks("")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !available {
		t.Error("expected tasks to be available for planning")
	}
}

func TestHasAvailablePlanningTasks_SkipsTasksWithDesign(t *testing.T) {
	// Task with existing design should be skipped
	taskJSON := `[{"id":"bd-1","status":"open","issue_type":"task","design":"Some plan here"}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
	})
	mock.Install()

	available, err := HasAvailablePlanningTasks("")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if available {
		t.Error("tasks with existing design should not be available for planning")
	}
}

func TestHasAvailablePlanningTasks_BdError(t *testing.T) {
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Err: os.ErrNotExist},
	})
	mock.Install()

	_, err := HasAvailablePlanningTasks("")
	if err == nil {
		t.Error("expected error when bd command fails")
	}
}

// ============================================================================
// GetAvailablePlanningTasks Tests (CommandMock-based)
// ============================================================================

func TestGetAvailablePlanningTasks_Success(t *testing.T) {
	taskJSON := `[{"id":"bd-1","status":"open","issue_type":"task","design":""}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
	})
	mock.Install()

	tasks, err := GetAvailablePlanningTasks("")
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
	taskJSON := `[{"id":"bd-1","status":"open","issue_type":"task","design":"Some plan here"}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
	})
	mock.Install()

	tasks, err := GetAvailablePlanningTasks("")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestGetAvailablePlanningTasks_BdError(t *testing.T) {
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Err: os.ErrNotExist},
	})
	mock.Install()

	_, err := GetAvailablePlanningTasks("")
	if err == nil {
		t.Error("expected error when bd command fails")
	}
}
