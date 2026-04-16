package automode

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/agenterr"
	"github.com/tysonthomas9/loomcli/internal/cli"
	"github.com/tysonthomas9/loomcli/internal/events"
)

// captureBus is a minimal events.Emitter that records every emitted event in
// memory so tests can assert on event types and payloads.
type captureBus struct {
	mu     sync.Mutex
	events []events.Event
}

func (c *captureBus) Emit(e events.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
	return nil
}

func (c *captureBus) Close() error { return nil }

func (c *captureBus) snapshot() []events.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]events.Event, len(c.events))
	copy(out, c.events)
	return out
}

func newStuckTestCtx(taskID string, bus *captureBus) *autoLoopCtx {
	currentID := taskID
	cleared := false
	return &autoLoopCtx{
		opts: AutoModeOptions{
			AgentName:   "test-agent",
			Interval:    0,
			BackoffBase: 1 * time.Millisecond,
			TaskPause:   1 * time.Millisecond,
			EventBus:    bus,
		},
		state: &AutoModeState{},
		readLock: func() (*cli.LockInfo, error) {
			if cleared {
				return &cli.LockInfo{}, nil
			}
			return &cli.LockInfo{TaskID: currentID}, nil
		},
		clearTaskID: func() error {
			cleared = true
			return nil
		},
		stuckTaskIDs: make(map[string]bool),
	}
}

func TestHandleAutoTaskError_SameTaskStuck_EmitsEventAfterThreshold(t *testing.T) {
	t.Parallel()
	bus := &captureBus{}
	ctx := newStuckTestCtx("T-1", bus)
	shutdown := make(chan struct{})

	ae := &agenterr.AgentError{Class: agenterr.Transient, Message: "server error", Backend: "claude"}
	rawErr := errors.New("exit code 1")

	// First two failures: counter increments, no stuck event yet.
	for i := 1; i <= 2; i++ {
		cont := handleAutoTaskError(ctx, ae, rawErr, shutdown)
		if !cont {
			t.Fatalf("iteration %d: expected continue=true, got false (ExitReason=%q)", i, ctx.state.ExitReason)
		}
		if ctx.sameTaskFailures != i {
			t.Fatalf("iteration %d: sameTaskFailures = %d, want %d", i, ctx.sameTaskFailures, i)
		}
	}
	for _, e := range bus.snapshot() {
		if e.Type == events.TaskStuck {
			t.Fatal("TaskStuck event should not be emitted before threshold")
		}
	}

	// Third failure: triggers stuck-task detection.
	cont := handleAutoTaskError(ctx, ae, rawErr, shutdown)
	if !cont {
		t.Fatalf("3rd iteration: expected continue=true (skip stuck task), got false (ExitReason=%q)", ctx.state.ExitReason)
	}
	if ctx.state.ShouldExit {
		t.Errorf("ShouldExit must remain false after stuck-task skip, got true (ExitReason=%q)", ctx.state.ExitReason)
	}
	if !ctx.stuckTaskIDs["T-1"] {
		t.Error("T-1 should be in stuckTaskIDs after 3 consecutive failures")
	}
	if ctx.sameTaskFailures != 0 {
		t.Errorf("sameTaskFailures should reset to 0 after skip, got %d", ctx.sameTaskFailures)
	}
	if ctx.lastFailedTaskID != "" {
		t.Errorf("lastFailedTaskID should reset after skip, got %q", ctx.lastFailedTaskID)
	}
	if ctx.state.ConsecutiveErrors != 0 {
		t.Errorf("ConsecutiveErrors should reset to 0 after stuck-task skip, got %d", ctx.state.ConsecutiveErrors)
	}

	// Verify a TaskStuck event was emitted with the correct payload.
	var stuckCount int
	var stuckEvt events.Event
	for _, e := range bus.snapshot() {
		if e.Type == events.TaskStuck {
			stuckCount++
			stuckEvt = e
		}
	}
	if stuckCount != 1 {
		t.Fatalf("expected exactly 1 TaskStuck event, got %d", stuckCount)
	}
	decoded, err := stuckEvt.DecodeData()
	if err != nil {
		t.Fatalf("DecodeData: %v", err)
	}
	data, ok := decoded.(*events.TaskStuckData)
	if !ok {
		t.Fatalf("expected *TaskStuckData, got %T", decoded)
	}
	if data.TaskID != "T-1" {
		t.Errorf("TaskID = %q, want T-1", data.TaskID)
	}
	if data.ConsecutiveFailures != 3 {
		t.Errorf("ConsecutiveFailures = %d, want 3", data.ConsecutiveFailures)
	}
	if data.LastError != "exit code 1" {
		t.Errorf("LastError = %q, want %q", data.LastError, "exit code 1")
	}
}

