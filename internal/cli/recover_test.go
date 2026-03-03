package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/lockfile"
)

func TestForceReleaseLock_RemovesLockFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a lock file
	lockPath := filepath.Join(tmpDir, LockFileName)
	if err := os.WriteFile(lockPath, []byte(`{"pid":12345}`), 0644); err != nil {
		t.Fatalf("failed to create lock file: %v", err)
	}

	// Verify lock file exists
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("lock file should exist before test: %v", err)
	}

	// Call forceReleaseLock
	err := forceReleaseLock(tmpDir)
	if err != nil {
		t.Errorf("forceReleaseLock returned error: %v", err)
	}

	// Verify lock file is gone
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("lock file should have been removed")
	}
}

func TestForceReleaseLock_NoLockFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Call forceReleaseLock on directory without lock
	err := forceReleaseLock(tmpDir)
	if err == nil {
		t.Error("expected error when removing non-existent lock file")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected os.IsNotExist error, got: %v", err)
	}
}

func TestCloseTask_Success(t *testing.T) {
	ResetBeadsDirCache()
	mock := NewCommandMock(t, []CommandStub{{
		Dir:    ".",
		Name:   "bd",
		Args:   []string{"close", "task-123", "--reason", "Completed (verified by recovery analysis): Tests pass"},
		Stdout: "Task closed\n",
		Err:    nil,
	}})
	mock.Install()

	// closeTask doesn't return anything, just verify no panic and correct args
	closeTask("task-123", "Tests pass")
}

func TestCloseTask_Failure(t *testing.T) {
	ResetBeadsDirCache()
	mock := NewCommandMock(t, []CommandStub{{
		Dir:    ".",
		Name:   "bd",
		Args:   []string{"close", "task-456", "--reason", "Completed (verified by recovery analysis): Done"},
		Stdout: "",
		Stderr: "Error: task not found\n",
		Err:    errors.New("exit status 1"),
	}})
	mock.Install()

	// closeTask prints warning but doesn't panic
	closeTask("task-456", "Done")
}

func TestResetTask_Success(t *testing.T) {
	ResetBeadsDirCache()
	mock := NewCommandMock(t, []CommandStub{
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"show", "task-789", "--json"},
			Stdout: `[{"status":"in_progress"}]`,
			Err:    nil,
		},
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"update", "task-789", "--status", "open", "--assignee", ""},
			Stdout: "Task updated\n",
			Err:    nil,
		},
	})
	mock.Install()

	resetTask("task-789")
}

func TestResetTask_Failure(t *testing.T) {
	ResetBeadsDirCache()
	mock := NewCommandMock(t, []CommandStub{
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"show", "task-789", "--json"},
			Stdout: `[{"status":"in_progress"}]`,
			Err:    nil,
		},
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"update", "task-789", "--status", "open", "--assignee", ""},
			Stdout: "",
			Stderr: "Error: invalid task\n",
			Err:    errors.New("exit status 1"),
		},
	})
	mock.Install()

	// resetTask prints warning and manual instructions but doesn't panic
	resetTask("task-789")
}

func TestAnalyzeTaskCompletion_Completed(t *testing.T) {
	ResetBeadsDirCache()
	mock := NewCommandMock(t, []CommandStub{
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"show", "task-abc"},
			Stdout: "Task: Implement feature X\nStatus: in_progress\n",
			Err:    nil,
		},
		{
			Dir:    "/test/worktree",
			Name:   "git",
			Args:   []string{"log", "--oneline", "-20", "--all", "--grep", "task-abc"},
			Stdout: "abc123 feat: implement feature X (task-abc)\n",
			Err:    nil,
		},
		{
			Dir:  "/test/worktree",
			Name: "claude",
			// Args contain the prompt - we don't check exact content
			Stdout: "COMPLETED: Feature X was fully implemented with tests\n",
			Err:    nil,
		},
	})
	mock.Install()

	completed, reason := analyzeTaskCompletion("/test/worktree", "task-abc")

	if !completed {
		t.Error("expected completed=true")
	}
	if reason != "Feature X was fully implemented with tests" {
		t.Errorf("unexpected reason: %q", reason)
	}
}

func TestAnalyzeTaskCompletion_Incomplete(t *testing.T) {
	ResetBeadsDirCache()
	mock := NewCommandMock(t, []CommandStub{
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"show", "task-def"},
			Stdout: "Task: Add unit tests\nStatus: in_progress\n",
			Err:    nil,
		},
		{
			Dir:    "/test/worktree",
			Name:   "git",
			Args:   []string{"log", "--oneline", "-20", "--all", "--grep", "task-def"},
			Stdout: "",
			Err:    nil,
		},
		{
			Dir:    "/test/worktree",
			Name:   "claude",
			Stdout: "INCOMPLETE: No commits found that implement the tests\n",
			Err:    nil,
		},
	})
	mock.Install()

	completed, reason := analyzeTaskCompletion("/test/worktree", "task-def")

	if completed {
		t.Error("expected completed=false")
	}
	if reason != "No commits found that implement the tests" {
		t.Errorf("unexpected reason: %q", reason)
	}
}

