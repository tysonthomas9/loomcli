package main

import (
	"errors"
	"os/exec"
	"testing"
	"time"
)

// hasTmux reports whether tmux is available on the system.
func hasTmux() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// skipIfNoTmux skips the test if tmux is not installed.
func skipIfNoTmux(t *testing.T) {
	t.Helper()
	if !hasTmux() {
		t.Skip("tmux not available, skipping test")
	}
}

// killTmuxSession is a cleanup helper that kills a tmux session by name.
func killTmuxSession(t *testing.T, name string) {
	t.Helper()
	cmd := exec.Command("tmux", "kill-session", "-t", name)
	_ = cmd.Run() // ignore error if session doesn't exist
}

// tmuxSessionExists checks whether a tmux session with the given name exists.
func tmuxSessionExists(name string) bool {
	cmd := exec.Command("tmux", "has-session", "-t", name)
	return cmd.Run() == nil
}

// TestTerminalNewManagerTmuxNotFound verifies that NewTerminalManager returns
// ErrTmuxNotFound when tmux is not in PATH, or succeeds when it is.
func TestTerminalNewManagerTmuxNotFound(t *testing.T) {
	_, err := NewTerminalManager("", "")
	if hasTmux() {
		if err != nil {
			t.Fatalf("expected NewTerminalManager to succeed when tmux is installed, got: %v", err)
		}
	} else {
		if !errors.Is(err, ErrTmuxNotFound) {
			t.Fatalf("expected ErrTmuxNotFound, got: %v", err)
		}
	}
}

// TestTerminalNewManagerSuccess verifies that NewTerminalManager succeeds when
// tmux is available and returns a properly initialized manager.
func TestTerminalNewManagerSuccess(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "")
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	if mgr == nil {
		t.Fatal("NewTerminalManager() returned nil manager")
	}
	if mgr.tmuxPath == "" {
		t.Error("expected tmuxPath to be set")
	}
	if mgr.sessions == nil {
		t.Error("expected sessions map to be initialized")
	}
	if mgr.defaultCols != 80 {
		t.Errorf("expected defaultCols=80, got %d", mgr.defaultCols)
	}
	if mgr.defaultRows != 24 {
		t.Errorf("expected defaultRows=24, got %d", mgr.defaultRows)
	}
}

// TestTerminalAttach verifies that Attach creates a new tmux session,
// returns a non-nil PTY, and the tmux session is visible via has-session.
func TestTerminalAttach(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "")
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	name := "test-" + t.Name()
	t.Cleanup(func() {
		mgr.Shutdown()
		killTmuxSession(t, name)
	})

	session, err := mgr.Attach(name, "", 80, 24)
	if err != nil {
		t.Fatalf("Attach() error: %v", err)
	}
	if session == nil {
		t.Fatal("Attach() returned nil session")
	}
	if session.PTY == nil {
		t.Error("expected PTY to be non-nil")
	}
	if session.Name != name {
		t.Errorf("expected session name %q, got %q", name, session.Name)
	}
	if session.ConnID == "" {
		t.Error("expected ConnID to be set")
	}

	// Verify the tmux session exists.
	if !tmuxSessionExists(name) {
		t.Error("expected tmux session to exist after Attach")
	}
}

// TestTerminalMultipleAttach verifies that multiple Attach calls to the same
// tmux session succeed simultaneously without displacing each other.
func TestTerminalMultipleAttach(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "")
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	name := "test-" + t.Name()
	t.Cleanup(func() {
		mgr.Shutdown()
		killTmuxSession(t, name)
	})

	session1, err := mgr.Attach(name, "", 80, 24)
	if err != nil {
		t.Fatalf("first Attach() error: %v", err)
	}

	// Give tmux a moment to settle.
	time.Sleep(100 * time.Millisecond)

	session2, err := mgr.Attach(name, "", 80, 24)
	if err != nil {
		t.Fatalf("second Attach() error: %v", err)
	}

	// Both sessions should have different connection IDs.
	if session1.ConnID == session2.ConnID {
		t.Errorf("expected different ConnIDs, both are %q", session1.ConnID)
	}

	// Both sessions should have different PTY fds.
	if session1.PTY == session2.PTY {
		t.Error("expected different PTY fds for concurrent connections")
	}

	// The first session's PTY should still be usable (not closed).
	_, writeErr := session1.PTY.Write([]byte("test"))
	if writeErr != nil {
		t.Errorf("expected first session PTY to still be writable, got error: %v", writeErr)
	}

	// The tmux session should still exist.
	if !tmuxSessionExists(name) {
		t.Error("expected tmux session to still exist with multiple attachments")
	}
}

