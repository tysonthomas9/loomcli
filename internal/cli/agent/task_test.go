package agent

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

// testCmdWithDeps creates a minimal cobra.Command with deps injected into context.
func testCmdWithDeps(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{}
	cmd.SetContext(WithDeps(context.Background(), deps))
	return cmd
}

func TestRunTask_SingleTask_NoTasksAvailable(t *testing.T) {
	// not parallel: uses os.Chdir, global taskAutoMode/taskDaemonMode, mock.Install(), os.Stdout capture
	deps, _, _, _, _ := NewTestDeps(t)

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

	// Mock issue-store ready returning empty array (no tasks).
	mock := NewCommandMock(t, []CommandStub{
		{Name: "issue-store", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: "[]"},
	})
	mock.Install()

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runTask(testCmdWithDeps(deps), nil)

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
	// not parallel: uses os.Chdir, global taskAutoMode/taskDaemonMode, mock.Install(), os.Stdout capture
	deps, _, _, _, _ := NewTestDeps(t)

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

	// Mock issue-store ready with available task (status=open, has design, no needs-revision label).
	taskJSON := `[{"id":"loom-123","status":"open","issue_type":"task","title":"Test task","design":"Implementation plan here"}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "issue-store", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
		{Name: "git", Args: []string{"rev-parse", "HEAD"}, Stdout: "abc123\n"},
		{Name: "git", Args: []string{"diff", "--numstat", "abc123..HEAD"}, Stdout: ""},
	})
	mock.Install()

	// Mock Claude invoker on deps
	recorder := SetupMockAgentInvokerOn(t, deps, nil)

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runTask(testCmdWithDeps(deps), nil)

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
	if len(recorder.InteractiveCalls) != 1 {
		t.Fatalf("expected 1 Claude invocation, got %d", len(recorder.InteractiveCalls))
	}

	// Verify lock was created and released
	_, err := os.Stat(filepath.Join(tmpDir, LockFileName))
	if err == nil {
		t.Error("lock file should be released after runTask completes")
	}
}

func TestRunTask_DaemonMode_AcquiresLock(t *testing.T) {
	// not parallel: uses os.Chdir, global taskDaemonMode, os.Stdout capture
	deps, _, _, _, _ := NewTestDeps(t)

	// Setup temp worktree directory
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)
	origDir, _ := os.Getwd()
	os.Chdir(tmpDir)
	t.Cleanup(func() { os.Chdir(origDir) })

	// Create .git directory
	os.MkdirAll(filepath.Join(tmpDir, ".git"), 0755)

	// Set daemon mode
	taskAutoMode = false
	taskDaemonMode = true
	defer func() { taskDaemonMode = false }()

	// Mock Claude invoker on deps
	recorder := SetupMockAgentInvokerOn(t, deps, nil)

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runTask(testCmdWithDeps(deps), nil)

	w.Close()
	os.Stdout = oldStdout
	var buf bytes.Buffer
	buf.ReadFrom(r)

	// Daemon mode routes through the wrapper-backed non-interactive path so
	// the supervisor watchdog sees per-turn stream output (see runTaskDaemon).
	if len(recorder.NonInteractiveCalls) != 1 {
		t.Fatalf("expected 1 Claude non-interactive invocation in daemon mode, got %d", len(recorder.NonInteractiveCalls))
	}
	if len(recorder.InteractiveCalls) != 0 {
		t.Fatalf("daemon mode must not invoke the interactive path, got %d calls", len(recorder.InteractiveCalls))
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
	// not parallel: uses os.Chdir, global taskAutoMode/taskDaemonMode, mock.Install(), os.Stdout capture
	deps, _, _, _, _ := NewTestDeps(t)

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

	// Mock issue-store ready with only an epic (should be skipped).
	taskJSON := `[{"id":"loom-123","status":"open","issue_type":"epic","title":"Test epic","design":"Some design"}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "issue-store", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
	})
	mock.Install()

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runTask(testCmdWithDeps(deps), nil)

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
	// not parallel: uses os.Chdir, global taskAutoMode/taskDaemonMode, mock.Install(), os.Stdout capture
	deps, _, _, _, _ := NewTestDeps(t)

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

	// Mock issue-store ready with task that has no design.
	taskJSON := `[{"id":"loom-123","status":"open","issue_type":"task","title":"Test task","design":""}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "issue-store", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
	})
	mock.Install()

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runTask(testCmdWithDeps(deps), nil)

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
	// not parallel: uses os.Chdir, global taskAutoMode/taskDaemonMode, mock.Install(), os.Stdout capture
	deps, _, _, _, _ := NewTestDeps(t)

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

	// Mock issue-store ready with task that has needs-revision label.
	taskJSON := `[{"id":"loom-123","status":"open","issue_type":"task","title":"Test task","design":"Some plan","labels":["needs-revision"]}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "issue-store", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
	})
	mock.Install()

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runTask(testCmdWithDeps(deps), nil)

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
	// not parallel: uses mock.Install() which mutates global defaultDeps.Exec
	// Task with design and no needs-revision is available for implementation
	taskJSON := `[{"id":"loom-1","status":"open","issue_type":"task","design":"Implementation plan"}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "issue-store", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
	})
	mock.Install()

	available, err := HasAvailableImplementationTasks("", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !available {
		t.Error("expected tasks to be available for implementation")
	}
}

func TestHasAvailableImplementationTasks_SkipsTasksWithoutDesign(t *testing.T) {
	// not parallel: uses mock.Install() which mutates global defaultDeps.Exec
	// Task without design should be skipped
	taskJSON := `[{"id":"loom-1","status":"open","issue_type":"task","design":""}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "issue-store", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
	})
	mock.Install()

	available, err := HasAvailableImplementationTasks("", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if available {
		t.Error("tasks without design should not be available for implementation")
	}
}