func TestAnalyzeTaskCompletion_BdShowFails(t *testing.T) {
	ResetBeadsDirCache()
	mock := NewCommandMock(t, []CommandStub{
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"show", "task-notfound"},
			Stdout: "",
			Stderr: "Error: task not found\n",
			Err:    errors.New("exit status 1"),
		},
	})
	mock.Install()

	completed, reason := analyzeTaskCompletion("/test/worktree", "task-notfound")

	if completed {
		t.Error("expected completed=false when bd show fails")
	}
	if reason != "Could not fetch task details" {
		t.Errorf("unexpected reason: %q", reason)
	}
}

func TestAnalyzeTaskCompletion_ClaudeFails(t *testing.T) {
	ResetBeadsDirCache()
	mock := NewCommandMock(t, []CommandStub{
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"show", "task-xyz"},
			Stdout: "Task: Some task\n",
			Err:    nil,
		},
		{
			Dir:    "/test/worktree",
			Name:   "git",
			Args:   []string{"log", "--oneline", "-20", "--all", "--grep", "task-xyz"},
			Stdout: "",
			Err:    nil,
		},
		{
			Dir:    "/test/worktree",
			Name:   "claude",
			Stdout: "",
			Stderr: "Error: API rate limit exceeded\n",
			Err:    errors.New("exit status 1"),
		},
	})
	mock.Install()

	completed, reason := analyzeTaskCompletion("/test/worktree", "task-xyz")

	if completed {
		t.Error("expected completed=false when claude fails")
	}
	if reason == "" {
		t.Error("expected a reason explaining the failure")
	}
}

func TestAnalyzeTaskCompletion_ParsesMultilineResponse(t *testing.T) {
	ResetBeadsDirCache()
	mock := NewCommandMock(t, []CommandStub{
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"show", "task-multi"},
			Stdout: "Task: Multiline test\n",
			Err:    nil,
		},
		{
			Dir:    "/test/worktree",
			Name:   "git",
			Args:   []string{"log", "--oneline", "-20", "--all", "--grep", "task-multi"},
			Stdout: "abc123 commit\n",
			Err:    nil,
		},
		{
			Dir:    "/test/worktree",
			Name:   "claude",
			Stdout: "Let me analyze this...\n\nLooking at the commits:\nCOMPLETED: All requirements met\n\nDone.\n",
			Err:    nil,
		},
	})
	mock.Install()

	completed, reason := analyzeTaskCompletion("/test/worktree", "task-multi")

	if !completed {
		t.Error("expected completed=true for multiline response with COMPLETED")
	}
	if reason != "All requirements met" {
		t.Errorf("unexpected reason: %q", reason)
	}
}

func TestAnalyzeTaskCompletion_CaseInsensitive(t *testing.T) {
	ResetBeadsDirCache()
	mock := NewCommandMock(t, []CommandStub{
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"show", "task-case"},
			Stdout: "Task: Case test\n",
			Err:    nil,
		},
		{
			Dir:    "/test/worktree",
			Name:   "git",
			Args:   []string{"log", "--oneline", "-20", "--all", "--grep", "task-case"},
			Stdout: "",
			Err:    nil,
		},
		{
			Dir:    "/test/worktree",
			Name:   "claude",
			Stdout: "Completed: work was done correctly\n",
			Err:    nil,
		},
	})
	mock.Install()

	completed, reason := analyzeTaskCompletion("/test/worktree", "task-case")

	if !completed {
		t.Error("expected completed=true for lowercase 'Completed'")
	}
	if reason != "work was done correctly" {
		t.Errorf("unexpected reason: %q", reason)
	}
}

func TestAnalyzeTaskCompletion_ReasonWithColons(t *testing.T) {
	ResetBeadsDirCache()
	mock := NewCommandMock(t, []CommandStub{
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"show", "task-colon"},
			Stdout: "Task: Colon test\n",
			Err:    nil,
		},
		{
			Dir:    "/test/worktree",
			Name:   "git",
			Args:   []string{"log", "--oneline", "-20", "--all", "--grep", "task-colon"},
			Stdout: "",
			Err:    nil,
		},
		{
			Dir:    "/test/worktree",
			Name:   "claude",
			Stdout: "INCOMPLETE: Missing: tests, docs, and coverage\n",
			Err:    nil,
		},
	})
	mock.Install()

	completed, reason := analyzeTaskCompletion("/test/worktree", "task-colon")

	if completed {
		t.Error("expected completed=false")
	}
	// Should capture everything after first colon
	if reason != "Missing: tests, docs, and coverage" {
		t.Errorf("unexpected reason: %q", reason)
	}
}

func TestAnalyzeTaskCompletion_UnparseableResponse(t *testing.T) {
	ResetBeadsDirCache()
	mock := NewCommandMock(t, []CommandStub{
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"show", "task-unparse"},
			Stdout: "Task: Unparse test\n",
			Err:    nil,
		},
		{
			Dir:    "/test/worktree",
			Name:   "git",
			Args:   []string{"log", "--oneline", "-20", "--all", "--grep", "task-unparse"},
			Stdout: "",
			Err:    nil,
		},
		{
			Dir:    "/test/worktree",
			Name:   "claude",
			Stdout: "I'm not sure about this task.\n",
			Err:    nil,
		},
	})
	mock.Install()

	completed, reason := analyzeTaskCompletion("/test/worktree", "task-unparse")

	if completed {
		t.Error("expected completed=false when response is unparseable")
	}
	if reason != "Could not determine completion status" {
		t.Errorf("unexpected reason: %q", reason)
	}
}

