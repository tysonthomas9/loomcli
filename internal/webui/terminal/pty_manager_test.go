package terminal

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)

// newTestManager returns a manager configured to spawn `/bin/bash -c "cat"`.
// cat echoes stdin to stdout so tests can deterministically drive the PTY.
func newTestManager(t *testing.T) *PTYManager {
	t.Helper()
	m := NewPTYManager("cat", 0)
	m.SetGracePeriod(200 * time.Millisecond)
	m.SetIdleTimeout(200 * time.Millisecond)
	t.Cleanup(func() { _ = m.Shutdown() })
	return m
}

// readChunk drains up to 500 ms of output from an attachment, returning the
// accumulated bytes. Used to synchronize with the PTY echo.
func readChunk(t *testing.T, att Attachment, deadline time.Duration) []byte {
	t.Helper()
	out := make([]byte, 0, 256)
	timeout := time.After(deadline)
	for {
		select {
		case chunk, ok := <-att.Output():
			if !ok {
				return out
			}
			out = append(out, chunk...)
		case <-timeout:
			return out
		}
	}
}

func waitUntil(t *testing.T, cond func() bool, deadline time.Duration, msg string) {
	t.Helper()
	start := time.Now()
	for time.Since(start) < deadline {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for: %s", msg)
}

func TestAttach_SpawnsFreshSession(t *testing.T) {
	m := newTestManager(t)
	key := SessionKey{Workspace: "ws1", Name: "lead-shell-1"}

	att, reattach, err := m.AttachSession(key, 80, 24, []string{"-c", "cat"})
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	if reattach {
		t.Errorf("reattach=true on fresh session")
	}
	if att.Scrollback() != nil {
		t.Errorf("Scrollback() non-nil on fresh session")
	}
	if got := m.SessionCount(); got != 1 {
		t.Errorf("SessionCount=%d want 1", got)
	}
	m.Detach(key, att.ConnID())
}

func TestDetachDoesNotKillImmediately(t *testing.T) {
	m := newTestManager(t)
	key := SessionKey{Workspace: "ws1", Name: "lead-shell-1"}

	att, _, err := m.AttachSession(key, 80, 24, []string{"-c", "cat"})
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	m.Detach(key, att.ConnID())

	// Session should still be live during the grace window.
	if got := m.SessionCount(); got != 1 {
		t.Errorf("SessionCount after Detach=%d want 1 (still in grace)", got)
	}
}

func TestReattachWithinGraceReplaysScrollback(t *testing.T) {
	m := newTestManager(t)
	m.SetGracePeriod(2 * time.Second) // wide enough to not race the test
	key := SessionKey{Workspace: "ws1", Name: "lead-shell-1"}

	att1, _, err := m.AttachSession(key, 80, 24, []string{"-c", "cat"})
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}

	// Send data through cat; it echoes back to stdout → scrollback.
	if _, err := att1.WriteInput([]byte("hello-world\n")); err != nil {
		t.Fatalf("WriteInput: %v", err)
	}
	out1 := readChunk(t, att1, 500*time.Millisecond)
	if !bytes.Contains(out1, []byte("hello-world")) {
		t.Fatalf("first attach output missing echo; got %q", string(out1))
	}

	// Detach and immediately reattach.
	m.Detach(key, att1.ConnID())
	att2, reattach, err := m.AttachSession(key, 80, 24, nil)
	if err != nil {
		t.Fatalf("reattach: %v", err)
	}
	if !reattach {
		t.Errorf("reattach=false on existing session")
	}
	replay := att2.Scrollback()
	if !bytes.HasPrefix(replay, []byte("\x1b[2J\x1b[H")) {
		t.Errorf("replay missing reset prefix; got %q", string(replay[:min(8, len(replay))]))
	}
	if !bytes.Contains(replay, []byte("hello-world")) {
		t.Errorf("replay missing prior output; got %q", string(replay))
	}
	m.Detach(key, att2.ConnID())
}

func TestGracePeriodExpiryKillsSession(t *testing.T) {
	m := newTestManager(t)
	m.SetGracePeriod(50 * time.Millisecond)
	key := SessionKey{Workspace: "ws1", Name: "lead-shell-1"}

	att, _, err := m.AttachSession(key, 80, 24, []string{"-c", "cat"})
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	m.Detach(key, att.ConnID())
	waitUntil(t, func() bool { return m.SessionCount() == 0 }, time.Second,
		"session count to reach 0 after grace expiry")
}