func TestHandleAutoTaskError_DifferentTasks_NotStuck(t *testing.T) {
	t.Parallel()
	bus := &captureBus{}

	currentID := "A"
	ctx := &autoLoopCtx{
		opts: AutoModeOptions{
			Interval: 0, BackoffBase: 1 * time.Millisecond, EventBus: bus,
		},
		state: &AutoModeState{},
		readLock: func() (*cli.LockInfo, error) {
			return &cli.LockInfo{TaskID: currentID}, nil
		},
		clearTaskID:  func() error { return nil },
		stuckTaskIDs: make(map[string]bool),
	}
	shutdown := make(chan struct{})
	ae := &agenterr.AgentError{Class: agenterr.Transient, Message: "server error", Backend: "claude"}

	// Alternate A → B → A across 3 iterations. Because the task ID changes
	// every failure, sameTaskFailures resets to 1 each time and the stuck
	// threshold (3) is never reached. The generic ConsecutiveErrors counter
	// does accumulate and should exit the loop on the 3rd failure.
	ids := []string{"A", "B", "A"}
	for i, id := range ids {
		currentID = id
		cont := handleAutoTaskError(ctx, ae, errors.New("exit code 1"), shutdown)
		wantCont := i < 2 // first two failures continue; third hits ConsecutiveErrors >= 3 and exits
		if cont != wantCont {
			t.Fatalf("iteration %d (id=%q): cont = %v, want %v (ExitReason=%q)", i, id, cont, wantCont, ctx.state.ExitReason)
		}
		if ctx.sameTaskFailures != 1 {
			t.Errorf("iteration %d: sameTaskFailures = %d, want 1 (different task)", i, ctx.sameTaskFailures)
		}
	}

	// Third failure should have exited via the generic 3-error threshold.
	if !ctx.state.ShouldExit {
		t.Error("loop should exit after 3 different-task failures via ConsecutiveErrors threshold")
	}
	if !strings.Contains(ctx.state.ExitReason, "too many consecutive errors") {
		t.Errorf("ExitReason = %q, want to contain 'too many consecutive errors'", ctx.state.ExitReason)
	}

	for _, e := range bus.snapshot() {
		if e.Type == events.TaskStuck {
			t.Fatal("TaskStuck event must not be emitted when failed task IDs differ")
		}
	}
}

func TestHandleAutoTaskError_EmptyTaskID_DoesNotTriggerStuck(t *testing.T) {
	t.Parallel()
	bus := &captureBus{}
	ctx := &autoLoopCtx{
		opts:  AutoModeOptions{Interval: 0, BackoffBase: 1 * time.Millisecond, EventBus: bus},
		state: &AutoModeState{},
		readLock: func() (*cli.LockInfo, error) {
			return &cli.LockInfo{}, nil
		},
		clearTaskID:  func() error { return nil },
		stuckTaskIDs: make(map[string]bool),
	}
	shutdown := make(chan struct{})
	ae := &agenterr.AgentError{Class: agenterr.Transient, Message: "server error", Backend: "claude"}

	// Three consecutive failures with empty task ID — agent crashed before
	// claiming. Per-task tracking must remain at zero, and the loop should
	// exit via the generic ConsecutiveErrors threshold (not the stuck path).
	for i := 0; i < 3; i++ {
		handleAutoTaskError(ctx, ae, errors.New("exit code 1"), shutdown)
	}
	if ctx.sameTaskFailures != 0 {
		t.Errorf("sameTaskFailures = %d, want 0 (no task ID claimed)", ctx.sameTaskFailures)
	}
	if len(ctx.stuckTaskIDs) != 0 {
		t.Errorf("stuckTaskIDs should be empty, got %d entries", len(ctx.stuckTaskIDs))
	}
	for _, e := range bus.snapshot() {
		if e.Type == events.TaskStuck {
			t.Fatal("TaskStuck event must not be emitted when failed task ID is empty")
		}
	}
}