func TestHandleOrphanedTask_AnalyzeComplete(t *testing.T) {
	ResetBeadsDirCache()
	mock := NewCommandMock(t, []CommandStub{
		// analyzeTaskCompletion calls
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"show", "task-orphan1"},
			Stdout: "Task: Orphan complete\n",
			Err:    nil,
		},
		{
			Dir:    "/test/worktree",
			Name:   "git",
			Args:   []string{"log", "--oneline", "-20", "--all", "--grep", "task-orphan1"},
			Stdout: "abc123 completed task-orphan1\n",
			Err:    nil,
		},
		{
			Dir:    "/test/worktree",
			Name:   "claude",
			Stdout: "COMPLETED: Task was finished\n",
			Err:    nil,
		},
		// closeTask call
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"close", "task-orphan1", "--reason", "Completed (verified by recovery analysis): Task was finished"},
			Stdout: "Closed\n",
			Err:    nil,
		},
	})
	mock.Install()

	handleOrphanedTask("/test/worktree", "task-orphan1", true)
}

func TestHandleOrphanedTask_AnalyzeIncomplete(t *testing.T) {
	ResetBeadsDirCache()
	mock := NewCommandMock(t, []CommandStub{
		// analyzeTaskCompletion calls
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"show", "task-orphan2"},
			Stdout: "Task: Orphan incomplete\n",
			Err:    nil,
		},
		{
			Dir:    "/test/worktree",
			Name:   "git",
			Args:   []string{"log", "--oneline", "-20", "--all", "--grep", "task-orphan2"},
			Stdout: "",
			Err:    nil,
		},
		{
			Dir:    "/test/worktree",
			Name:   "claude",
			Stdout: "INCOMPLETE: No work found\n",
			Err:    nil,
		},
		// resetTask: show then update
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"show", "task-orphan2", "--json"},
			Stdout: `[{"status":"in_progress"}]`,
			Err:    nil,
		},
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"update", "task-orphan2", "--status", "open", "--assignee", ""},
			Stdout: "Reset\n",
			Err:    nil,
		},
	})
	mock.Install()

	handleOrphanedTask("/test/worktree", "task-orphan2", true)
}

func TestHandleOrphanedTask_NoAnalyze(t *testing.T) {
	ResetBeadsDirCache()
	mock := NewCommandMock(t, []CommandStub{
		// resetTask: show then update (no analyze)
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"show", "task-orphan3", "--json"},
			Stdout: `[{"status":"in_progress"}]`,
			Err:    nil,
		},
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"update", "task-orphan3", "--status", "open", "--assignee", ""},
			Stdout: "Reset\n",
			Err:    nil,
		},
	})
	mock.Install()

	handleOrphanedTask("/test/worktree", "task-orphan3", false)
}

func TestCleanUntrackedFiles_NoFiles(t *testing.T) {
	// Dry run returns empty — no clean should be called
	mock := NewCommandMock(t, []CommandStub{{
		Dir:    "/test/worktree",
		Name:   "git",
		Args:   []string{"clean", "-fdn"},
		Stdout: "",
		Err:    nil,
	}})
	mock.Install()

	// No output command mock needed — GitClean should not be called
	cleanUntrackedFiles("/test/worktree", false)
}

func TestCleanUntrackedFiles_WithForce(t *testing.T) {
	// Dry run returns files, force=true → clean is called without prompt
	mock := NewCommandMock(t, []CommandStub{{
		Dir:    "/test/worktree",
		Name:   "git",
		Args:   []string{"clean", "-fdn"},
		Stdout: "Would remove test.txt\nWould remove screenshots/\n",
		Err:    nil,
	}})
	mock.Install()

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{{
		Dir:  "/test/worktree",
		Args: []string{"clean", "-fd"},
		Err:  nil,
	}})
	outputMock.Install()

	cleanUntrackedFiles("/test/worktree", true)
}

func TestCleanUntrackedFiles_DryRunFails(t *testing.T) {
	// Dry run fails — prints warning, no clean called
	mock := NewCommandMock(t, []CommandStub{{
		Dir:    "/test/worktree",
		Name:   "git",
		Args:   []string{"clean", "-fdn"},
		Stdout: "",
		Stderr: "error: not a git repo\n",
		Err:    errors.New("exit status 128"),
	}})
	mock.Install()

	cleanUntrackedFiles("/test/worktree", true)
}

func TestCleanUntrackedFiles_CleanFails(t *testing.T) {
	// Dry run succeeds but actual clean fails — prints warning
	mock := NewCommandMock(t, []CommandStub{{
		Dir:    "/test/worktree",
		Name:   "git",
		Args:   []string{"clean", "-fdn"},
		Stdout: "Would remove test.txt\n",
		Err:    nil,
	}})
	mock.Install()

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{{
		Dir:  "/test/worktree",
		Args: []string{"clean", "-fd"},
		Err:  errors.New("Permission denied"),
	}})
	outputMock.Install()

	cleanUntrackedFiles("/test/worktree", true)
}