// TestTerminalResize verifies that Resize succeeds on an active connection.
func TestTerminalResize(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "")
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	name := "test-" + t.Name()
	t.Cleanup(func() {
		mgr.Shutdown()
		killTmuxSession(t, name)
	})

	session, err := mgr.Attach(name, "", 80, 24)
	if err != nil {
		t.Fatalf("Attach() error: %v", err)
	}

	// Give tmux a moment to settle before resizing.
	time.Sleep(100 * time.Millisecond)

	if err := mgr.Resize(session.ConnID, 120, 40); err != nil {
		t.Fatalf("Resize() error: %v", err)
	}
}

// TestTerminalDetach verifies that Detach closes the Go-side connection but leaves
// the tmux session alive.
func TestTerminalDetach(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "")
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	name := "test-" + t.Name()
	t.Cleanup(func() {
		killTmuxSession(t, name)
	})

	session, err := mgr.Attach(name, "", 80, 24)
	if err != nil {
		t.Fatalf("Attach() error: %v", err)
	}

	if err := mgr.Detach(session.ConnID); err != nil {
		t.Fatalf("Detach() error: %v", err)
	}

	// The tmux session should still exist.
	if !tmuxSessionExists(name) {
		t.Error("expected tmux session to still exist after Detach")
	}

	// The connection should be removed — Resize should fail.
	if err := mgr.Resize(session.ConnID, 100, 30); err == nil {
		t.Error("expected Resize to fail after Detach, got nil error")
	}
}

// TestTerminalDetachOneOfMany verifies that detaching one connection doesn't
// affect other connections to the same tmux session.
func TestTerminalDetachOneOfMany(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "")
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	name := "test-" + t.Name()
	t.Cleanup(func() {
		mgr.Shutdown()
		killTmuxSession(t, name)
	})

	session1, err := mgr.Attach(name, "", 80, 24)
	if err != nil {
		t.Fatalf("first Attach() error: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	session2, err := mgr.Attach(name, "", 80, 24)
	if err != nil {
		t.Fatalf("second Attach() error: %v", err)
	}

	// Detach the first connection.
	if err := mgr.Detach(session1.ConnID); err != nil {
		t.Fatalf("Detach(session1) error: %v", err)
	}

	// The second connection should still work.
	if err := mgr.Resize(session2.ConnID, 100, 30); err != nil {
		t.Errorf("expected Resize on session2 to succeed after detaching session1, got: %v", err)
	}

	// The tmux session should still exist.
	if !tmuxSessionExists(name) {
		t.Error("expected tmux session to still exist after detaching one connection")
	}
}

// TestTerminalShutdown verifies that Shutdown kills all tmux sessions and
// cleans up all connections.
func TestTerminalShutdown(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "")
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	name1 := "test-" + t.Name() + "-1"
	name2 := "test-" + t.Name() + "-2"
	t.Cleanup(func() {
		killTmuxSession(t, name1)
		killTmuxSession(t, name2)
	})

	_, err = mgr.Attach(name1, "", 80, 24)
	if err != nil {
		t.Fatalf("Attach(%q) error: %v", name1, err)
	}
	_, err = mgr.Attach(name2, "", 80, 24)
	if err != nil {
		t.Fatalf("Attach(%q) error: %v", name2, err)
	}

	if err := mgr.Shutdown(); err != nil {
		t.Fatalf("Shutdown() error: %v", err)
	}

	// Give tmux a moment to clean up.
	time.Sleep(200 * time.Millisecond)

	if tmuxSessionExists(name1) {
		t.Errorf("expected tmux session %q to be killed after Shutdown", name1)
	}
	if tmuxSessionExists(name2) {
		t.Errorf("expected tmux session %q to be killed after Shutdown", name2)
	}
}

