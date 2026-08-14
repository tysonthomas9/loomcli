package pty

import (
	"testing"
	"time"
)

// TestExitReason_KillPropagatesToAttachment verifies that after PTYManager.Kill,
// an already-attached client can read an ExitReason of "killed" from its
// Attachment. The WS handler uses this to pick close code 4002.
func TestExitReason_KillPropagatesToAttachment(t *testing.T) {
	m := newTestManager(t)
	key := SessionKey{Workspace: "ws1", Name: "sess"}

	att, _, err := m.AttachSession(key, 80, 24, &LaunchSpec{Argv: []string{"-c", "cat"}})
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}

	if got := att.ExitReason(); got != "" {
		t.Errorf("ExitReason before Kill = %q; want empty", got)
	}

	if err := m.Kill(key); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	// Drain until the output channel is closed, then read ExitReason.
	waitUntil(t, func() bool {
		select {
		case _, ok := <-att.Output():
			return !ok
		default:
			return false
		}
	}, 2*time.Second, "output channel to close after Kill")

	if got := att.ExitReason(); got != ExitReasonKilled {
		t.Errorf("ExitReason after Kill = %q; want %q", got, ExitReasonKilled)
	}
}

// TestExitReason_ChildExitIsExited verifies that a self-terminating child
// process surfaces ExitReasonExited, so the WS handler can emit a normal
// session-ended close rather than 4002 (user-initiated kill).
func TestExitReason_ChildExitIsExited(t *testing.T) {
	m := newTestManager(t)
	key := SessionKey{Workspace: "ws1", Name: "sess"}

	att, _, err := m.AttachSession(key, 80, 24, &LaunchSpec{Argv: []string{"-c", "true"}})
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}

	waitUntil(t, func() bool {
		select {
		case _, ok := <-att.Output():
			return !ok
		default:
			return false
		}
	}, 2*time.Second, "output channel to close after child exit")

	if got := att.ExitReason(); got != ExitReasonExited {
		t.Errorf("ExitReason after child exit = %q; want %q", got, ExitReasonExited)
	}
	if !m.SessionClosed(key) {
		t.Errorf("SessionClosed after child exit = false; want true")
	}
	if m.HasSession(key) {
		t.Errorf("HasSession after child exit = true; want false")
	}
}

func TestSessionClosedClearsAfterFreshAttach(t *testing.T) {
	m := newTestManager(t)
	key := SessionKey{Workspace: "ws1", Name: "sess"}

	att, _, err := m.AttachSession(key, 80, 24, &LaunchSpec{Argv: []string{"-c", "true"}})
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}
	waitUntil(t, func() bool {
		select {
		case _, ok := <-att.Output():
			return !ok
		default:
			return false
		}
	}, 2*time.Second, "output channel to close after child exit")
	if !m.SessionClosed(key) {
		t.Fatalf("SessionClosed after child exit = false; want true")
	}

	next, reattach, err := m.AttachSession(key, 80, 24, &LaunchSpec{Argv: []string{"-c", "cat"}})
	if err != nil {
		t.Fatalf("AttachSession after closed session: %v", err)
	}
	if reattach {
		t.Errorf("reattach after closed session = true; want false")
	}
	if m.SessionClosed(key) {
		t.Errorf("SessionClosed after fresh attach = true; want false")
	}
	m.Detach(key, next.ConnID())
}

// TestExitReason_ReplacementIsEmpty verifies that an attachment replaced by a
// same-connID reconnect (not a session close) reports empty ExitReason. The
// session itself is still live, so a 1000 normal close is appropriate.
func TestExitReason_ReplacementIsEmpty(t *testing.T) {
	m := newTestManager(t)
	m.SetGracePeriod(5 * time.Second)
	key := SessionKey{Workspace: "ws1", Name: "sess"}

	att1, _, err := m.AttachSession(key, 80, 24, &LaunchSpec{Argv: []string{"-c", "cat"}})
	if err != nil {
		t.Fatalf("first attach: %v", err)
	}
	m.Detach(key, att1.ConnID())
	// Session remains live in the grace window; the attachment's
	// output channel closed because it was detached. ExitReason must stay
	// empty so the handler emits a benign close (1000).
	if got := att1.ExitReason(); got != "" {
		t.Errorf("ExitReason on plain detach = %q; want empty", got)
	}
}

// TestAttachSession_RetriesAcrossConcurrentClose exercises the race the
// attachNew nil-map guard + AttachSession retry loop together are meant to
// fix: a race where Kill runs between the manager-level lookup and the
// per-session attachNew call. We can't easily inject that race window
// deterministically, so this test instead verifies the effect — a Kill
// followed immediately by an AttachSession on the same key succeeds with a
// fresh session rather than returning an error or panicking.
func TestAttachSession_RetriesAcrossConcurrentClose(t *testing.T) {
	m := newTestManager(t)
	key := SessionKey{Workspace: "ws1", Name: "sess"}

	att1, _, err := m.AttachSession(key, 80, 24, &LaunchSpec{Argv: []string{"-c", "cat"}})
	if err != nil {
		t.Fatalf("initial attach: %v", err)
	}
	_ = att1

	if err := m.Kill(key); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	att2, reattach, err := m.AttachSession(key, 80, 24, &LaunchSpec{Argv: []string{"-c", "cat"}})
	if err != nil {
		t.Fatalf("re-attach after Kill: %v", err)
	}
	if reattach {
		t.Errorf("reattach=true after Kill; want fresh session")
	}
	if att2 == nil {
		t.Fatalf("AttachSession returned nil attachment")
	}
	m.Detach(key, att2.ConnID())
}

// TestAttachNew_ReturnsNilAfterClose directly covers the per-session guard:
// calling attachNew on a closed session must return nil rather than panic on
// a nil-map write.
func TestAttachNew_ReturnsNilAfterClose(t *testing.T) {
	m := newTestManager(t)
	key := SessionKey{Workspace: "ws1", Name: "sess"}

	_, _, err := m.AttachSession(key, 80, 24, &LaunchSpec{Argv: []string{"-c", "cat"}})
	if err != nil {
		t.Fatalf("AttachSession: %v", err)
	}

	// Reach into the manager to close the session directly, simulating a
	// teardown that races with an in-flight AttachSession. Then call
	// attachNew on the orphan session reference — it must return nil, not
	// panic.
	m.mu.Lock()
	sess := m.sessions[key]
	m.mu.Unlock()
	if sess == nil {
		t.Fatalf("session missing from manager")
	}
	if err := sess.close(ExitReasonKilled); err != nil {
		t.Fatalf("close: %v", err)
	}

	if got := sess.attachNew("pty-racy"); got != nil {
		t.Errorf("attachNew on closed session = %v; want nil", got)
	}
}