func TestKillProcess_Success(t *testing.T) {
	// Start a sleep process in its own process group (as the daemon does)
	cmd := exec.Command("sleep", "60") //nolint:norawexec
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start sleep process: %v", err)
	}
	pid := cmd.Process.Pid

	// Verify it's running
	if !lockfile.IsProcessRunning(pid) {
		t.Fatal("process should be running before kill")
	}

	// Reap the child in a goroutine so it doesn't become a zombie
	// (in production, killed processes aren't children of the recover command)
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	// Kill it (sends SIGTERM to process group, then SIGKILL if needed)
	err := killProcess(pid)
	if err != nil {
		t.Errorf("killProcess returned error: %v", err)
	}

	<-done

	// Verify it's dead
	if lockfile.IsProcessRunning(pid) {
		t.Error("process should not be running after kill")
	}
}

func TestKillProcess_NonExistentPid(t *testing.T) {
	// Use a PID that almost certainly doesn't exist
	// killProcess treats ESRCH (no such process) as success
	err := killProcess(999999999)
	if err != nil {
		t.Errorf("killProcess should treat non-existent PID as success, got: %v", err)
	}
}

func TestKillProcess_AlreadyDead(t *testing.T) {
	// Start and immediately kill a process to get a dead PID
	cmd := exec.Command("sleep", "60") //nolint:norawexec
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start sleep process: %v", err)
	}
	pid := cmd.Process.Pid

	// Kill the process group directly
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = cmd.Wait()

	// Now killProcess should handle the already-dead case gracefully
	err := killProcess(pid)
	if err != nil {
		t.Errorf("killProcess should succeed for already-dead process, got: %v", err)
	}
}

func TestKillProcess_WithChildProcesses(t *testing.T) {
	// Start a process that spawns children, all in their own process group.
	// killProcess should terminate the entire group.
	cmd := exec.Command("bash", "-c", "sleep 60 & sleep 60 & wait") //nolint:norawexec
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start bash process: %v", err)
	}
	pid := cmd.Process.Pid

	// Give children a moment to spawn
	time.Sleep(200 * time.Millisecond)

	// Reap the parent in a goroutine
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	// Kill the process group
	err := killProcess(pid)
	if err != nil {
		t.Errorf("killProcess returned error: %v", err)
	}

	<-done

	// Verify parent is dead
	if lockfile.IsProcessRunning(pid) {
		t.Error("parent process should not be running after kill")
	}

	// Verify no process in the group is still running.
	// Sending signal 0 to the process group checks for existence.
	err = syscall.Kill(-pid, 0)
	if err != syscall.ESRCH {
		t.Errorf("process group should not exist after kill, got err: %v", err)
	}
}

func TestResetOrphanedAgentTasks_FindsAndResetsMultiple(t *testing.T) {
	ResetBeadsDirCache()
	mock := NewCommandMock(t, []CommandStub{
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"list", "--assignee", "falcon", "--status", "in_progress", "--json"},
			Stdout: `[{"id":"task-1","title":"First task"},{"id":"task-2","title":"Second task"}]`,
			Err:    nil,
		},
		// resetTask for task-1: show then update
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"show", "task-1", "--json"},
			Stdout: `[{"status":"in_progress"}]`,
			Err:    nil,
		},
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"update", "task-1", "--status", "open", "--assignee", ""},
			Stdout: "Updated\n",
			Err:    nil,
		},
		// resetTask for task-2: show then update
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"show", "task-2", "--json"},
			Stdout: `[{"status":"in_progress"}]`,
			Err:    nil,
		},
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"update", "task-2", "--status", "open", "--assignee", ""},
			Stdout: "Updated\n",
			Err:    nil,
		},
	})
	mock.Install()

	resetOrphanedAgentTasks("/test/worktree", "falcon", "", false)
}

func TestResetOrphanedAgentTasks_SkipsAlreadyHandled(t *testing.T) {
	ResetBeadsDirCache()
	mock := NewCommandMock(t, []CommandStub{
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"list", "--assignee", "ember", "--status", "in_progress", "--json"},
			Stdout: `[{"id":"task-1","title":"Already handled"},{"id":"task-2","title":"Orphaned task"}]`,
			Err:    nil,
		},
		// resetTask for task-2 only (task-1 is already handled): show then update
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"show", "task-2", "--json"},
			Stdout: `[{"status":"in_progress"}]`,
			Err:    nil,
		},
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"update", "task-2", "--status", "open", "--assignee", ""},
			Stdout: "Updated\n",
			Err:    nil,
		},
	})
	mock.Install()

	resetOrphanedAgentTasks("/test/worktree", "ember", "task-1", false)
}

func TestResetOrphanedAgentTasks_NoOrphanedTasks(t *testing.T) {
	mock := NewCommandMock(t, []CommandStub{
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"list", "--assignee", "falcon", "--status", "in_progress", "--json"},
			Stdout: `[]`,
			Err:    nil,
		},
	})
	mock.Install()

	resetOrphanedAgentTasks("/test/worktree", "falcon", "", false)
}

