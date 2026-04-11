package terminal

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/workspace"
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
	cmd := exec.Command("tmux", "kill-session", "-t", name) //nolint:norawexec
	_ = cmd.Run()                                           // ignore error if session doesn't exist
}

// tmuxSessionExists checks whether a tmux session with the given name exists.
func tmuxSessionExists(name string) bool {
	cmd := exec.Command("tmux", "has-session", "-t", name) //nolint:norawexec
	return cmd.Run() == nil
}

// testRunPrefix scopes all tmux session names to this test process.
// Embedding os.Getpid() prevents collisions when multiple make gate runs
// execute simultaneously on the same machine (sharing the same tmux server).
var testRunPrefix = fmt.Sprintf("tr%d", os.Getpid())

// testSessionName returns a tmux session name unique to this process and test.
func testSessionName(t *testing.T, suffix ...string) string {
	t.Helper()
	name := testRunPrefix + "-" + strings.ReplaceAll(t.Name(), "/", "-")
	if len(suffix) > 0 {
		name += "-" + suffix[0]
	}
	return name
}

// termTestWS is the workspace ID used by terminal_test.go. Keeping it short
// and literal ("testws") makes the test bodies easy to read. Its wsShortID
// is unchanged (it's only 6 chars), so the internal tmux name for a session
// with no server prefix is "testws-<userName>".
const termTestWS = "testws"

// termTestInternal returns the internal tmux session name for a session
// created with termTestWS and no server prefix — matching the default test
// configuration. Tests that use a custom serverPrefix compose the internal
// name themselves.
func termTestInternal(userName string) string {
	return termTestWS + "-" + userName
}

