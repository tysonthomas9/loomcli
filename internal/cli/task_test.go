package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunTask_SingleTask_NoTasksAvailable(t *testing.T) {
	// Setup temp worktree directory
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create .git directory to mark as git repo
	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	// Reset flags
	taskAutoMode = false
	taskDaemonMode = false

	// Mock bd ready returning empty array (no tasks)
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: "[]"},
		{Name: "bd", Args: []string{"list", "--json", "--limit", "500"}, Stdout: "[]"},
	})
	mock.Install()

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runTask(nil, nil)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Verify "no tasks available" message
	if !strings.Contains(output, "No tasks available for implementation") {
		t.Errorf("expected 'No tasks available for implementation' in output, got: %s", output)
	}
}

func TestRunTask_SingleTask_Success(t *testing.T) {
	// Setup temp worktree directory
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create .git directory
	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	// Reset flags
	taskAutoMode = false
	taskDaemonMode = false

	// Mock bd ready with available task (status=open, has design, no needs-revision label)
	taskJSON := `[{"id":"bd-123","status":"open","issue_type":"task","title":"Test task","design":"Implementation plan here"}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
		{Name: "bd", Args: []string{"list", "--json", "--limit", "500"}, Stdout: taskJSON},
	})
	mock.Install()

	// Mock Claude invoker
	recorder := SetupMockClaudeInvoker(t, nil)

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runTask(nil, nil)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Verify banner was printed
	if !strings.Contains(output, "Running IMPLEMENTATION agent") {
		t.Errorf("expected 'Running IMPLEMENTATION agent' banner in output, got: %s", output)
	}

	// Verify Claude was invoked
	if len(recorder.Invocations) != 1 {
		t.Fatalf("expected 1 Claude invocation, got %d", len(recorder.Invocations))
	}

	// Verify lock was created and released
	_, err := os.Stat(filepath.Join(tmpDir, LockFileName))
	if err == nil {
		t.Error("lock file should be released after runTask completes")
	}
}

func TestRunTask_DaemonMode_AcquiresLock(t *testing.T) {
	// Setup temp worktree directory
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Isolate from real ~/.loom/config.yaml so NewResolver() falls back to
	// legacy mode and ResolveAgentTarget("") returns tmpDir.
	t.Setenv("LOOM_CONFIG_DIR", filepath.Join(tmpDir, "no-config"))
	oldResolver := defaultResolver
	defaultResolver = NewResolverFromConfig(nil)
	t.Cleanup(func() { defaultResolver = oldResolver })

	// Create .git directory
	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	// Set daemon mode
	taskAutoMode = false
	taskDaemonMode = true
	defer func() { taskDaemonMode = false }()

	// Mock Claude invoker
	recorder := SetupMockClaudeInvoker(t, nil)

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runTask(nil, nil)

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
	if info.Command != "task" {
		t.Errorf("expected lock command 'task', got %q", info.Command)
	}
}

func TestRunTask_SkipsEpics(t *testing.T) {
	// Setup temp worktree directory
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create .git directory
	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	// Reset flags
	taskAutoMode = false
	taskDaemonMode = false

	// Mock bd ready with only an epic (should be skipped)
	taskJSON := `[{"id":"bd-123","status":"open","issue_type":"epic","title":"Test epic","design":"Some design"}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
		{Name: "bd", Args: []string{"list", "--json", "--limit", "500"}, Stdout: taskJSON},
	})
	mock.Install()

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runTask(nil, nil)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Should say no tasks available because epics are skipped
	if !strings.Contains(output, "No tasks available for implementation") {
		t.Errorf("expected 'No tasks available for implementation' when only epics exist, got: %s", output)
	}
}

