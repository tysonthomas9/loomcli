package automode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTmuxWaitForTasksBranches(t *testing.T) {
	shutdown := make(chan struct{})
	ctx := &tmuxLoopCtx{
		opts:              AutoModeOptions{Interval: 999},
		sessionName:       "loom-test-missing",
		hasAvailableTasks: func() (bool, error) { return true, nil },
		idleStart:         time.Now(),
	}
	if !tmuxWaitForTasks(ctx, shutdown) {
		t.Fatal("available task should continue")
	}

	close(shutdown)
	ctx.hasAvailableTasks = func() (bool, error) { return false, os.ErrPermission }
	if tmuxWaitForTasks(ctx, shutdown) {
		t.Fatal("task check error with shutdown should exit")
	}

	openShutdown := make(chan struct{})
	ctx = &tmuxLoopCtx{
		opts:              AutoModeOptions{IdleTimeout: 1},
		sessionName:       "loom-test-missing",
		hasAvailableTasks: func() (bool, error) { return false, nil },
		idleStart:         time.Now().Add(-2 * time.Minute),
	}
	if tmuxWaitForTasks(ctx, openShutdown) {
		t.Fatal("idle timeout should exit")
	}
}

func TestTmuxHandlePostSessionBranches(t *testing.T) {
	worktree := t.TempDir()
	yieldFile := filepath.Join(t.TempDir(), "yield.json")
	if err := os.WriteFile(yieldFile, []byte(`{"reason":"maintenance"}`), 0o600); err != nil {
		t.Fatalf("write yield: %v", err)
	}
	ctx := &tmuxLoopCtx{
		opts:        AutoModeOptions{WorktreePath: worktree, AgentName: "worker"},
		sessionName: "loom-test-missing",
		yieldFile:   yieldFile,
	}
	if tmuxHandlePostSession(ctx, make(chan struct{})) {
		t.Fatal("yield after session should exit")
	}

	lockBytes, err := json.Marshal(LockInfo{TaskID: "TASK-1"})
	if err != nil {
		t.Fatalf("marshal lock: %v", err)
	}
	if err := os.WriteFile(filepath.Join(worktree, LockFileName), lockBytes, 0o600); err != nil {
		t.Fatalf("write lock: %v", err)
	}
	ctx = &tmuxLoopCtx{opts: AutoModeOptions{WorktreePath: worktree, AgentName: "worker"}}
	if !tmuxHandlePostSession(ctx, make(chan struct{})) {
		t.Fatal("claimed task should continue")
	}
	if ctx.taskCount != 1 || ctx.consecutiveNoProgress != 0 {
		t.Fatalf("post claimed state = taskCount %d noProgress %d", ctx.taskCount, ctx.consecutiveNoProgress)
	}

	noProgressShutdown := make(chan struct{})
	close(noProgressShutdown)
	_ = os.Remove(filepath.Join(worktree, LockFileName))
	ctx = &tmuxLoopCtx{
		opts:        AutoModeOptions{WorktreePath: worktree, AgentName: "worker"},
		sessionName: "loom-test-missing",
	}
	if tmuxHandlePostSession(ctx, noProgressShutdown) {
		t.Fatal("no progress interrupted by shutdown should exit")
	}
	if ctx.consecutiveNoProgress != 1 {
		t.Fatalf("no progress count = %d, want 1", ctx.consecutiveNoProgress)
	}

	ctx.consecutiveNoProgress = 2
	if tmuxHandlePostSession(ctx, make(chan struct{})) {
		t.Fatal("third no-progress session should exit")
	}
}