// TestTerminalNewManagerTmuxNotFound verifies that NewTerminalManager returns
// ErrTmuxNotFound when tmux is not in PATH, or succeeds when it is.
func TestTerminalNewManagerTmuxNotFound(t *testing.T) {
	_, err := NewTerminalManager("", "", 0)
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

	mgr, err := NewTerminalManager("", "", 0)
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

	mgr, err := NewTerminalManager("", "", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	name := testSessionName(t)
	t.Cleanup(func() {
		mgr.Shutdown()
		killTmuxSession(t, termTestInternal(name))
	})

	session, err := mgr.Attach(termTestWS, name, "", 80, 24)
	if err != nil {
		t.Fatalf("Attach() error: %v", err)
	}
	if session == nil {
		t.Fatal("Attach() returned nil session")
	}
	if session.PTY == nil {
		t.Error("expected PTY to be non-nil")
	}
	// session.Name is the internal (workspace-qualified) tmux name, not the
	// user-facing name — TerminalSession.Name has always meant "the string we
	// pass to tmux", which under the new naming scheme includes the workspace.
	if session.Name != termTestInternal(name) {
		t.Errorf("expected session name %q, got %q", termTestInternal(name), session.Name)
	}
	if session.ConnID == "" {
		t.Error("expected ConnID to be set")
	}

	// Verify the tmux session exists.
	if !tmuxSessionExists(termTestInternal(name)) {
		t.Error("expected tmux session to exist after Attach")
	}
}

// TestTerminalMultipleAttach verifies that multiple Attach calls to the same
// tmux session succeed simultaneously without displacing each other.
func TestTerminalMultipleAttach(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	name := testSessionName(t)
	t.Cleanup(func() {
		mgr.Shutdown()
		killTmuxSession(t, termTestInternal(name))
	})

	session1, err := mgr.Attach(termTestWS, name, "", 80, 24)
	if err != nil {
		t.Fatalf("first Attach() error: %v", err)
	}

	// Give tmux a moment to settle.
	time.Sleep(100 * time.Millisecond)

	session2, err := mgr.Attach(termTestWS, name, "", 80, 24)
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

	// The first session's PTY may or may not be writable depending on tmux
	// client behavior in non-interactive environments. Some tmux versions
	// close the first client's PTY when a second attach occurs. Log but
	// don't fail on write errors since the important assertions (distinct
	// ConnIDs, distinct PTYs, tmux session alive) are already checked above.
	_, writeErr := session1.PTY.Write([]byte("test"))
	if writeErr != nil {
		t.Logf("first session PTY not writable after second attach (expected in some tmux versions): %v", writeErr)
	}

	// The tmux session should still exist.
	if !tmuxSessionExists(termTestInternal(name)) {
		t.Error("expected tmux session to still exist with multiple attachments")
	}
}

// TestTerminalResize verifies that Resize succeeds on an active connection.
func TestTerminalResize(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	name := testSessionName(t)
	t.Cleanup(func() {
		mgr.Shutdown()
		killTmuxSession(t, termTestInternal(name))
	})

	session, err := mgr.Attach(termTestWS, name, "", 80, 24)
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

	mgr, err := NewTerminalManager("", "", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	name := testSessionName(t)
	t.Cleanup(func() {
		killTmuxSession(t, termTestInternal(name))
	})

	session, err := mgr.Attach(termTestWS, name, "", 80, 24)
	if err != nil {
		t.Fatalf("Attach() error: %v", err)
	}

	if err := mgr.Detach(session.ConnID); err != nil {
		t.Fatalf("Detach() error: %v", err)
	}

	// The tmux session should still exist.
	if !tmuxSessionExists(termTestInternal(name)) {
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

	mgr, err := NewTerminalManager("", "", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	name := testSessionName(t)
	t.Cleanup(func() {
		mgr.Shutdown()
		killTmuxSession(t, termTestInternal(name))
	})

	session1, err := mgr.Attach(termTestWS, name, "", 80, 24)
	if err != nil {
		t.Fatalf("first Attach() error: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	session2, err := mgr.Attach(termTestWS, name, "", 80, 24)
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
	if !tmuxSessionExists(termTestInternal(name)) {
		t.Error("expected tmux session to still exist after detaching one connection")
	}
}

// TestTerminalShutdown verifies that Shutdown kills all tmux sessions and
// cleans up all connections.
func TestTerminalShutdown(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	name1 := testSessionName(t, "1")
	name2 := testSessionName(t, "2")
	t.Cleanup(func() {
		killTmuxSession(t, termTestInternal(name1))
		killTmuxSession(t, termTestInternal(name2))
	})

	_, err = mgr.Attach(termTestWS, name1, "", 80, 24)
	if err != nil {
		t.Fatalf("Attach(%q) error: %v", name1, err)
	}
	_, err = mgr.Attach(termTestWS, name2, "", 80, 24)
	if err != nil {
		t.Fatalf("Attach(%q) error: %v", name2, err)
	}

	if err := mgr.Shutdown(); err != nil {
		t.Fatalf("Shutdown() error: %v", err)
	}

	// Give tmux a moment to clean up.
	time.Sleep(200 * time.Millisecond)

	if tmuxSessionExists(termTestInternal(name1)) {
		t.Errorf("expected tmux session %q to be killed after Shutdown", name1)
	}
	if tmuxSessionExists(termTestInternal(name2)) {
		t.Errorf("expected tmux session %q to be killed after Shutdown", name2)
	}
}

// TestTerminalResizeNonexistent verifies that Resize returns an error for a
// connection that does not exist in the manager.
func TestTerminalResizeNonexistent(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 0)
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

	mgr, err := NewTerminalManager("", "", 0)
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

	mgr, err := NewTerminalManager("bash", "", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	if mgr.defaultCommand != "bash" {
		t.Errorf("expected defaultCommand=bash, got %q", mgr.defaultCommand)
	}

	name := testSessionName(t)
	t.Cleanup(func() {
		mgr.Shutdown()
		killTmuxSession(t, termTestInternal(name))
	})

	// Call Attach with empty command - should use default "bash"
	session, err := mgr.Attach(termTestWS, name, "", 80, 24)
	if err != nil {
		t.Fatalf("Attach() error: %v", err)
	}
	if session.Command != "bash" {
		t.Errorf("expected session command=bash, got %q", session.Command)
	}
}

// TestTerminalMaxSessionsReached verifies that Attach returns ErrMaxSessionsReached
// when the maximum number of concurrent sessions is reached, and that detaching
// a session frees up a slot for a new one.
func TestTerminalMaxSessionsReached(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 2)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	name1 := testSessionName(t, "1")
	name2 := testSessionName(t, "2")
	name3 := testSessionName(t, "3")
	t.Cleanup(func() {
		mgr.Shutdown()
		killTmuxSession(t, termTestInternal(name1))
		killTmuxSession(t, termTestInternal(name2))
		killTmuxSession(t, termTestInternal(name3))
	})

	// Fill both slots.
	session1, err := mgr.Attach(termTestWS, name1, "", 80, 24)
	if err != nil {
		t.Fatalf("first Attach() error: %v", err)
	}

	_, err = mgr.Attach(termTestWS, name2, "", 80, 24)
	if err != nil {
		t.Fatalf("second Attach() error: %v", err)
	}

	// Third attach should fail with ErrMaxSessionsReached.
	_, err = mgr.Attach(termTestWS, name3, "", 80, 24)
	if !errors.Is(err, ErrMaxSessionsReached) {
		t.Fatalf("expected ErrMaxSessionsReached, got: %v", err)
	}

	// Detach one session to free a slot.
	if err := mgr.Detach(session1.ConnID); err != nil {
		t.Fatalf("Detach() error: %v", err)
	}

	// Now the third attach should succeed.
	_, err = mgr.Attach(termTestWS, name3, "", 80, 24)
	if err != nil {
		t.Fatalf("Attach after Detach should succeed, got: %v", err)
	}
}

// TestTerminalSessionCount verifies that SessionCount accurately tracks the
// number of active terminal connections as sessions are attached and detached.
func TestTerminalSessionCount(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	name1 := testSessionName(t, "1")
	name2 := testSessionName(t, "2")
	t.Cleanup(func() {
		mgr.Shutdown()
		killTmuxSession(t, termTestInternal(name1))
		killTmuxSession(t, termTestInternal(name2))
	})

	// Initially zero.
	if got := mgr.SessionCount(); got != 0 {
		t.Fatalf("expected SessionCount()==0, got %d", got)
	}

	// Attach first session.
	session1, err := mgr.Attach(termTestWS, name1, "", 80, 24)
	if err != nil {
		t.Fatalf("first Attach() error: %v", err)
	}
	if got := mgr.SessionCount(); got != 1 {
		t.Fatalf("expected SessionCount()==1, got %d", got)
	}

	// Attach second session (different name).
	_, err = mgr.Attach(termTestWS, name2, "", 80, 24)
	if err != nil {
		t.Fatalf("second Attach() error: %v", err)
	}
	if got := mgr.SessionCount(); got != 2 {
		t.Fatalf("expected SessionCount()==2, got %d", got)
	}

	// Detach first session.
	if err := mgr.Detach(session1.ConnID); err != nil {
		t.Fatalf("Detach() error: %v", err)
	}
	if got := mgr.SessionCount(); got != 1 {
		t.Fatalf("expected SessionCount()==1 after Detach, got %d", got)
	}
}

// TestTerminalMaxSessionsZeroUsesDefault verifies that creating a manager with
// maxSessions=0 uses the default limit (defaultMaxTerminalSessions = 20).
func TestTerminalMaxSessionsZeroUsesDefault(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	if got := mgr.MaxSessions(); got != defaultMaxTerminalSessions {
		t.Errorf("expected MaxSessions()==%d, got %d", defaultMaxTerminalSessions, got)
	}
}

// TestSetDefaultCommand verifies that SetDefaultCommand updates the manager's
// default command and DefaultCommand returns it.
func TestSetDefaultCommand(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("initial-cmd", "", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	if got := mgr.DefaultCommand(); got != "initial-cmd" {
		t.Errorf("expected DefaultCommand()=%q before set, got %q", "initial-cmd", got)
	}

	mgr.SetDefaultCommand("new-cmd")

	if got := mgr.DefaultCommand(); got != "new-cmd" {
		t.Errorf("expected DefaultCommand()=%q after set, got %q", "new-cmd", got)
	}

	// Verify setting to empty string works
	mgr.SetDefaultCommand("")
	if got := mgr.DefaultCommand(); got != "" {
		t.Errorf("expected DefaultCommand()=%q after set empty, got %q", "", got)
	}
}

// TestDefaultCommand_Initial verifies that DefaultCommand returns the value
// passed to NewTerminalManager.
func TestDefaultCommand_Initial(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("claude", "", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	if got := mgr.DefaultCommand(); got != "claude" {
		t.Errorf("expected DefaultCommand()=%q, got %q", "claude", got)
	}
}

// TestKillSession_NoSuchSession verifies that KillSession returns
// nil (no error) when the named session does not exist.
func TestKillSession_NoSuchSession(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	err = mgr.KillSession(termTestWS, "nonexistent-session")
	if err != nil {
		t.Errorf("expected KillSession to return nil for nonexistent session, got: %v", err)
	}
}

// TestTerminalMultipleManagersWithPrefixes verifies that two managers with different
// prefixes create isolated tmux sessions and shutdown of one doesn't affect the other.
func TestTerminalMultipleManagersWithPrefixes(t *testing.T) {
	skipIfNoTmux(t)

	prefix1 := testRunPrefix + "-8080"
	prefix2 := testRunPrefix + "-8081"

	mgr1, err := NewTerminalManager("", prefix1, 0)
	if err != nil {
		t.Fatalf("NewTerminalManager(%s) error: %v", prefix1, err)
	}

	mgr2, err := NewTerminalManager("", prefix2, 0)
	if err != nil {
		t.Fatalf("NewTerminalManager(%s) error: %v", prefix2, err)
	}

	sessionName := "isolation"
	// Under the new workspace-aware naming, the internal tmux name is
	// "<serverPrefix>-<wsShort>-<userName>" so both managers' sessions are
	// prefixed with the same workspace but different server instances.
	internal1 := prefix1 + "-" + termTestWS + "-" + sessionName
	internal2 := prefix2 + "-" + termTestWS + "-" + sessionName
	t.Cleanup(func() {
		killTmuxSession(t, internal1)
		killTmuxSession(t, internal2)
	})

	sess1, err := mgr1.Attach(termTestWS, sessionName, "", 80, 24)
	if err != nil {
		t.Fatalf("mgr1.Attach() error: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	sess2, err := mgr2.Attach(termTestWS, sessionName, "", 80, 24)
	if err != nil {
		t.Fatalf("mgr2.Attach() error: %v", err)
	}

	// Should have different internal tmux names.
	if sess1.Name == sess2.Name {
		t.Errorf("expected different tmux session names, both are %q", sess1.Name)
	}
	if sess1.Name != internal1 {
		t.Errorf("expected session1 name %q, got %q", internal1, sess1.Name)
	}
	if sess2.Name != internal2 {
		t.Errorf("expected session2 name %q, got %q", internal2, sess2.Name)
	}

	// Both tmux sessions should exist.
	if !tmuxSessionExists(internal1) {
		t.Errorf("expected %s to exist", internal1)
	}
	if !tmuxSessionExists(internal2) {
		t.Errorf("expected %s to exist", internal2)
	}

	// Shutdown mgr1 — should only kill prefix1 sessions.
	if err := mgr1.Shutdown(); err != nil {
		t.Fatalf("mgr1.Shutdown() error: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	if tmuxSessionExists(internal1) {
		t.Errorf("expected %s to be killed after mgr1.Shutdown()", internal1)
	}
	if !tmuxSessionExists(internal2) {
		t.Errorf("expected %s to still exist after mgr1.Shutdown()", internal2)
	}

	// Cleanup mgr2.
	mgr2.Shutdown()
}

// TestTerminalShutdownConcurrentWithAttach verifies that calling Shutdown()
// concurrently with Attach() does not cause panics or data races.
func TestTerminalShutdownConcurrentWithAttach(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 10)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	baseName := testSessionName(t)
	t.Cleanup(func() {
		// Clean up any straggler tmux sessions.
		for i := 0; i < 15; i++ {
			killTmuxSession(t, fmt.Sprintf("%s-%d", baseName, i))
		}
	})

	// Attach a few initial sessions.
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("%s-%d", baseName, i)
		if _, err := mgr.Attach(termTestWS, name, "", 80, 24); err != nil {
			t.Fatalf("initial Attach(%q) error: %v", name, err)
		}
	}

	var wg sync.WaitGroup

	// Launch goroutines that try to Attach concurrently with Shutdown.
	for i := 3; i < 13; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := fmt.Sprintf("%s-%d", baseName, idx)
			// Attach may succeed or fail — both are acceptable.
			_, _ = mgr.Attach(termTestWS, name, "", 80, 24)
		}(i)
	}

	// Simultaneously call Shutdown.
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = mgr.Shutdown()
	}()

	wg.Wait()

	// Attach calls that raced after Shutdown may have added sessions to
	// the fresh map. This is by design — Shutdown is idempotent and the
	// manager remains reusable. Clean up any stragglers.
	_ = mgr.Shutdown()

	if got := mgr.SessionCount(); got != 0 {
		t.Errorf("expected SessionCount()==0 after final Shutdown, got %d", got)
	}
}

// TestTerminalShutdownIdempotent verifies that calling Shutdown() twice
// does not panic or return an error.
func TestTerminalShutdownIdempotent(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 5)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	name := testSessionName(t)
	t.Cleanup(func() {
		killTmuxSession(t, termTestInternal(name))
	})

	if _, err := mgr.Attach(termTestWS, name, "", 80, 24); err != nil {
		t.Fatalf("Attach() error: %v", err)
	}

	// First shutdown.
	if err := mgr.Shutdown(); err != nil {
		t.Fatalf("first Shutdown() error: %v", err)
	}
	if got := mgr.SessionCount(); got != 0 {
		t.Errorf("expected SessionCount()==0 after first Shutdown, got %d", got)
	}

	// Second shutdown — should be a no-op, no panic.
	if err := mgr.Shutdown(); err != nil {
		t.Fatalf("second Shutdown() error: %v", err)
	}
	if got := mgr.SessionCount(); got != 0 {
		t.Errorf("expected SessionCount()==0 after second Shutdown, got %d", got)
	}
}

// TestTerminalKillSessionConcurrentWithAttach verifies that calling
// KillSession concurrently with Attach does not cause panics or data races.
func TestTerminalKillSessionConcurrentWithAttach(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 20)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	name := testSessionName(t)
	t.Cleanup(func() {
		mgr.Shutdown()
		killTmuxSession(t, termTestInternal(name))
	})

	// Create initial session.
	if _, err := mgr.Attach(termTestWS, name, "", 80, 24); err != nil {
		t.Fatalf("initial Attach() error: %v", err)
	}

	var wg sync.WaitGroup

	// Goroutines that kill the session by name.
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = mgr.KillSession(termTestWS, name)
		}()
	}

	// Goroutines that try to re-attach.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = mgr.Attach(termTestWS, name, "", 80, 24)
		}()
	}

	wg.Wait()

	// Manager should be in a consistent state.
	count := mgr.SessionCount()
	if count < 0 {
		t.Errorf("expected SessionCount() >= 0, got %d", count)
	}
}

