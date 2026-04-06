//go:build ignore

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/usage"
)

func TestCheckYieldFile_EmptyPath(t *testing.T) {
	t.Parallel()
	reason, yielded := checkYieldFile("")
	if yielded {
		t.Error("expected yielded=false for empty path")
	}
	if reason != "" {
		t.Errorf("expected empty reason, got %q", reason)
	}
}

func TestCheckYieldFile_NoFile(t *testing.T) {
	t.Parallel()
	reason, yielded := checkYieldFile(filepath.Join(t.TempDir(), ".agent.yield"))
	if yielded {
		t.Error("expected yielded=false when file does not exist")
	}
	if reason != "" {
		t.Errorf("expected empty reason, got %q", reason)
	}
}

func TestCheckYieldFile_ValidJSON(t *testing.T) {
	t.Parallel()
	f := filepath.Join(t.TempDir(), ".agent.yield")
	os.WriteFile(f, []byte(`{"reason":"manual_stop","requested_at":"2026-04-04T00:00:00Z","requested_by":"daemon"}`), 0600)

	reason, yielded := checkYieldFile(f)
	if !yielded {
		t.Error("expected yielded=true for valid yield file")
	}
	if reason != "manual_stop" {
		t.Errorf("expected reason %q, got %q", "manual_stop", reason)
	}
}

func TestCheckYieldFile_InvalidJSON(t *testing.T) {
	t.Parallel()
	f := filepath.Join(t.TempDir(), ".agent.yield")
	os.WriteFile(f, []byte("not-json"), 0600)

	reason, yielded := checkYieldFile(f)
	if !yielded {
		t.Error("expected yielded=true for invalid JSON yield file")
	}
	if reason != "unknown" {
		t.Errorf("expected reason %q, got %q", "unknown", reason)
	}
}

func TestCheckYieldFile_EmptyFile(t *testing.T) {
	t.Parallel()
	f := filepath.Join(t.TempDir(), ".agent.yield")
	os.WriteFile(f, []byte(""), 0600)

	reason, yielded := checkYieldFile(f)
	if !yielded {
		t.Error("expected yielded=true for empty yield file")
	}
	if reason != "unknown" {
		t.Errorf("expected reason %q, got %q", "unknown", reason)
	}
}

func TestRunAutoModeLoop_YieldBeforeFirstTask(t *testing.T) {
	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Create yield file before loop starts
	yieldFile := filepath.Join(tmpDir, ".agent.yield")
	os.WriteFile(yieldFile, []byte(`{"reason":"test_preempt"}`), 0600)
	t.Setenv("LOOM_YIELD_FILE", yieldFile)

	// Mock bd ready to return tasks (loop would continue without yield)
	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	claudeInvoked := false
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		claudeInvoked = true
		return nil
	})

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(5 * time.Second):
		t.Fatal("RunAutoModeLoop did not exit after yield")
	}

	if claudeInvoked {
		t.Error("Claude was invoked despite yield file present before first task")
	}
}

func TestRunAutoModeLoop_YieldAfterTask(t *testing.T) {
	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	yieldFile := filepath.Join(tmpDir, ".agent.yield")
	t.Setenv("LOOM_YIELD_FILE", yieldFile)

	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	invocations := 0
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		invocations++
		// Write yield file after first invocation returns
		os.WriteFile(yieldFile, []byte(`{"reason":"post_task_yield"}`), 0600)
		UpdateLockTask(workDir, fmt.Sprintf("mock-%d", invocations), "Mock Task")
		return nil
	})

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(5 * time.Second):
		t.Fatal("RunAutoModeLoop did not exit after yield")
	}

	if invocations != 1 {
		t.Errorf("expected 1 invocation, got %d (should not start second task after yield)", invocations)
	}
}

func TestRunAutoModeLoop_NoYieldFileEnv(t *testing.T) {
	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	// Do NOT set LOOM_YIELD_FILE — ensure any yield file on disk is ignored
	t.Setenv("LOOM_YIELD_FILE", "")
	yieldFile := filepath.Join(tmpDir, ".agent.yield")
	os.WriteFile(yieldFile, []byte(`{"reason":"should_be_ignored"}`), 0600)

	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	invocations := 0
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		invocations++
		UpdateLockTask(workDir, fmt.Sprintf("mock-%d", invocations), "Mock Task")
		return nil
	})

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     2, // Limit to 2 tasks to prove loop continues
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(5 * time.Second):
		t.Fatal("RunAutoModeLoop did not exit")
	}

	if invocations < 2 {
		t.Errorf("expected at least 2 invocations (yield ignored without env var), got %d", invocations)
	}
}

func TestRunAutoModeLoop_YieldWithMalformedJSON(t *testing.T) {
	tmpDir := t.TempDir()
	setupLockFile(t, tmpDir)

	yieldFile := filepath.Join(tmpDir, ".agent.yield")
	os.WriteFile(yieldFile, []byte("not-valid-json"), 0600)
	t.Setenv("LOOM_YIELD_FILE", yieldFile)

	installExecMock(t, &MockExecRunner{RunFunc: func(dir, name string, args ...string) CommandResult {
		return CommandResult{
			Stdout: mustJSON([]backend.IssueData{
				{ID: "T-1", Title: "Task", Status: "open", Design: "Design"},
			}),
		}
	}})

	claudeInvoked := false
	installClaudeNonInteractiveMock(t, func(workDir, prompt, agentName string, shutdown <-chan struct{}, _ *usage.Collector) error {
		claudeInvoked = true
		return nil
	})

	shutdown := make(chan struct{})
	opts := AutoModeOptions{
		Interval:     0,
		MaxTasks:     0,
		IdleTimeout:  0,
		AgentType:    "task",
		AgentName:    "test",
		WorktreePath: tmpDir,
		BackoffBase:  10 * time.Millisecond,
		TaskPause:    10 * time.Millisecond,
	}

	done := make(chan struct{})
	go func() {
		RunAutoModeLoop(opts, shutdown)
		close(done)
	}()

	select {
	case <-done:
		// Good
	case <-time.After(5 * time.Second):
		t.Fatal("RunAutoModeLoop did not exit after yield with malformed JSON")
	}

	if claudeInvoked {
		t.Error("Claude was invoked despite malformed yield file present")
	}
}