func TestHandleAutoTaskError_FatalBypassesStuckDetection(t *testing.T) {
	t.Parallel()
	bus := &captureBus{}
	ctx := newStuckTestCtx("T-1", bus)
	// Pre-set sameTaskFailures so the next failure would normally hit the
	// stuck threshold. A fatal error must still exit immediately rather than
	// being routed through the stuck-skip path.
	ctx.lastFailedTaskID = "T-1"
	ctx.sameTaskFailures = 2
	shutdown := make(chan struct{})

	ae := &agenterr.AgentError{Class: agenterr.AuthFailure, Message: "invalid api key", Backend: "claude"}
	cont := handleAutoTaskError(ctx, ae, errors.New("exit code 1"), shutdown)
	if cont {
		t.Error("fatal error must return false (exit), even when stuck threshold would otherwise fire")
	}
	if !ctx.state.ShouldExit || !strings.Contains(ctx.state.ExitReason, "fatal error") {
		t.Errorf("ShouldExit=%v ExitReason=%q, want fatal exit", ctx.state.ShouldExit, ctx.state.ExitReason)
	}
	for _, e := range bus.snapshot() {
		if e.Type == events.TaskStuck {
			t.Fatal("TaskStuck event must not be emitted on fatal error path")
		}
	}
}

func TestHandleTaskClaimed_ResetsPerTaskCounters(t *testing.T) {
	t.Parallel()
	ctx := &autoLoopCtx{
		opts:             AutoModeOptions{TaskPause: 1 * time.Millisecond, EventBus: events.NopBus{}},
		state:            &AutoModeState{},
		readLock:         func() (*cli.LockInfo, error) { return &cli.LockInfo{TaskID: "T-2"}, nil },
		clearTaskID:      func() error { return nil },
		lastFailedTaskID: "T-1",
		sameTaskFailures: 2,
		stuckTaskIDs:     map[string]bool{"T-1": true},
	}
	shutdown := make(chan struct{})

	if !handleTaskClaimed(ctx, "", time.Now(), time.Now(), shutdown) {
		t.Fatal("handleTaskClaimed should return true after a successful task")
	}
	if ctx.sameTaskFailures != 0 {
		t.Errorf("sameTaskFailures = %d, want 0 after successful task", ctx.sameTaskFailures)
	}
	if ctx.lastFailedTaskID != "" {
		t.Errorf("lastFailedTaskID = %q, want empty after successful task", ctx.lastFailedTaskID)
	}
	// stuckTaskIDs persists across tasks (per design).
	if !ctx.stuckTaskIDs["T-1"] {
		t.Error("stuckTaskIDs should persist across successful tasks")
	}
}