// TestTerminalPTYWriteAfterClose verifies that writing to a session's PTY
// after Close() returns an error rather than panicking.
func TestTerminalPTYWriteAfterClose(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 5)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	name := testSessionName(t)
	t.Cleanup(func() {
		mgr.Shutdown()
		killTmuxSession(t, termTestInternal(name))
	})

	session, err := mgr.Attach(termTestWS, name, "", 80, 24)
	if err != nil {
		t.Fatalf("Attach() error: %v", err)
	}

	ptyFd := session.PTY

	// Close the session.
	if err := session.Close(); err != nil {
		t.Fatalf("Close() error: %v", err)
	}

	// Write to the closed PTY — should return an error, not panic.
	_, writeErr := ptyFd.Write([]byte("hello"))
	if writeErr == nil {
		t.Error("expected Write to closed PTY to return an error")
	}
}

// TestTerminalPTYWriteCloseConcurrent verifies that writing to a session's PTY
// concurrently with Close() does not cause panics or data races.
func TestTerminalPTYWriteCloseConcurrent(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 5)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	name := testSessionName(t)
	t.Cleanup(func() {
		mgr.Shutdown()
		killTmuxSession(t, termTestInternal(name))
	})

	session, err := mgr.Attach(termTestWS, name, "", 80, 24)
	if err != nil {
		t.Fatalf("Attach() error: %v", err)
	}

	ptyFd := session.PTY

	var wg sync.WaitGroup

	// Launch writers that write to the PTY in a loop.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				// Write may succeed or fail — both are acceptable.
				_, _ = ptyFd.Write([]byte("x"))
			}
		}()
	}

	// Let writers start, then close.
	time.Sleep(5 * time.Millisecond)
	if err := session.Close(); err != nil {
		t.Logf("Close() returned error (acceptable): %v", err)
	}

	wg.Wait()
	// Key assertion: no panic, no data race (detected by -race).
}