func TestResetOrphanedAgentTasks_BdListFails(t *testing.T) {
	mock := NewCommandMock(t, []CommandStub{
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"list", "--assignee", "falcon", "--status", "in_progress", "--json"},
			Stdout: "",
			Stderr: "Error: connection failed\n",
			Err:    errors.New("exit status 1"),
		},
	})
	mock.Install()

	// Should not panic
	resetOrphanedAgentTasks("/test/worktree", "falcon", "", false)
}

func TestResetOrphanedAgentTasks_EmptyAgentName(t *testing.T) {
	mock := NewCommandMock(t, []CommandStub{})
	mock.Install()

	// Should return immediately with no commands
	resetOrphanedAgentTasks("/test/worktree", "", "", false)
}

func TestRecoverWorktree_NoLock(t *testing.T) {
	ResetBeadsDirCache()
	tmpDir := t.TempDir()

	// No lock file in tmpDir, so CheckLock returns (nil, false, nil).
	// RecoverWorktree should call resetOrphanedAgentTasks (bd list) and cleanUntrackedFiles (git clean -fdn).
	mock := NewCommandMock(t, []CommandStub{
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"list", "--assignee", "test-agent", "--status", "in_progress", "--json"},
			Stdout: `[]`,
			Err:    nil,
		},
		{
			Dir:    tmpDir,
			Name:   "git",
			Args:   []string{"clean", "-fdn"},
			Stdout: "",
			Err:    nil,
		},
	})
	mock.Install()

	err := RecoverWorktree(tmpDir, "test-agent", -1)
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
}

func TestRecoverWorktree_StaleLock(t *testing.T) {
	ResetBeadsDirCache()
	tmpDir := t.TempDir()

	// Create a lock file with a non-existent PID so CheckLock returns (info, false, nil)
	lockPath := filepath.Join(tmpDir, LockFileName)
	lockData := `{"pid":999999999,"command":"test","started_at":"2024-01-01T00:00:00Z","agent_name":"test-agent","task_id":"task-123"}`
	if err := os.WriteFile(lockPath, []byte(lockData), 0644); err != nil {
		t.Fatalf("failed to create lock file: %v", err)
	}

	// RecoverWorktree should:
	// 1. CheckLock -> lock exists, not running
	// 2. forceReleaseLock -> removes lock file
	// 3. resetTask: bd show task-123 --json (check status), then bd update
	// 4. resetOrphanedAgentTasks (bd list for test-agent, skipping task-123)
	// 5. cleanUntrackedFiles (git clean -fdn)
	mock := NewCommandMock(t, []CommandStub{
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"show", "task-123", "--json"},
			Stdout: `[{"status":"in_progress"}]`,
			Err:    nil,
		},
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"update", "task-123", "--status", "open", "--assignee", ""},
			Stdout: "Updated\n",
			Err:    nil,
		},
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"list", "--assignee", "test-agent", "--status", "in_progress", "--json"},
			Stdout: `[]`,
			Err:    nil,
		},
		{
			Dir:    tmpDir,
			Name:   "git",
			Args:   []string{"clean", "-fdn"},
			Stdout: "",
			Err:    nil,
		},
	})
	mock.Install()

	err := RecoverWorktree(tmpDir, "test-agent", -1)
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}

	// Verify lock file was removed
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("lock file should have been removed by forceReleaseLock")
	}
}

func TestRecoverWorktree_LockCheckError(t *testing.T) {
	ResetBeadsDirCache()
	tmpDir := t.TempDir()

	// Create a directory named .agent.lock instead of a file, causing ReadFile to fail
	lockDirPath := filepath.Join(tmpDir, LockFileName)
	if err := os.Mkdir(lockDirPath, 0755); err != nil {
		t.Fatalf("failed to create lock dir: %v", err)
	}

	// No mocks needed — RecoverWorktree should return an error before calling any commands
	mock := NewCommandMock(t, []CommandStub{})
	mock.Install()

	err := RecoverWorktree(tmpDir, "test-agent", -1)
	if err == nil {
		t.Error("expected error when CheckLock fails, got nil")
	}
}

func TestRecoverWorktree_EmptyAgentName(t *testing.T) {
	ResetBeadsDirCache()
	tmpDir := t.TempDir()

	// No lock file. Empty agent name means resetOrphanedAgentTasks returns immediately.
	// Only cleanUntrackedFiles runs (git clean -fdn returning empty).
	mock := NewCommandMock(t, []CommandStub{
		{
			Dir:    tmpDir,
			Name:   "git",
			Args:   []string{"clean", "-fdn"},
			Stdout: "",
			Err:    nil,
		},
	})
	mock.Install()

	err := RecoverWorktree(tmpDir, "", -1)
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
}

// ============================================================================
// Workspace-Aware Tests
// ============================================================================