func TestIsStuckTaskClaimed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		stuckSet     map[string]bool
		lockTaskID   string
		readLockNil  bool
		readLockErr  error
		readLockInfo *cli.LockInfo
		want         bool
	}{
		{
			name:       "claimed task in stuck set",
			stuckSet:   map[string]bool{"T-1": true},
			lockTaskID: "T-1",
			want:       true,
		},
		{
			name:       "claimed task not in stuck set",
			stuckSet:   map[string]bool{"T-1": true},
			lockTaskID: "T-2",
			want:       false,
		},
		{
			name:       "empty stuck set",
			stuckSet:   map[string]bool{},
			lockTaskID: "T-1",
			want:       false,
		},
		{
			name:        "nil readLock",
			stuckSet:    map[string]bool{"T-1": true},
			readLockNil: true,
			want:        false,
		},
		{
			name:        "readLock error",
			stuckSet:    map[string]bool{"T-1": true},
			readLockErr: errors.New("boom"),
			want:        false,
		},
		{
			name:         "lock info nil",
			stuckSet:     map[string]bool{"T-1": true},
			readLockInfo: nil,
			want:         false,
		},
		{
			name:       "empty task ID in lock",
			stuckSet:   map[string]bool{"T-1": true},
			lockTaskID: "",
			want:       false,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := &autoLoopCtx{stuckTaskIDs: tt.stuckSet}
			if !tt.readLockNil {
				ctx.readLock = func() (*cli.LockInfo, error) {
					if tt.readLockErr != nil {
						return nil, tt.readLockErr
					}
					if tt.readLockInfo != nil {
						return tt.readLockInfo, nil
					}
					return &cli.LockInfo{TaskID: tt.lockTaskID}, nil
				}
			}
			got := isStuckTaskClaimed(ctx)
			if got != tt.want {
				t.Errorf("isStuckTaskClaimed() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandleAutoTaskError_StuckTaskDoesNotCountAgainstConsecutiveErrors(t *testing.T) {
	t.Parallel()
	bus := &captureBus{}
	ctx := newStuckTestCtx("T-1", bus)
	shutdown := make(chan struct{})
	ae := &agenterr.AgentError{Class: agenterr.Transient, Message: "server error", Backend: "claude"}

	// Three same-task failures: the third triggers stuck-skip and resets
	// ConsecutiveErrors to zero. The loop must remain alive.
	for i := 0; i < 3; i++ {
		handleAutoTaskError(ctx, ae, errors.New("exit code 1"), shutdown)
	}
	if ctx.state.ShouldExit {
		t.Fatalf("loop must remain alive after stuck-task skip, but exited (ExitReason=%q)", ctx.state.ExitReason)
	}
	if ctx.state.ConsecutiveErrors != 0 {
		t.Errorf("ConsecutiveErrors should reset to 0 after stuck-task skip, got %d", ctx.state.ConsecutiveErrors)
	}
}

// TestHandleAutoTaskError_AlreadyStuckTaskShortCircuits verifies that if the
// agent re-claims a task already in stuckTaskIDs and that invocation fails,
// handleAutoTaskError does not re-run per-task tracking or re-emit task.stuck.
// It clears the lock's TaskID and continues the loop.
func TestHandleAutoTaskError_AlreadyStuckTaskShortCircuits(t *testing.T) {
	t.Parallel()
	bus := &captureBus{}
	ctx := newStuckTestCtx("T-1", bus)
	ctx.stuckTaskIDs["T-1"] = true
	shutdown := make(chan struct{})

	ae := &agenterr.AgentError{Class: agenterr.Transient, Message: "server error", Backend: "claude"}
	cont := handleAutoTaskError(ctx, ae, errors.New("exit code 1"), shutdown)

	if !cont {
		t.Fatalf("handleAutoTaskError should continue the loop for already-stuck task (ExitReason=%q)", ctx.state.ExitReason)
	}
	if ctx.state.ShouldExit {
		t.Error("ShouldExit must remain false for already-stuck-task short-circuit")
	}
	if ctx.sameTaskFailures != 0 {
		t.Errorf("sameTaskFailures = %d, want 0 (already-stuck short-circuit must not advance counter)", ctx.sameTaskFailures)
	}
	if ctx.state.ConsecutiveErrors != 0 {
		t.Errorf("ConsecutiveErrors = %d, want 0 (already-stuck short-circuit must not count as error)", ctx.state.ConsecutiveErrors)
	}

	// Verify no TaskStuck event was emitted this time.
	for _, e := range bus.snapshot() {
		if e.Type == events.TaskStuck {
			t.Fatal("TaskStuck event must not be re-emitted for already-stuck task")
		}
	}
}

// TestHandleAutoTaskSuccess_StuckTaskReclaimedTreatedAsNoProgress verifies that
// when the agent "succeeds" (exit 0) but re-claims a task already in the stuck
// set, the loop does not count it as a completed task. Instead it clears the
// lock's TaskID and routes through handleNoProgress so ConsecutiveNoProgress
// grows until the loop exits cleanly.
func TestHandleAutoTaskSuccess_StuckTaskReclaimedTreatedAsNoProgress(t *testing.T) {
	t.Parallel()

	// Build a ctx where ReadLock returns a LockInfo with TaskID=T-1, and
	// where T-1 is already marked stuck. We use a stub worktree path so
	// agentClaimedTask returns true via the local filesystem branch. The
	// simplest way is to mock via LockBridge so agentClaimedTask reads
	// through the bridge instead of the filesystem.
	bridge := &stuckTestLockBridge{taskID: "T-1"}
	ctx := &autoLoopCtx{
		opts: AutoModeOptions{
			AgentName:   "test-agent",
			BackoffBase: 1 * time.Millisecond,
			TaskPause:   1 * time.Millisecond,
			EventBus:    events.NopBus{},
			LockBridge:  bridge,
		},
		state: &AutoModeState{},
		readLock: func() (*cli.LockInfo, error) {
			if bridge.cleared {
				return &cli.LockInfo{}, nil
			}
			return &cli.LockInfo{TaskID: "T-1"}, nil
		},
		clearTaskID: func() error {
			bridge.cleared = true
			return nil
		},
		stuckTaskIDs: map[string]bool{"T-1": true},
	}

	shutdown := make(chan struct{})
	cont := handleAutoTaskSuccess(ctx, "", time.Now(), time.Now(), shutdown)

	if !cont {
		t.Fatalf("handleAutoTaskSuccess should continue the loop for stuck re-claim (ExitReason=%q)", ctx.state.ExitReason)
	}
	if ctx.state.TasksCompleted != 0 {
		t.Errorf("TasksCompleted = %d, want 0 (stuck re-claim must not count as completion)", ctx.state.TasksCompleted)
	}
	if ctx.state.ConsecutiveNoProgress != 1 {
		t.Errorf("ConsecutiveNoProgress = %d, want 1 (stuck re-claim must register as no-progress)", ctx.state.ConsecutiveNoProgress)
	}
	if !bridge.cleared {
		t.Error("clearTaskID should have been called for stuck re-claim")
	}
}

// stuckTestLockBridge is a minimal cli.LockBridge stub for the stuck-reclaim
// test. Only ReadLock is exercised by agentClaimedTask.
type stuckTestLockBridge struct {
	taskID  string
	cleared bool
}

func (b *stuckTestLockBridge) ReadLock(agentName string) (*cli.LockInfo, error) {
	if b.cleared {
		return &cli.LockInfo{}, nil
	}
	return &cli.LockInfo{TaskID: b.taskID}, nil
}

func (b *stuckTestLockBridge) UpdateState(agentName, state string) error { return nil }
func (b *stuckTestLockBridge) UpdateTask(agentName, taskID, title string) error {
	b.taskID = taskID
	b.cleared = false
	return nil
}
func (b *stuckTestLockBridge) ClearTaskID(agentName string) error {
	b.cleared = true
	return nil
}
func (b *stuckTestLockBridge) UpdateClaudeSessionID(agentName, claudeSessionID string) error {
	return nil
}
func (b *stuckTestLockBridge) ClearClaudeSessionID(agentName string) error { return nil }

func TestTaskStuckEvent_RoundTrip(t *testing.T) {
	t.Parallel()
	original := events.TaskStuckData{
		TaskID:              "T-99",
		ConsecutiveFailures: 3,
		LastError:           "context canceled",
	}
	evt, err := events.NewEvent(events.TaskStuck, "agent-x", "task", "epic-1", original)
	if err != nil {
		t.Fatalf("NewEvent: %v", err)
	}
	if evt.Type != events.TaskStuck {
		t.Errorf("Type = %q, want %q", evt.Type, events.TaskStuck)
	}
	decoded, err := evt.DecodeData()
	if err != nil {
		t.Fatalf("DecodeData: %v", err)
	}
	got, ok := decoded.(*events.TaskStuckData)
	if !ok {
		t.Fatalf("expected *TaskStuckData, got %T", decoded)
	}
	if *got != original {
		t.Errorf("round-trip mismatch: got %+v, want %+v", *got, original)
	}
}