func TestExplicitKillTerminatesImmediately(t *testing.T) {
	m := newTestManager(t)
	key := SessionKey{Workspace: "ws1", Name: "lead-shell-1"}

	att, _, err := m.AttachSession(key, 80, 24, []string{"-c", "cat"})
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	if err := m.Kill(key); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	if got := m.SessionCount(); got != 0 {
		t.Errorf("SessionCount after Kill=%d want 0", got)
	}
	// Output channel should be closed for the formerly-attached consumer.
	select {
	case _, ok := <-att.Output():
		if ok {
			// One residual frame is acceptable if drained just before close.
			select {
			case _, ok2 := <-att.Output():
				if ok2 {
					t.Errorf("output channel still open after Kill")
				}
			case <-time.After(200 * time.Millisecond):
				t.Errorf("output channel did not close after Kill")
			}
		}
	case <-time.After(200 * time.Millisecond):
		t.Errorf("output channel did not close after Kill")
	}
}

func TestIdleReapClosesDetachedSession(t *testing.T) {
	m := newTestManager(t)
	m.SetGracePeriod(10 * time.Second) // disable grace path
	m.SetIdleTimeout(50 * time.Millisecond)
	key := SessionKey{Workspace: "ws1", Name: "lead-shell-1"}

	att, _, err := m.AttachSession(key, 80, 24, []string{"-c", "cat"})
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	m.Detach(key, att.ConnID())

	// The reaper wakes every defaultReaperTick (60 s) so triggering it by
	// time alone would be slow. Poke reapIdle directly.
	waitUntil(t, func() bool {
		m.reapIdle()
		return m.SessionCount() == 0
	}, 2*time.Second, "idle reap to clear session")
}

func TestSessionCountIncludesDetachedUpToMax(t *testing.T) {
	m := newTestManager(t)
	m.SetGracePeriod(5 * time.Second)

	type attached struct {
		key SessionKey
		att Attachment
	}
	var all []attached
	for i := 0; i < m.MaxSessions(); i++ {
		k := SessionKey{Workspace: "ws", Name: fmt.Sprintf("s-%d", i)}
		att, _, err := m.AttachSession(k, 80, 24, []string{"-c", "cat"})
		if err != nil {
			t.Fatalf("attach %d: %v", i, err)
		}
		all = append(all, attached{k, att})
	}
	// Detach every second one. Detached sessions still count toward the cap.
	for i, a := range all {
		if i%2 == 0 {
			m.Detach(a.key, a.att.ConnID())
		}
	}
	if got := m.SessionCount(); got != m.MaxSessions() {
		t.Errorf("SessionCount=%d want %d", got, m.MaxSessions())
	}

	key := SessionKey{Workspace: "ws", Name: "over-cap"}
	_, _, err := m.AttachSession(key, 80, 24, []string{"-c", "cat"})
	if err == nil {
		t.Errorf("expected ErrPTYMaxSessionsReached, got nil")
	}
}

func TestSecondAttachReplacesFirstAndReceivesScrollback(t *testing.T) {
	m := newTestManager(t)
	m.SetGracePeriod(5 * time.Second)
	key := SessionKey{Workspace: "ws1", Name: "lead-shell-1"}

	att1, _, err := m.AttachSession(key, 80, 24, []string{"-c", "cat"})
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}

	if _, err := att1.WriteInput([]byte("marker-abc\n")); err != nil {
		t.Fatalf("WriteInput: %v", err)
	}
	readChunk(t, att1, 500*time.Millisecond) // drain

	// Second attach without an intervening Detach should kick out the first.
	att2, reattach, err := m.AttachSession(key, 80, 24, nil)
	if err != nil {
		t.Fatalf("second attach: %v", err)
	}
	if !reattach {
		t.Errorf("reattach=false after second Attach")
	}
	if !bytes.Contains(att2.Scrollback(), []byte("marker-abc")) {
		t.Errorf("second attach replay missing marker; got %q", string(att2.Scrollback()))
	}

	// First attachment's channel should be closed.
	waitUntil(t, func() bool {
		select {
		case _, ok := <-att1.Output():
			return !ok
		default:
			return false
		}
	}, time.Second, "first attachment channel to close after replacement")

	m.Detach(key, att2.ConnID())
}

func TestChildExitRemovesSession(t *testing.T) {
	m := newTestManager(t)
	key := SessionKey{Workspace: "ws1", Name: "lead-shell-1"}

	// A command that exits immediately.
	_, _, err := m.AttachSession(key, 80, 24, []string{"-c", "true"})
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	waitUntil(t, func() bool { return m.SessionCount() == 0 }, 2*time.Second,
		"session to be removed after child exits")
}

