package backends

// Unit tests for the claude RunTurn idle-output guard (startClaudeIdleGuard).
// They exercise the real helper without spawning a claude process. The guard's
// internal poll ticker is 5s, so the timing cases necessarily run a few seconds;
// the two timing tests run in parallel to overlap that wait.

import (
	"context"
	"testing"
	"time"

	"github.com/olesho/harness-wrapper/pkg/wrapper"
)

// Fires once output started and then stalled past the idle window: cancels the
// turn ctx and reports fired=true. This is the hang-recovery path.
func TestStartClaudeIdleGuard_FiresAfterOutputStalls(t *testing.T) {
	t.Parallel()
	turnCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	raw := &capturedOutput{}

	fired := startClaudeIdleGuard(turnCtx, cancel, raw, nil, 1*time.Second)
	raw.onActivity(wrapper.Snapshot{LastOutputAt: time.Now()}) // one burst of output, then silence

	select {
	case <-turnCtx.Done():
		if !fired.Load() {
			t.Fatalf("turn ctx cancelled but fired flag not set")
		}
	case <-time.After(12 * time.Second):
		t.Fatalf("idle guard did not fire after output stalled")
	}
}

// Does NOT fire when output never started (sawAct stays false): a turn that is
// merely slow to emit its first byte must not be killed by the guard.
func TestStartClaudeIdleGuard_DoesNotFireWithoutAnyOutput(t *testing.T) {
	t.Parallel()
	turnCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	raw := &capturedOutput{}

	fired := startClaudeIdleGuard(turnCtx, cancel, raw, nil, 1*time.Second)
	// never call raw.onActivity

	select {
	case <-turnCtx.Done():
		t.Fatalf("idle guard fired with no output ever produced (would kill a slow-to-start turn)")
	case <-time.After(7 * time.Second): // past at least one 5s poll tick
		if fired.Load() {
			t.Fatalf("fired flag set despite no output")
		}
	}
}

// idle<=0 disables the watchdog (no goroutine), but the onActivity wrapper is
// still installed and forwards to the caller's callback.
func TestStartClaudeIdleGuard_DisabledWhenIdleZero(t *testing.T) {
	turnCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	raw := &capturedOutput{}

	forwarded := 0
	fired := startClaudeIdleGuard(turnCtx, cancel, raw, func(wrapper.Snapshot) { forwarded++ }, 0)
	raw.onActivity(wrapper.Snapshot{})

	time.Sleep(200 * time.Millisecond)
	if fired.Load() {
		t.Fatalf("guard fired while disabled (idle=0)")
	}
	if turnCtx.Err() != nil {
		t.Fatalf("turn ctx cancelled while guard disabled")
	}
	if forwarded != 1 {
		t.Fatalf("caller onActivity not forwarded: got %d calls, want 1", forwarded)
	}
}