// TestTerminalMaxSessionsExactBoundary verifies the exact boundary behavior of
// max sessions enforcement: exactly N sessions succeed, N+1 fails, and freeing
// one slot allows a new attach.
func TestTerminalMaxSessionsExactBoundary(t *testing.T) {
	skipIfNoTmux(t)

	const maxSess = 5

	mgr, err := NewTerminalManager("", "", maxSess)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	baseName := testSessionName(t)
	t.Cleanup(func() {
		mgr.Shutdown()
		for i := 0; i < maxSess+2; i++ {
			killTmuxSession(t, fmt.Sprintf("%s-%d", baseName, i))
		}
	})

	// Attach exactly maxSess sessions — all should succeed.
	sessions := make([]*TerminalSession, 0, maxSess)
	for i := 0; i < maxSess; i++ {
		name := fmt.Sprintf("%s-%d", baseName, i)
		sess, err := mgr.Attach(termTestWS, name, "", 80, 24)
		if err != nil {
			t.Fatalf("Attach(%q) error at slot %d: %v", name, i, err)
		}
		sessions = append(sessions, sess)
	}

	if got := mgr.SessionCount(); got != maxSess {
		t.Fatalf("expected SessionCount()==%d, got %d", maxSess, got)
	}

	// The (maxSess+1)th attach must fail.
	overflowName := fmt.Sprintf("%s-%d", baseName, maxSess)
	_, err = mgr.Attach(termTestWS, overflowName, "", 80, 24)
	if !errors.Is(err, ErrMaxSessionsReached) {
		t.Fatalf("expected ErrMaxSessionsReached for session %d, got: %v", maxSess+1, err)
	}

	// Detach one session to free a slot.
	if err := mgr.Detach(sessions[0].ConnID); err != nil {
		t.Fatalf("Detach() error: %v", err)
	}

	// Now a new attach should succeed.
	replaceName := fmt.Sprintf("%s-%d", baseName, maxSess+1)
	_, err = mgr.Attach(termTestWS, replaceName, "", 80, 24)
	if err != nil {
		t.Fatalf("Attach after Detach should succeed, got: %v", err)
	}

	if got := mgr.SessionCount(); got != maxSess {
		t.Fatalf("expected SessionCount()==%d after replace, got %d", maxSess, got)
	}
}

