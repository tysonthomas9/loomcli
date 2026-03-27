package webui

import (
	"testing"
	"time"
)

// TestScheduleKill_FiresAfterDelay verifies that ScheduleKill schedules a
// deferred kill that executes after the specified delay.
func TestScheduleKill_FiresAfterDelay(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	name := testSessionName(t)
	t.Cleanup(func() {
		mgr.Shutdown()
		killTmuxSession(t, name)
	})

	// Spawn a tmux session so there is something to kill.
	if _, err := mgr.Spawn(name, "", 80, 24); err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}
	if !mgr.SessionExists(name) {
		t.Fatal("expected tmux session to exist after Spawn")
	}

	// Schedule kill with a short delay.
	mgr.ScheduleKill(name, 100*time.Millisecond)

	// Wait for the kill to fire.
	time.Sleep(400 * time.Millisecond)

	if mgr.SessionExists(name) {
		t.Error("expected tmux session to be killed after ScheduleKill delay elapsed")
	}
}

// TestCancelPendingKill_CancelsPendingKill verifies that CancelPendingKill
// cancels a pending kill and returns true.
func TestCancelPendingKill_CancelsPendingKill(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	name := testSessionName(t)
	t.Cleanup(func() {
		mgr.Shutdown()
		killTmuxSession(t, name)
	})

	// Spawn a tmux session.
	if _, err := mgr.Spawn(name, "", 80, 24); err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}

	// Schedule kill with a longer delay.
	mgr.ScheduleKill(name, 2*time.Second)

	// Cancel it immediately.
	cancelled := mgr.CancelPendingKill(name)
	if !cancelled {
		t.Error("expected CancelPendingKill to return true for pending kill")
	}

	// Wait past the original delay to confirm the session is still alive.
	time.Sleep(300 * time.Millisecond)

	if !mgr.SessionExists(name) {
		t.Error("expected tmux session to still exist after CancelPendingKill")
	}
}

// TestCancelPendingKill_ReturnsFalseWhenNoPending verifies that
// CancelPendingKill returns false when no pending kill exists.
func TestCancelPendingKill_ReturnsFalseWhenNoPending(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	cancelled := mgr.CancelPendingKill("nonexistent-session")
	if cancelled {
		t.Error("expected CancelPendingKill to return false when no pending kill exists")
	}
}

// TestAttach_CancelsPendingKill verifies that Attach cancels a pending
// kill for the same session.
func TestAttach_CancelsPendingKill(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	name := testSessionName(t)
	t.Cleanup(func() {
		mgr.Shutdown()
		killTmuxSession(t, name)
	})

	// Create a session then detach so it has no active connections.
	session, err := mgr.Attach(name, "", 80, 24)
	if err != nil {
		t.Fatalf("Attach() error: %v", err)
	}
	if err := mgr.Detach(session.ConnID); err != nil {
		t.Fatalf("Detach() error: %v", err)
	}

	// Schedule a kill.
	mgr.ScheduleKill(name, 200*time.Millisecond)

	// Re-attach, which should cancel the pending kill.
	session2, err := mgr.Attach(name, "", 80, 24)
	if err != nil {
		t.Fatalf("second Attach() error: %v", err)
	}
	_ = session2

	// Verify the pending kill was cancelled.
	cancelled := mgr.CancelPendingKill(name)
	if cancelled {
		t.Error("expected no pending kill after Attach cancelled it")
	}

	// Wait past the original delay to confirm the session is still alive.
	time.Sleep(400 * time.Millisecond)

	if !mgr.SessionExists(name) {
		t.Error("expected tmux session to still exist after Attach cancelled pending kill")
	}
}

// TestScheduleKill_ReplacesExistingPending verifies that scheduling a kill
// for the same session replaces any existing pending kill.
func TestScheduleKill_ReplacesExistingPending(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	name := testSessionName(t)
	t.Cleanup(func() {
		mgr.Shutdown()
		killTmuxSession(t, name)
	})

	// Spawn a tmux session.
	if _, err := mgr.Spawn(name, "", 80, 24); err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}

	// Schedule a kill with a short delay.
	mgr.ScheduleKill(name, 100*time.Millisecond)

	// Replace it with a much longer delay.
	mgr.ScheduleKill(name, 5*time.Second)

	// Wait past the first delay — session should still be alive because
	// the second ScheduleKill replaced the first.
	time.Sleep(300 * time.Millisecond)

	if !mgr.SessionExists(name) {
		t.Error("expected tmux session to still exist after first ScheduleKill delay (replaced by second)")
	}

	// Cancel the second pending kill to clean up.
	mgr.CancelPendingKill(name)
}

// TestHasActiveConnections verifies that HasActiveConnections returns
// true/false correctly based on the state of connections.
func TestHasActiveConnections(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	name := testSessionName(t)
	t.Cleanup(func() {
		mgr.Shutdown()
		killTmuxSession(t, name)
	})

	// No connections initially.
	if mgr.HasActiveConnections(name) {
		t.Error("expected HasActiveConnections to return false with no connections")
	}

	// Attach a connection.
	session, err := mgr.Attach(name, "", 80, 24)
	if err != nil {
		t.Fatalf("Attach() error: %v", err)
	}

	if !mgr.HasActiveConnections(name) {
		t.Error("expected HasActiveConnections to return true after Attach")
	}

	// Detach the connection.
	if err := mgr.Detach(session.ConnID); err != nil {
		t.Fatalf("Detach() error: %v", err)
	}

	if mgr.HasActiveConnections(name) {
		t.Error("expected HasActiveConnections to return false after Detach")
	}
}

// TestScheduleKill_DoesNotKillWithActiveConnections verifies that ScheduleKill
// does not kill a session that has active connections when the timer fires.
func TestScheduleKill_DoesNotKillWithActiveConnections(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	name := testSessionName(t)
	t.Cleanup(func() {
		mgr.Shutdown()
		killTmuxSession(t, name)
	})

	// Attach a connection (creates the tmux session and has an active connection).
	session, err := mgr.Attach(name, "", 80, 24)
	if err != nil {
		t.Fatalf("Attach() error: %v", err)
	}
	_ = session

	// Schedule a kill with a short delay. The active connection should prevent the kill.
	mgr.ScheduleKill(name, 100*time.Millisecond)

	// Wait for the timer to fire.
	time.Sleep(400 * time.Millisecond)

	// Session should still exist because there's an active connection.
	if !mgr.SessionExists(name) {
		t.Error("expected tmux session to still exist because of active connections")
	}
}