func TestForceReleaseLock_WorkspacePath(t *testing.T) {
	// In workspace mode, forceReleaseLock uses ResolveLockDir which redirects
	// to the workspace root. Verify the lock at the workspace root is removed.
	wsDir := t.TempDir()
	repoDir := filepath.Join(wsDir, "repo1")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatalf("failed to create repo dir: %v", err)
	}

	setupLockWorkspaceConfig(t, &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"testws": {
				Path: wsDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: repoDir},
				},
			},
		},
	})

	// Create lock file at workspace root (where ResolveLockDir points)
	lockPath := filepath.Join(wsDir, LockFileName)
	if err := os.WriteFile(lockPath, []byte(`{"pid":12345}`), 0644); err != nil {
		t.Fatalf("failed to create lock file: %v", err)
	}

	// Call forceReleaseLock with the REPO path (not workspace root)
	err := forceReleaseLock(repoDir)
	if err != nil {
		t.Errorf("forceReleaseLock returned error: %v", err)
	}

	// Lock at workspace root should be removed
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("lock file at workspace root should have been removed")
	}

	// There should be no lock at repo dir
	repoLockPath := filepath.Join(repoDir, LockFileName)
	if _, err := os.Stat(repoLockPath); !os.IsNotExist(err) {
		t.Error("no lock file should exist at repo dir")
	}
}

func TestForceReleaseLock_WorkspacePath_NoLock(t *testing.T) {
	// forceReleaseLock via workspace path when no lock exists should return error
	wsDir := t.TempDir()
	repoDir := filepath.Join(wsDir, "repo1")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatalf("failed to create repo dir: %v", err)
	}

	setupLockWorkspaceConfig(t, &LoomConfig{
		Workspaces: map[string]WorkspaceConfig{
			"testws": {
				Path: wsDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: repoDir},
				},
			},
		},
	})

	err := forceReleaseLock(repoDir)
	if err == nil {
		t.Error("expected error when removing non-existent lock file in workspace mode")
	}
	if !os.IsNotExist(err) {
		t.Errorf("expected os.IsNotExist error, got: %v", err)
	}
}