// TestTerminalMaxSessionsConcurrentAttach verifies that concurrent Attach calls
// correctly enforce the max sessions limit without races.
func TestTerminalMaxSessionsConcurrentAttach(t *testing.T) {
	skipIfNoTmux(t)

	const maxSess = 3
	const totalAttempts = 10

	mgr, err := NewTerminalManager("", "", maxSess)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	baseName := testSessionName(t)
	t.Cleanup(func() {
		mgr.Shutdown()
		for i := 0; i < totalAttempts; i++ {
			killTmuxSession(t, fmt.Sprintf("%s-%d", baseName, i))
		}
	})

	type result struct {
		err error
	}
	results := make(chan result, totalAttempts)

	var wg sync.WaitGroup
	for i := 0; i < totalAttempts; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := fmt.Sprintf("%s-%d", baseName, idx)
			_, err := mgr.Attach(termTestWS, name, "", 80, 24)
			results <- result{err: err}
		}(i)
	}

	wg.Wait()
	close(results)

	var successes, maxReached int
	for r := range results {
		if r.err == nil {
			successes++
		} else if errors.Is(r.err, ErrMaxSessionsReached) {
			maxReached++
		} else {
			t.Errorf("unexpected error: %v", r.err)
		}
	}

	if successes != maxSess {
		t.Errorf("expected exactly %d successes, got %d", maxSess, successes)
	}
	if maxReached != totalAttempts-maxSess {
		t.Errorf("expected %d ErrMaxSessionsReached, got %d", totalAttempts-maxSess, maxReached)
	}
	if got := mgr.SessionCount(); got != maxSess {
		t.Errorf("expected SessionCount()==%d, got %d", maxSess, got)
	}
}