func TestHasAvailableImplementationTasks_SkipsNeedsRevision(t *testing.T) {
	// not parallel: uses mock.Install() which mutates global defaultDeps.Exec
	// Task with needs-revision label should be skipped
	taskJSON := `[{"id":"loom-1","status":"open","issue_type":"task","design":"Some plan","labels":["needs-revision"]}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "issue-store", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
	})
	mock.Install()

	available, err := HasAvailableImplementationTasks("", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if available {
		t.Error("tasks with needs-revision label should not be available")
	}
}

func TestHasAvailableImplementationTasks_ReadyError(t *testing.T) {
	// not parallel: uses mock.Install() which mutates global defaultDeps.Exec
	mock := NewCommandMock(t, []CommandStub{
		{Name: "issue-store", Args: []string{"ready", "--json", "--limit", "100"}, Err: os.ErrNotExist},
	})
	mock.Install()

	_, err := HasAvailableImplementationTasks("", "")
	if err == nil {
		t.Error("expected error when issue-store command fails")
	}
}

// ============================================================================
// GetAvailableImplementationTasks Tests (CommandMock-based)
// ============================================================================

func TestGetAvailableImplementationTasks_Success(t *testing.T) {
	// not parallel: uses mock.Install() which mutates global defaultDeps.Exec
	taskJSON := `[{"id":"loom-1","status":"open","issue_type":"task","design":"Implementation plan"}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "issue-store", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
	})
	mock.Install()

	tasks, err := GetAvailableImplementationTasks("", "")
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

func TestGetAvailableImplementationTasks_ReturnsEmpty(t *testing.T) {
	// not parallel: uses mock.Install() which mutates global defaultDeps.Exec
	taskJSON := `[{"id":"loom-1","status":"open","issue_type":"task","design":""}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "issue-store", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
	})
	mock.Install()

	tasks, err := GetAvailableImplementationTasks("", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestGetAvailableImplementationTasks_SkipsNeedsRevision(t *testing.T) {
	// not parallel: uses mock.Install() which mutates global defaultDeps.Exec
	taskJSON := `[{"id":"loom-1","status":"open","issue_type":"task","design":"Some plan","labels":["needs-revision"]}]`
	mock := NewCommandMock(t, []CommandStub{
		{Name: "issue-store", Args: []string{"ready", "--json", "--limit", "100"}, Stdout: taskJSON},
	})
	mock.Install()

	tasks, err := GetAvailableImplementationTasks("", "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(tasks))
	}
}

func TestGetAvailableImplementationTasks_ReadyError(t *testing.T) {
	// not parallel: uses mock.Install() which mutates global defaultDeps.Exec
	mock := NewCommandMock(t, []CommandStub{
		{Name: "issue-store", Args: []string{"ready", "--json", "--limit", "100"}, Err: os.ErrNotExist},
	})
	mock.Install()

	_, err := GetAvailableImplementationTasks("", "")
	if err == nil {
		t.Error("expected error when issue-store command fails")
	}
}