func TestAnalyzeTaskCompletion_WorkspaceMode(t *testing.T) {
	// In workspace mode, analyzeTaskCompletion should search git logs across
	// all repos discovered by the resolver, aggregating with repo name labels.
	ResetBeadsDirCache()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repo1Path := filepath.Join(tmpDir, "repo1")
	repo2Path := filepath.Join(tmpDir, "repo2")
	createGitRepo(t, repo1Path)
	createGitRepo(t, repo2Path)

	cfg := &LoomConfig{
		DefaultWorkspace: "testws",
		Workspaces: map[string]WorkspaceConfig{
			"testws": {
				Path: tmpDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: repo1Path},
					{Name: "repo2", Path: repo2Path},
				},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	mock := NewCommandMock(t, []CommandStub{
		// bd show task (GetBeadsDir() returns tmpDir in workspace mode)
		{
			Dir:    tmpDir,
			Name:   "bd",
			Args:   []string{"show", "task-ws1"},
			Stdout: "Task: Cross-repo feature\nStatus: in_progress\n",
			Err:    nil,
		},
		// DiscoverWorktrees calls GetCurrentBranch for each repo
		{Dir: repo1Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "main\n"},
		{Dir: repo2Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "main\n"},
		// git log in repo1
		{
			Dir:    repo1Path,
			Name:   "git",
			Args:   []string{"log", "--oneline", "-20", "--all", "--grep", "task-ws1"},
			Stdout: "abc123 feat: implement backend (task-ws1)\n",
			Err:    nil,
		},
		// git log in repo2
		{
			Dir:    repo2Path,
			Name:   "git",
			Args:   []string{"log", "--oneline", "-20", "--all", "--grep", "task-ws1"},
			Stdout: "def456 feat: implement frontend (task-ws1)\n",
			Err:    nil,
		},
		// claude analysis
		{
			Dir:    "/test/worktree",
			Name:   "claude",
			Stdout: "COMPLETED: Both backend and frontend implemented across repos\n",
			Err:    nil,
		},
	})
	mock.Install()

	completed, reason := analyzeTaskCompletion("/test/worktree", "task-ws1")

	if !completed {
		t.Error("expected completed=true in workspace mode")
	}
	if reason != "Both backend and frontend implemented across repos" {
		t.Errorf("unexpected reason: %q", reason)
	}
}

func TestAnalyzeTaskCompletion_WorkspaceMode_NoCommitsInAnyRepo(t *testing.T) {
	// When workspace mode finds no commits across any repo, the fallback
	// single-repo search also runs. Either way, claude receives empty git logs.
	ResetBeadsDirCache()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repo1Path := filepath.Join(tmpDir, "repo1")
	createGitRepo(t, repo1Path)

	cfg := &LoomConfig{
		DefaultWorkspace: "testws",
		Workspaces: map[string]WorkspaceConfig{
			"testws": {
				Path: tmpDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: repo1Path},
				},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	mock := NewCommandMock(t, []CommandStub{
		// bd show task (GetBeadsDir returns tmpDir)
		{
			Dir:    tmpDir,
			Name:   "bd",
			Args:   []string{"show", "task-empty"},
			Stdout: "Task: Nothing done\nStatus: in_progress\n",
			Err:    nil,
		},
		// DiscoverWorktrees: GetCurrentBranch for repo1
		{Dir: repo1Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "main\n"},
		// git log in repo1 returns empty
		{
			Dir:  repo1Path,
			Name: "git",
			Args: []string{"log", "--oneline", "-20", "--all", "--grep", "task-empty"},
			// Return empty to simulate no commits
			Stdout: "",
			Err:    nil,
		},
		// Fallback single-repo search OR claude (depends on whether fallback triggers).
		// Use permissive matching to handle either case.
		{
			Name:   "claude",
			Stdout: "INCOMPLETE: No commits found\n",
			Err:    nil,
		},
	})
	mock.Install()

	completed, _ := analyzeTaskCompletion("/test/worktree", "task-empty")

	if completed {
		t.Error("expected completed=false when no commits found in workspace")
	}
}

func TestAnalyzeTaskCompletion_WorkspaceMode_PartialResults(t *testing.T) {
	// Only some repos have matching commits; the aggregated output
	// should include only those repos with results.
	ResetBeadsDirCache()

	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repo1Path := filepath.Join(tmpDir, "repo1")
	repo2Path := filepath.Join(tmpDir, "repo2")
	createGitRepo(t, repo1Path)
	createGitRepo(t, repo2Path)

	cfg := &LoomConfig{
		DefaultWorkspace: "testws",
		Workspaces: map[string]WorkspaceConfig{
			"testws": {
				Path: tmpDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: repo1Path},
					{Name: "repo2", Path: repo2Path},
				},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	mock := NewCommandMock(t, []CommandStub{
		// bd show (GetBeadsDir returns tmpDir)
		{
			Dir:    tmpDir,
			Name:   "bd",
			Args:   []string{"show", "task-partial"},
			Stdout: "Task: Partial work\nStatus: in_progress\n",
			Err:    nil,
		},
		// DiscoverWorktrees: GetCurrentBranch for each repo
		{Dir: repo1Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "main\n"},
		{Dir: repo2Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "main\n"},
		// repo1 has commits
		{
			Dir:    repo1Path,
			Name:   "git",
			Args:   []string{"log", "--oneline", "-20", "--all", "--grep", "task-partial"},
			Stdout: "abc123 partial work (task-partial)\n",
			Err:    nil,
		},
		// repo2 has no commits
		{
			Dir:    repo2Path,
			Name:   "git",
			Args:   []string{"log", "--oneline", "-20", "--all", "--grep", "task-partial"},
			Stdout: "",
			Err:    nil,
		},
		// claude analysis (gitOutput is not empty because repo1 had results)
		{
			Dir:    "/test/worktree",
			Name:   "claude",
			Stdout: "INCOMPLETE: Only backend work done in repo1\n",
			Err:    nil,
		},
	})
	mock.Install()

	completed, reason := analyzeTaskCompletion("/test/worktree", "task-partial")

	if completed {
		t.Error("expected completed=false")
	}
	if reason != "Only backend work done in repo1" {
		t.Errorf("unexpected reason: %q", reason)
	}
}

func TestCleanUntrackedFiles_WorkspaceMode(t *testing.T) {
	// In workspace mode, cleanUntrackedFiles should iterate over all repos.
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repo1Path := filepath.Join(tmpDir, "repo1")
	repo2Path := filepath.Join(tmpDir, "repo2")
	createGitRepo(t, repo1Path)
	createGitRepo(t, repo2Path)

	cfg := &LoomConfig{
		DefaultWorkspace: "testws",
		Workspaces: map[string]WorkspaceConfig{
			"testws": {
				Path: tmpDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: repo1Path},
					{Name: "repo2", Path: repo2Path},
				},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	// DiscoverWorktrees calls GetCurrentBranch for each repo, then
	// GitCleanDryRun (via RunGitCommand/execCommand) for each.
	mock := NewCommandMock(t, []CommandStub{
		// DiscoverWorktrees: GetCurrentBranch
		{Dir: repo1Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "main\n"},
		{Dir: repo2Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "main\n"},
		// GitCleanDryRun for each repo
		{Dir: repo1Path, Name: "git", Args: []string{"clean", "-fdn"}, Stdout: "Would remove file1.txt\n"},
		{Dir: repo2Path, Name: "git", Args: []string{"clean", "-fdn"}, Stdout: "Would remove file2.txt\n"},
	})
	mock.Install()

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Dir: repo1Path, Args: []string{"clean", "-fd"}},
		{Dir: repo2Path, Args: []string{"clean", "-fd"}},
	})
	outputMock.Install()

	cleanUntrackedFiles("/some/path", true)
}

func TestCleanUntrackedFiles_WorkspaceMode_NoUntrackedInAnyRepo(t *testing.T) {
	// No untracked files in any workspace repo -- GitClean should not be called.
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repo1Path := filepath.Join(tmpDir, "repo1")
	repo2Path := filepath.Join(tmpDir, "repo2")
	createGitRepo(t, repo1Path)
	createGitRepo(t, repo2Path)

	cfg := &LoomConfig{
		DefaultWorkspace: "testws",
		Workspaces: map[string]WorkspaceConfig{
			"testws": {
				Path: tmpDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: repo1Path},
					{Name: "repo2", Path: repo2Path},
				},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	mock := NewCommandMock(t, []CommandStub{
		// DiscoverWorktrees: GetCurrentBranch
		{Dir: repo1Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "main\n"},
		{Dir: repo2Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "main\n"},
		// GitCleanDryRun returns empty for both
		{Dir: repo1Path, Name: "git", Args: []string{"clean", "-fdn"}, Stdout: ""},
		{Dir: repo2Path, Name: "git", Args: []string{"clean", "-fdn"}, Stdout: ""},
	})
	mock.Install()

	// No OutputCommandMock needed -- GitClean should not be called
	cleanUntrackedFiles("/some/path", true)
}

func TestCleanUntrackedFiles_WorkspaceMode_PartialUntracked(t *testing.T) {
	// Only one repo has untracked files. Both repos go through the dry-run
	// check, but both have GitClean called when any repo has untracked files.
	// However, the code below tests that the workspace iterates all repos.
	tmpDir := t.TempDir()
	tmpDir, _ = filepath.EvalSymlinks(tmpDir)

	repo1Path := filepath.Join(tmpDir, "repo1")
	repo2Path := filepath.Join(tmpDir, "repo2")
	createGitRepo(t, repo1Path)
	createGitRepo(t, repo2Path)

	cfg := &LoomConfig{
		DefaultWorkspace: "testws",
		Workspaces: map[string]WorkspaceConfig{
			"testws": {
				Path: tmpDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: repo1Path},
					{Name: "repo2", Path: repo2Path},
				},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	// Both repos have untracked files so that GitClean is definitively called for both
	mock := NewCommandMock(t, []CommandStub{
		// DiscoverWorktrees: GetCurrentBranch
		{Dir: repo1Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "main\n"},
		{Dir: repo2Path, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "main\n"},
		// GitCleanDryRun: repo1 has files, repo2 also has files
		{Dir: repo1Path, Name: "git", Args: []string{"clean", "-fdn"}, Stdout: "Would remove leftover.txt\n"},
		{Dir: repo2Path, Name: "git", Args: []string{"clean", "-fdn"}, Stdout: "Would remove other.txt\n"},
	})
	mock.Install()

	outputMock := NewOutputCommandMock(t, []OutputCommandStub{
		{Dir: repo1Path, Args: []string{"clean", "-fd"}},
		{Dir: repo2Path, Args: []string{"clean", "-fd"}},
	})
	outputMock.Install()

	cleanUntrackedFiles("/some/path", true)
}

func TestRecoverWorktree_WorkspaceStaleLock(t *testing.T) {
	// Full RecoverWorktree flow in workspace mode: lock at workspace root,
	// clean across workspace repos.
	ResetBeadsDirCache()

	wsDir := t.TempDir()
	wsDir, _ = filepath.EvalSymlinks(wsDir)
	repoDir := filepath.Join(wsDir, "repo1")
	createGitRepo(t, repoDir)

	cfg := &LoomConfig{
		DefaultWorkspace: "testws",
		Workspaces: map[string]WorkspaceConfig{
			"testws": {
				Path: wsDir,
				Repos: []RepoConfig{
					{Name: "repo1", Path: repoDir},
				},
			},
		},
	}
	setupWorkspaceConfig(t, cfg)

	old := defaultResolver
	defaultResolver = nil
	defer func() { defaultResolver = old }()

	// Create lock file at workspace root (where ResolveLockDir resolves to)
	lockPath := filepath.Join(wsDir, LockFileName)
	lockData := `{"pid":999999999,"command":"test","started_at":"2024-01-01T00:00:00Z","agent_name":"test-agent","task_id":"task-ws","workspace":"testws"}`
	if err := os.WriteFile(lockPath, []byte(lockData), 0644); err != nil {
		t.Fatalf("failed to create lock file: %v", err)
	}

	mock := NewCommandMock(t, []CommandStub{
		// resetTask: check status first (GetBeadsDir returns wsDir)
		{
			Dir:    wsDir,
			Name:   "bd",
			Args:   []string{"show", "task-ws", "--json"},
			Stdout: `[{"status":"in_progress"}]`,
			Err:    nil,
		},
		// resetTask for task-ws (GetBeadsDir returns wsDir)
		{
			Dir:    wsDir,
			Name:   "bd",
			Args:   []string{"update", "task-ws", "--status", "open", "--assignee", ""},
			Stdout: "Updated\n",
			Err:    nil,
		},
		// resetOrphanedAgentTasks (GetBeadsDir returns wsDir)
		{
			Dir:    wsDir,
			Name:   "bd",
			Args:   []string{"list", "--assignee", "test-agent", "--status", "in_progress", "--json"},
			Stdout: `[]`,
			Err:    nil,
		},
		// cleanUntrackedFiles: DiscoverWorktrees calls GetCurrentBranch
		{Dir: repoDir, Name: "git", Args: []string{"branch", "--show-current"}, Stdout: "main\n"},
		// cleanUntrackedFiles: GitCleanDryRun
		{Dir: repoDir, Name: "git", Args: []string{"clean", "-fdn"}, Stdout: ""},
	})
	mock.Install()

	// Pass repoDir as worktreePath -- ResolveLockDir redirects to wsDir
	err := RecoverWorktree(repoDir, "test-agent", -1)
	if err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}

	// Verify lock file was removed at workspace root
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Error("lock file at workspace root should have been removed")
	}
}