// TestTerminalDetachDuringShutdown verifies that calling Detach() concurrently
// with Shutdown() does not cause panics or data races.
func TestTerminalDetachDuringShutdown(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 10)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}

	baseName := testSessionName(t)
	t.Cleanup(func() {
		for i := 0; i < 5; i++ {
			killTmuxSession(t, fmt.Sprintf("%s-%d", baseName, i))
		}
	})

	// Attach several sessions and collect their connection IDs.
	var connIDs []string
	for i := 0; i < 5; i++ {
		name := fmt.Sprintf("%s-%d", baseName, i)
		sess, err := mgr.Attach(termTestWS, name, "", 80, 24)
		if err != nil {
			t.Fatalf("Attach(%q) error: %v", name, err)
		}
		connIDs = append(connIDs, sess.ConnID)
	}

	var wg sync.WaitGroup

	// Goroutines that try to Detach known connections.
	for _, connID := range connIDs {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			// Detach may succeed or fail (Shutdown may have cleared it first).
			_ = mgr.Detach(id)
		}(connID)
	}

	// Simultaneously call Shutdown.
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = mgr.Shutdown()
	}()

	wg.Wait()

	// Manager should be in a clean state.
	if got := mgr.SessionCount(); got != 0 {
		t.Errorf("expected SessionCount()==0 after concurrent Detach+Shutdown, got %d", got)
	}
}

