package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// TestRunLeaseHeartbeatLoop_TicksUntilCanceled confirms the loop calls the
// heartbeat function on each tick and stops cleanly when its context is
// canceled. The interval is tiny so the test stays fast.
func TestRunLeaseHeartbeatLoop_TicksUntilCanceled(t *testing.T) {
	var calls atomic.Int32
	heartbeat := func() error {
		calls.Add(1)
		return nil
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		runLeaseHeartbeatLoop(ctx, heartbeat, 20*time.Millisecond)
		close(done)
	}()

	// Wait long enough for ~5 ticks.
	time.Sleep(120 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat loop did not exit after cancel")
	}

	if got := calls.Load(); got < 3 {
		t.Errorf("expected at least 3 heartbeat calls in 120ms at 20ms tick, got %d", got)
	}
}

// TestRunLeaseHeartbeatLoop_SwallowsErrors makes sure that a heartbeat
// failure does NOT abort the loop. The deliberate behavior is to keep
// trying — a transient error must not kill an active worker session.
func TestRunLeaseHeartbeatLoop_SwallowsErrors(t *testing.T) {
	var calls atomic.Int32
	heartbeat := func() error {
		calls.Add(1)
		return errors.New("simulated daemon outage")
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		runLeaseHeartbeatLoop(ctx, heartbeat, 15*time.Millisecond)
		close(done)
	}()

	time.Sleep(80 * time.Millisecond)
	cancel()
	<-done

	if got := calls.Load(); got < 3 {
		t.Errorf("loop stopped on errors: expected ≥3 attempts, got %d", got)
	}
}

// TestStartBackgroundLeaseHeartbeat_NoEnv exercises the early-return path:
// when the daemon-spawn env vars aren't set, the helper must return a
// no-op stopper and start no goroutine. Calling the returned function
// must not panic.
func TestStartBackgroundLeaseHeartbeat_NoEnv(t *testing.T) {
	// Force a clean env. t.Setenv reverts on cleanup.
	t.Setenv("LOOM_DAEMON_SOCKET", "")
	t.Setenv("LOOM_AGENT_LEASE_ID", "")
	t.Setenv("LOOM_AGENT_LEASE_TOKEN", "")
	t.Setenv("LOOM_AGENT_NAME", "")
	t.Setenv("LOOM_SESSION_ID", "")

	stop := startBackgroundLeaseHeartbeat()
	if stop == nil {
		t.Fatal("expected non-nil stop function even when no daemon context")
	}
	// Calling stop twice must be safe.
	stop()
	stop()
}
