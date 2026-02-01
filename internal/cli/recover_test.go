package cli

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
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
	mock := NewCommandMock(t, []CommandStub{{
		Dir:    "/test/worktree",
		Name:   "bd",
		Args:   []string{"close", "task-123", "--reason", "Completed (verified by recovery analysis): Tests pass"},
		Stdout: "Task closed\n",
		Err:    nil,
	}})
	mock.Install()

	// closeTask doesn't return anything, just verify no panic and correct args
	closeTask("/test/worktree", "task-123", "Tests pass")
}

func TestCloseTask_Failure(t *testing.T) {
	mock := NewCommandMock(t, []CommandStub{{
		Dir:    "/test/worktree",
		Name:   "bd",
		Args:   []string{"close", "task-456", "--reason", "Completed (verified by recovery analysis): Done"},
		Stdout: "",
		Stderr: "Error: task not found\n",
		Err:    errors.New("exit status 1"),
	}})
	mock.Install()

	// closeTask prints warning but doesn't panic
	closeTask("/test/worktree", "task-456", "Done")
}

func TestResetTask_Success(t *testing.T) {
	mock := NewCommandMock(t, []CommandStub{{
		Dir:    "/test/worktree",
		Name:   "bd",
		Args:   []string{"update", "task-789", "--status", "open", "--assignee", ""},
		Stdout: "Task updated\n",
		Err:    nil,
	}})
	mock.Install()

	resetTask("/test/worktree", "task-789")
}

func TestResetTask_Failure(t *testing.T) {
	mock := NewCommandMock(t, []CommandStub{{
		Dir:    "/test/worktree",
		Name:   "bd",
		Args:   []string{"update", "task-789", "--status", "open", "--assignee", ""},
		Stdout: "",
		Stderr: "Error: invalid task\n",
		Err:    errors.New("exit status 1"),
	}})
	mock.Install()

	// resetTask prints warning and manual instructions but doesn't panic
	resetTask("/test/worktree", "task-789")
}

func TestAnalyzeTaskCompletion_Completed(t *testing.T) {
	mock := NewCommandMock(t, []CommandStub{
		{
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
	mock := NewCommandMock(t, []CommandStub{
		{
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
	mock := NewCommandMock(t, []CommandStub{
		{
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
	mock := NewCommandMock(t, []CommandStub{
		{
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
	mock := NewCommandMock(t, []CommandStub{
		{
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
	mock := NewCommandMock(t, []CommandStub{
		{
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
	mock := NewCommandMock(t, []CommandStub{
		{
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
	mock := NewCommandMock(t, []CommandStub{
		{
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
	mock := NewCommandMock(t, []CommandStub{
		// analyzeTaskCompletion calls
		{
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
			Dir:    "/test/worktree",
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
	mock := NewCommandMock(t, []CommandStub{
		// analyzeTaskCompletion calls
		{
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
		// resetTask call
		{
			Dir:    "/test/worktree",
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
	mock := NewCommandMock(t, []CommandStub{
		// resetTask call only (no analyze)
		{
			Dir:    "/test/worktree",
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
	// Start a sleep process that we can kill
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start sleep process: %v", err)
	}
	pid := cmd.Process.Pid

	// Verify it's running
	if !IsProcessRunning(pid) {
		t.Fatal("process should be running before kill")
	}

	// Reap the child in a goroutine so it doesn't become a zombie
	// (in production, killed processes aren't children of the recover command)
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()

	// Kill it (sends SIGTERM first, then SIGKILL if needed)
	err := killProcess(pid)
	if err != nil {
		t.Errorf("killProcess returned error: %v", err)
	}

	<-done

	// Verify it's dead
	if IsProcessRunning(pid) {
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
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start sleep process: %v", err)
	}
	pid := cmd.Process.Pid

	// Kill it directly
	_ = syscall.Kill(pid, syscall.SIGKILL)
	_ = cmd.Wait()

	// Now killProcess should handle the already-dead case gracefully
	err := killProcess(pid)
	if err != nil {
		t.Errorf("killProcess should succeed for already-dead process, got: %v", err)
	}
}

func TestResetOrphanedAgentTasks_FindsAndResetsMultiple(t *testing.T) {
	mock := NewCommandMock(t, []CommandStub{
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"list", "--assignee", "falcon", "--status", "in_progress", "--json"},
			Stdout: `[{"id":"task-1","title":"First task"},{"id":"task-2","title":"Second task"}]`,
			Err:    nil,
		},
		// resetTask for task-1
		{
			Dir:    "/test/worktree",
			Name:   "bd",
			Args:   []string{"update", "task-1", "--status", "open", "--assignee", ""},
			Stdout: "Updated\n",
			Err:    nil,
		},
		// resetTask for task-2
		{
			Dir:    "/test/worktree",
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
	mock := NewCommandMock(t, []CommandStub{
		{
			Dir:    ".",
			Name:   "bd",
			Args:   []string{"list", "--assignee", "ember", "--status", "in_progress", "--json"},
			Stdout: `[{"id":"task-1","title":"Already handled"},{"id":"task-2","title":"Orphaned task"}]`,
			Err:    nil,
		},
		// resetTask for task-2 only (task-1 is already handled)
		{
			Dir:    "/test/worktree",
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