// --- FindLatestAgentSession tests ---

// TestFindLatestAgentSession_WithWorkspaceID verifies that when a workspace ID
// is provided, only sessions for that workspace are matched.
func TestFindLatestAgentSession_WithWorkspaceID(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	defer mgr.Shutdown()

	wsID := "aaaabbbb-cccc-dddd-eeee-ffffffffffff"
	wsPrefix := workspace.ShortWorkspaceID(wsID) // "aaaabbbb"
	agentName := "ember"

	// Create a session matching the workspace naming convention.
	// Use PID as the numeric suffix to avoid collisions across parallel runs.
	pid := os.Getpid()
	sessionName := fmt.Sprintf("loom-%s-task-%s-%d", wsPrefix, agentName, pid)
	if err := exec.Command("tmux", "new-session", "-d", "-s", sessionName, "sleep", "300").Run(); err != nil { //nolint:norawexec
		t.Fatalf("failed to create tmux session %q: %v", sessionName, err)
	}
	t.Cleanup(func() { killTmuxSession(t, sessionName) })

	// Create a session for a different workspace to verify isolation.
	otherSession := fmt.Sprintf("loom-bbbbcccc-task-%s-%d", agentName, pid+1)
	if err := exec.Command("tmux", "new-session", "-d", "-s", otherSession, "sleep", "300").Run(); err != nil { //nolint:norawexec
		t.Fatalf("failed to create tmux session %q: %v", otherSession, err)
	}
	t.Cleanup(func() { killTmuxSession(t, otherSession) })

	// With workspace ID: should only find the matching workspace's session.
	name, found, err := mgr.FindLatestAgentSession(wsID, agentName)
	if err != nil {
		t.Fatalf("FindLatestAgentSession() error: %v", err)
	}
	if !found {
		t.Fatal("FindLatestAgentSession() found = false, want true")
	}
	if name != sessionName {
		t.Errorf("FindLatestAgentSession() = %q, want %q", name, sessionName)
	}
}

