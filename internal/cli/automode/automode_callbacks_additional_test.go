package automode

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/cli/backends"
)

type callbackBridge struct {
	stateAgent string
	state      string
	clearAgent string
	readAgent  string
	lock       *cli.LockInfo
}

func (b *callbackBridge) UpdateState(agentName, state string) error {
	b.stateAgent = agentName
	b.state = state
	return nil
}

func (b *callbackBridge) UpdateTask(agentName, taskID, title string) error { return nil }

func (b *callbackBridge) ClearTaskID(agentName string) error {
	b.clearAgent = agentName
	return nil
}

func (b *callbackBridge) UpdateClaudeSessionID(agentName, claudeSessionID string) error {
	return nil
}

func (b *callbackBridge) ClearClaudeSessionID(agentName string) error { return nil }

func (b *callbackBridge) ReadLock(agentName string) (*cli.LockInfo, error) {
	b.readAgent = agentName
	return b.lock, nil
}

func TestAutoModeCallbackBuildersUseBridge(t *testing.T) {
	bridge := &callbackBridge{lock: &cli.LockInfo{TaskID: "T-1"}}
	opts := AutoModeOptions{AgentName: "nova", LockBridge: bridge}

	if err := buildStateUpdater(opts)(cli.StateActive); err != nil {
		t.Fatalf("update state: %v", err)
	}
	if err := buildTaskIDClearer(opts)(); err != nil {
		t.Fatalf("clear task: %v", err)
	}
	lock, err := buildLockReader(opts)()
	if err != nil {
		t.Fatalf("read lock: %v", err)
	}
	if bridge.stateAgent != "nova" || bridge.state != cli.StateActive ||
		bridge.clearAgent != "nova" || bridge.readAgent != "nova" || lock.TaskID != "T-1" {
		t.Fatalf("bridge calls = %+v lock=%+v", bridge, lock)
	}
	if !agentClaimedTask("", "nova", bridge) {
		t.Fatal("agentClaimedTask should read claimed task from bridge")
	}

	bridge.lock = &cli.LockInfo{}
	if agentClaimedTask("", "nova", bridge) {
		t.Fatal("agentClaimedTask should ignore empty bridge lock")
	}
}

func TestAutoModeExitWaitAndSummaryBranches(t *testing.T) {
	shutdown := make(chan struct{})
	close(shutdown)
	ctx := &autoLoopCtx{opts: AutoModeOptions{}, state: &AutoModeState{}}
	if got := checkAutoExitConditions(ctx, shutdown); got != "shutdown signal received" {
		t.Fatalf("shutdown exit reason = %q", got)
	}

	yieldFile := filepath.Join(t.TempDir(), "yield.json")
	if err := os.WriteFile(yieldFile, []byte(`{"reason":"operator"}`), 0o600); err != nil {
		t.Fatalf("write yield file: %v", err)
	}
	ctx = &autoLoopCtx{yieldFile: yieldFile, opts: AutoModeOptions{}, state: &AutoModeState{}}
	if got := checkAutoExitConditions(ctx, make(chan struct{})); got != "yield requested: operator" {
		t.Fatalf("yield exit reason = %q", got)
	}

	ctx = &autoLoopCtx{opts: AutoModeOptions{MaxTasks: 2}, state: &AutoModeState{TasksCompleted: 2}}
	if got := checkAutoExitConditions(ctx, make(chan struct{})); got != "reached max tasks limit (2)" {
		t.Fatalf("max task exit reason = %q", got)
	}

	ctx = &autoLoopCtx{
		opts:  AutoModeOptions{Interval: 60},
		state: &AutoModeState{},
		hasAvailableTasks: func() (bool, error) {
			return false, errors.New("backend down")
		},
	}
	if waitForAvailableTasks(ctx, shutdown) {
		t.Fatal("waitForAvailableTasks should stop when error sleep is interrupted")
	}
	if !ctx.state.ShouldExit || ctx.state.ExitReason != "shutdown signal received" {
		t.Fatalf("interrupted error wait state = %+v", ctx.state)
	}

	ctx = &autoLoopCtx{
		opts:  AutoModeOptions{IdleTimeout: 1},
		state: &AutoModeState{IdleStartTime: time.Now().Add(-2 * time.Minute)},
		hasAvailableTasks: func() (bool, error) {
			return false, nil
		},
	}
	if waitForAvailableTasks(ctx, make(chan struct{})) {
		t.Fatal("waitForAvailableTasks should stop on idle timeout")
	}
	if !ctx.state.ShouldExit || ctx.state.ExitReason != "idle timeout exceeded (1 minutes)" {
		t.Fatalf("idle timeout state = %+v", ctx.state)
	}

	printAutoModeSummary(&AutoModeState{
		ExitReason:            "done",
		TasksCompleted:        3,
		ConsecutiveErrors:     1,
		ConsecutiveRateLimits: 2,
		ConsecutiveNoProgress: 4,
		CircuitBreakerTrips:   5,
	})
	if formatLimit(0) != "unlimited" || formatLimit(7) != "7" {
		t.Fatal("formatLimit returned unexpected values")
	}
	if formatTimeout(0) != "none" || formatTimeout(9) != "9m" {
		t.Fatal("formatTimeout returned unexpected values")
	}
}

func TestAutoModeResolveTaskCheckerAndCaptureSessionID(t *testing.T) {
	called := false
	custom := resolveTaskChecker(AutoModeOptions{CustomTaskCheck: func() (bool, error) {
		called = true
		return true, nil
	}})
	available, err := custom()
	if err != nil || !available || !called {
		t.Fatalf("custom checker available=%t called=%t err=%v", available, called, err)
	}
	if resolveTaskChecker(AutoModeOptions{AgentType: "plan"}) == nil {
		t.Fatal("plan checker is nil")
	}
	if resolveTaskChecker(AutoModeOptions{AgentType: "task"}) == nil {
		t.Fatal("task checker is nil")
	}

	backends.ClearLastCapturedSessionID()
	ctx := &autoLoopCtx{lastClaudeSessionID: "old", resumeFailures: 2}
	captureSessionID(ctx)
	if ctx.lastClaudeSessionID != "old" || ctx.resumeFailures != 2 {
		t.Fatalf("empty capture changed ctx: %+v", ctx)
	}

	backends.SetLastCapturedSessionID("claude-1")
	t.Cleanup(backends.ClearLastCapturedSessionID)
	captureSessionID(ctx)
	if ctx.lastClaudeSessionID != "claude-1" || ctx.resumeFailures != 0 {
		t.Fatalf("captured ctx = %+v", ctx)
	}
}
