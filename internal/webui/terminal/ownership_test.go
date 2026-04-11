package terminal

import (
	"testing"
)

// TestSetSessionOwner_FirstWriteWins verifies that SetSessionOwner uses
// first-write-wins semantics: the first call records ownership, and
// subsequent calls for the same session are no-ops.
func TestSetSessionOwner_FirstWriteWins(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("bash", testRunPrefix+"-tesdown01", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	defer mgr.Shutdown()

	mgr.SetSessionOwner("s1", "ws-a")
	mgr.SetSessionOwner("s1", "ws-b") // should be a no-op

	owner, ok := mgr.SessionOwner("s1")
	if !ok {
		t.Fatal("expected SessionOwner to return true for s1")
	}
	if owner != "ws-a" {
		t.Errorf("expected owner %q, got %q", "ws-a", owner)
	}
}

// TestSetSessionOwner_DifferentSessions verifies that different sessions
// can have different owners recorded independently.
func TestSetSessionOwner_DifferentSessions(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("bash", testRunPrefix+"-tesdown02", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	defer mgr.Shutdown()

	mgr.SetSessionOwner("s1", "ws-a")
	mgr.SetSessionOwner("s2", "ws-b")
	mgr.SetSessionOwner("s3", "ws-c")

	tests := []struct {
		session string
		want    string
	}{
		{"s1", "ws-a"},
		{"s2", "ws-b"},
		{"s3", "ws-c"},
	}
	for _, tt := range tests {
		owner, ok := mgr.SessionOwner(tt.session)
		if !ok {
			t.Errorf("expected SessionOwner(%q) to return true", tt.session)
			continue
		}
		if owner != tt.want {
			t.Errorf("SessionOwner(%q) = %q, want %q", tt.session, owner, tt.want)
		}
	}
}

// TestSessionOwner_Unset verifies that SessionOwner returns ("", false) for
// a session that has no recorded owner.
func TestSessionOwner_Unset(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("bash", testRunPrefix+"-tesdown03", 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	defer mgr.Shutdown()

	owner, ok := mgr.SessionOwner("nonexistent")
	if ok {
		t.Error("expected SessionOwner to return false for unknown session")
	}
	if owner != "" {
		t.Errorf("expected empty owner string, got %q", owner)
	}
}

// TestListActiveSessionsForWorkspace_FiltersCorrectly verifies that
// ListActiveSessionsForWorkspace returns only sessions whose qualified tmux
// name carries the workspace's prefix.
func TestListActiveSessionsForWorkspace_FiltersCorrectly(t *testing.T) {
	skipIfNoTmux(t)

	prefix := testRunPrefix + "-tesdown04"
	mgr, err := NewTerminalManager("bash", prefix, 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	defer mgr.Shutdown()

	// Spawn two sessions in ws-1 and one in ws-2.
	for _, name := range []string{"alpha", "gamma"} {
		if _, err := mgr.Spawn("ws-1", name, "", 80, 24); err != nil {
			t.Fatalf("Spawn(%q) error: %v", name, err)
		}
	}
	if _, err := mgr.Spawn("ws-2", "beta", "", 80, 24); err != nil {
		t.Fatalf("Spawn(beta) error: %v", err)
	}
	t.Cleanup(func() {
		_ = mgr.KillWorkspaceSessions("ws-1")
		_ = mgr.KillWorkspaceSessions("ws-2")
	})

	sessions, err := mgr.ListActiveSessionsForWorkspace("ws-1")
	if err != nil {
		t.Fatalf("ListActiveSessionsForWorkspace() error: %v", err)
	}

	names := make(map[string]bool)
	for _, s := range sessions {
		names[s.Name] = true
	}

	if !names["alpha"] {
		t.Error("expected alpha to be included for ws-1")
	}
	if !names["gamma"] {
		t.Error("expected gamma to be included for ws-1")
	}
	if names["beta"] {
		t.Error("expected beta (ws-2) to be excluded for ws-1")
	}
}

// TestKillSession_CleansOwnership verifies that KillSession removes the
// ownership entry for the killed session.
func TestKillSession_CleansOwnership(t *testing.T) {
	skipIfNoTmux(t)

	prefix := testRunPrefix + "-tesdown06"
	mgr, err := NewTerminalManager("bash", prefix, 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	defer mgr.Shutdown()

	name := "ownkill"
	if _, err := mgr.Spawn("ws-doomed", name, "", 80, 24); err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}
	mgr.SetSessionOwner(name, "ws-doomed")

	if owner, ok := mgr.SessionOwner(name); !ok || owner != "ws-doomed" {
		t.Fatalf("expected owner %q before kill, got (%q, %v)", "ws-doomed", owner, ok)
	}

	if err := mgr.KillSession("ws-doomed", name); err != nil {
		t.Fatalf("KillSession() error: %v", err)
	}

	owner, ok := mgr.SessionOwner(name)
	if ok {
		t.Errorf("expected SessionOwner to return false after kill, got (%q, true)", owner)
	}
}