func TestRunTask_SkipsTasksWithoutDesign(t *testing.T) {
	// Setup temp worktree directory
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create .git directory
	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	// Reset flags
	taskAutoMode = false
	taskDaemonMode = false

	// Mock bd ready with task that has no design
	taskJSON := `[{"id":"bd-123","status":"open","issue_type":"task","title":"Test task","design":""}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
		{Name: "bd", Args: []string{"list", "--json", "--limit", "500"}, Stdout: taskJSON},
	})
	mock.Install()

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runTask(nil, nil)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Should say no tasks available because task has no design
	if !strings.Contains(output, "No tasks available for implementation") {
		t.Errorf("expected 'No tasks available for implementation' when task has no design, got: %s", output)
	}
}

func TestRunTask_SkipsTasksWithNeedsRevision(t *testing.T) {
	// Setup temp worktree directory
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create .git directory
	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	// Reset flags
	taskAutoMode = false
	taskDaemonMode = false

	// Mock bd ready with task that has needs-revision label
	taskJSON := `[{"id":"bd-123","status":"open","issue_type":"task","title":"Test task","design":"Some plan","labels":["needs-revision"]}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
		{Name: "bd", Args: []string{"list", "--json", "--limit", "500"}, Stdout: taskJSON},
	})
	mock.Install()

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runTask(nil, nil)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)
	output := buf.String()

	// Should say no tasks available because task has needs-revision
	if !strings.Contains(output, "No tasks available for implementation") {
		t.Errorf("expected 'No tasks available for implementation' when task has needs-revision, got: %s", output)
	}
}

func TestHasAvailableImplementationTasks_Success(t *testing.T) {
	// Task with design and no needs-revision is available for implementation
	taskJSON := `[{"id":"bd-1","status":"open","issue_type":"task","design":"Implementation plan"}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
		{Name: "bd", Args: []string{"list", "--json", "--limit", "500"}, Stdout: taskJSON},
	})
	mock.Install()

	available, err := HasAvailableImplementationTasks("")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !available {
		t.Error("expected tasks to be available for implementation")
	}
}

func TestHasAvailableImplementationTasks_SkipsTasksWithoutDesign(t *testing.T) {
	// Task without design should be skipped
	taskJSON := `[{"id":"bd-1","status":"open","issue_type":"task","design":""}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
		{Name: "bd", Args: []string{"list", "--json", "--limit", "500"}, Stdout: taskJSON},
	})
	mock.Install()

	available, err := HasAvailableImplementationTasks("")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if available {
		t.Error("tasks without design should not be available for implementation")
	}
}

func TestHasAvailableImplementationTasks_SkipsNeedsRevision(t *testing.T) {
	// Task with needs-revision label should be skipped
	taskJSON := `[{"id":"bd-1","status":"open","issue_type":"task","design":"Some plan","labels":["needs-revision"]}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
		{Name: "bd", Args: []string{"list", "--json", "--limit", "500"}, Stdout: taskJSON},
	})
	mock.Install()

	available, err := HasAvailableImplementationTasks("")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if available {
		t.Error("tasks with needs-revision label should not be available")
	}
}

func TestHasAvailableImplementationTasks_BdError(t *testing.T) {
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Err: os.ErrNotExist},
	})
	mock.Install()

	_, err := HasAvailableImplementationTasks("")
	if err == nil {
		t.Error("expected error when bd command fails")
	}
}

// ============================================================================
// GetAvailableImplementationTasks Tests (CommandMock-based)
// ============================================================================

func TestGetAvailableImplementationTasks_Success(t *testing.T) {
	taskJSON := `[{"id":"bd-1","status":"open","issue_type":"task","design":"Implementation plan"}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
		{Name: "bd", Args: []string{"list", "--json", "--limit", "500"}, Stdout: taskJSON},
	})
	mock.Install()

	tasks, err := GetAvailableImplementationTasks("")
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

func TestGetAvailableImplementationTasks_ReturnsEmpty(t *testing.T) {
	taskJSON := `[{"id":"bd-1","status":"open","issue_type":"task","design":""}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
		{Name: "bd", Args: []string{"list", "--json", "--limit", "500"}, Stdout: taskJSON},
	})
	mock.Install()

	tasks, err := GetAvailableImplementationTasks("")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestGetAvailableImplementationTasks_SkipsNeedsRevision(t *testing.T) {
	taskJSON := `[{"id":"bd-1","status":"open","issue_type":"task","design":"Some plan","labels":["needs-revision"]}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
		{Name: "bd", Args: []string{"list", "--json", "--limit", "500"}, Stdout: taskJSON},
	})
	mock.Install()

	tasks, err := GetAvailableImplementationTasks("")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestGetAvailableImplementationTasks_BdError(t *testing.T) {
	mock := NewCommandMock(t, []CommandStub{
		{Name: "bd", Args: []string{"ready", "--json", "--limit", "100"}, Err: os.ErrNotExist},
	})
	mock.Install()

	_, err := GetAvailableImplementationTasks("")
	if err == nil {
		t.Error("expected error when bd command fails")
	}
}