// TestTerminalResizeNonexistent verifies that Resize returns an error for a
// connection that does not exist in the manager.
func TestTerminalResizeNonexistent(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "")
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	err = mgr.Resize("nonexistent:1", 80, 24)
	if err == nil {
		t.Fatal("expected Resize on nonexistent connection to return error")
	}
}

// TestTerminalDetachNonexistent verifies that Detach returns an error for a
// connection that does not exist in the manager.
func TestTerminalDetachNonexistent(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "")
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	err = mgr.Detach("nonexistent:1")
	if err == nil {
		t.Fatal("expected Detach on nonexistent connection to return error")
	}
}

// TestTerminalDefaultCommand verifies that NewTerminalManager stores the default
// command and Attach uses it when the client passes an empty command.
func TestTerminalDefaultCommand(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("bash", "")
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	if mgr.defaultCommand != "bash" {
		t.Errorf("expected defaultCommand=bash, got %q", mgr.defaultCommand)
	}

	name := "test-" + t.Name()
	t.Cleanup(func() {
		mgr.Shutdown()
		killTmuxSession(t, name)
	})

	// Call Attach with empty command - should use default "bash"
	session, err := mgr.Attach(name, "", 80, 24)
	if err != nil {
		t.Fatalf("Attach() error: %v", err)
	}
	if session.Command != "bash" {
		t.Errorf("expected session command=bash, got %q", session.Command)
	}
}

// TestTerminalMultipleManagersWithPrefixes verifies that two managers with different
// prefixes create isolated tmux sessions and shutdown of one doesn't affect the other.
func TestTerminalMultipleManagersWithPrefixes(t *testing.T) {
	skipIfNoTmux(t)

	mgr1, err := NewTerminalManager("", "8080")
	if err != nil {
		t.Fatalf("NewTerminalManager(8080) error: %v", err)
	}

	mgr2, err := NewTerminalManager("", "8081")
	if err != nil {
		t.Fatalf("NewTerminalManager(8081) error: %v", err)
	}

	sessionName := "test-isolation"
	t.Cleanup(func() {
		killTmuxSession(t, "8080-"+sessionName)
		killTmuxSession(t, "8081-"+sessionName)
	})

	sess1, err := mgr1.Attach(sessionName, "", 80, 24)
	if err != nil {
		t.Fatalf("mgr1.Attach() error: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	sess2, err := mgr2.Attach(sessionName, "", 80, 24)
	if err != nil {
		t.Fatalf("mgr2.Attach() error: %v", err)
	}

	// Should have different internal tmux names.
	if sess1.Name == sess2.Name {
		t.Errorf("expected different tmux session names, both are %q", sess1.Name)
	}
	if sess1.Name != "8080-"+sessionName {
		t.Errorf("expected session1 name %q, got %q", "8080-"+sessionName, sess1.Name)
	}
	if sess2.Name != "8081-"+sessionName {
		t.Errorf("expected session2 name %q, got %q", "8081-"+sessionName, sess2.Name)
	}

	// Both tmux sessions should exist.
	if !tmuxSessionExists("8080-" + sessionName) {
		t.Error("expected 8080-test-isolation to exist")
	}
	if !tmuxSessionExists("8081-" + sessionName) {
		t.Error("expected 8081-test-isolation to exist")
	}

	// Shutdown mgr1 — should only kill 8080 sessions.
	if err := mgr1.Shutdown(); err != nil {
		t.Fatalf("mgr1.Shutdown() error: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	if tmuxSessionExists("8080-" + sessionName) {
		t.Error("expected 8080-test-isolation to be killed after mgr1.Shutdown()")
	}
	if !tmuxSessionExists("8081-" + sessionName) {
		t.Error("expected 8081-test-isolation to still exist after mgr1.Shutdown()")
	}

	// Cleanup mgr2.
	mgr2.Shutdown()
}