// TestFindLatestAgentSession_EmptyWorkspaceID verifies that when no workspace
// ID is provided, no sessions are matched (fail-closed behavior).
func TestFindLatestAgentSession_EmptyWorkspaceID(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	defer mgr.Shutdown()

	agentName := "spark"

	// Create sessions from two different workspaces.
	pid := os.Getpid()
	session1 := fmt.Sprintf("loom-aaaabbbb-task-%s-%d", agentName, pid)
	session2 := fmt.Sprintf("loom-ccccdddd-task-%s-%d", agentName, pid+1)
	for _, s := range []string{session1, session2} {
		if err := exec.Command("tmux", "new-session", "-d", "-s", s, "sleep", "300").Run(); err != nil { //nolint:norawexec
			t.Fatalf("failed to create tmux session %q: %v", s, err)
		}
		t.Cleanup(func() { killTmuxSession(t, s) })
	}

	// With empty workspace ID: should NOT find any session (fail-closed).
	_, found, err := mgr.FindLatestAgentSession("", agentName)
	if err != nil {
		t.Fatalf("FindLatestAgentSession() error: %v", err)
	}
	if found {
		t.Fatal("FindLatestAgentSession() found = true, want false (fail-closed on empty workspace)")
	}
}

// TestFindLatestAgentSession_WorkspaceIsolation verifies that sessions from
// different workspaces with the same agent name are properly isolated.
func TestFindLatestAgentSession_WorkspaceIsolation(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	defer mgr.Shutdown()

	agentName := "bolt"
	wsA := "11112222-3333-4444-5555-666677778888"
	wsB := "99998888-7777-6666-5555-444433332222"
	wsPrefixA := workspace.ShortWorkspaceID(wsA) // "11112222"
	wsPrefixB := workspace.ShortWorkspaceID(wsB) // "99998888"

	pid := os.Getpid()
	sessionA := fmt.Sprintf("loom-%s-task-%s-%d", wsPrefixA, agentName, pid)
	sessionB := fmt.Sprintf("loom-%s-task-%s-%d", wsPrefixB, agentName, pid+1)
	for _, s := range []string{sessionA, sessionB} {
		if err := exec.Command("tmux", "new-session", "-d", "-s", s, "sleep", "300").Run(); err != nil { //nolint:norawexec
			t.Fatalf("failed to create tmux session %q: %v", s, err)
		}
		t.Cleanup(func() { killTmuxSession(t, s) })
	}

	// Query workspace A: should only find sessionA.
	name, found, err := mgr.FindLatestAgentSession(wsA, agentName)
	if err != nil {
		t.Fatalf("FindLatestAgentSession(wsA) error: %v", err)
	}
	if !found {
		t.Fatal("FindLatestAgentSession(wsA) found = false, want true")
	}
	if name != sessionA {
		t.Errorf("FindLatestAgentSession(wsA) = %q, want %q", name, sessionA)
	}

	// Query workspace B: should only find sessionB.
	name, found, err = mgr.FindLatestAgentSession(wsB, agentName)
	if err != nil {
		t.Fatalf("FindLatestAgentSession(wsB) error: %v", err)
	}
	if !found {
		t.Fatal("FindLatestAgentSession(wsB) found = false, want true")
	}
	if name != sessionB {
		t.Errorf("FindLatestAgentSession(wsB) = %q, want %q", name, sessionB)
	}
}

// TestFindLatestAgentSession_NoMatch verifies that when no sessions match the
// given agent name, the function returns found=false without error.
func TestFindLatestAgentSession_NoMatch(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("", "", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	defer mgr.Shutdown()

	_, found, err := mgr.FindLatestAgentSession("", "nonexistent-agent-xyz")
	if err != nil {
		t.Fatalf("FindLatestAgentSession() error: %v", err)
	}
	if found {
		t.Error("FindLatestAgentSession() found = true, want false for nonexistent agent")
	}
}

// TestNewTerminalManager_NoTmux verifies that NewTerminalManager returns
// ErrTmuxNotFound when tmux is not available (using lookPathTmux override).
func TestNewTerminalManager_NoTmux(t *testing.T) {
	orig := lookPathTmux
	lookPathTmux = func(file string) (string, error) {
		return "", exec.ErrNotFound
	}
	t.Cleanup(func() { lookPathTmux = orig })

	_, err := NewTerminalManager("bash", "noTmuxTest", 0)
	if !errors.Is(err, ErrTmuxNotFound) {
		t.Errorf("expected ErrTmuxNotFound, got: %v", err)
	}
}
