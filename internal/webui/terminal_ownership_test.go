package webui

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
// ListActiveSessionsForWorkspace returns only sessions owned by the
// specified workspace and excludes sessions owned by other workspaces.
func TestListActiveSessionsForWorkspace_FiltersCorrectly(t *testing.T) {
	skipIfNoTmux(t)

	prefix := testRunPrefix + "-tesdown04"
	mgr, err := NewTerminalManager("bash", prefix, 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	defer mgr.Shutdown()

	// Spawn three sessions.
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if _, err := mgr.Spawn(name, "", 80, 24); err != nil {
			t.Fatalf("Spawn(%q) error: %v", name, err)
		}
		t.Cleanup(func() {
			killTmuxSession(t, prefix+"-"+name)
		})
	}

	// Assign owners: alpha -> ws-1, beta -> ws-2, gamma -> ws-1.
	mgr.SetSessionOwner("alpha", "ws-1")
	mgr.SetSessionOwner("beta", "ws-2")
	mgr.SetSessionOwner("gamma", "ws-1")

	// Filter for ws-1: should include alpha and gamma, exclude beta.
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
		t.Error("expected beta to be excluded for ws-1")
	}
}

// TestListActiveSessionsForWorkspace_UnownedIncluded verifies that sessions
// with no recorded owner appear in the results for any workspace.
func TestListActiveSessionsForWorkspace_UnownedIncluded(t *testing.T) {
	skipIfNoTmux(t)

	prefix := testRunPrefix + "-tesdown05"
	mgr, err := NewTerminalManager("bash", prefix, 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	defer mgr.Shutdown()

	// Spawn two sessions without setting any owners.
	for _, name := range []string{"unowned1", "unowned2"} {
		if _, err := mgr.Spawn(name, "", 80, 24); err != nil {
			t.Fatalf("Spawn(%q) error: %v", name, err)
		}
		t.Cleanup(func() {
			killTmuxSession(t, prefix+"-"+name)
		})
	}

	// Both unowned sessions should appear for any workspace.
	sessions, err := mgr.ListActiveSessionsForWorkspace("ws-arbitrary")
	if err != nil {
		t.Fatalf("ListActiveSessionsForWorkspace() error: %v", err)
	}

	names := make(map[string]bool)
	for _, s := range sessions {
		names[s.Name] = true
	}

	if !names["unowned1"] {
		t.Error("expected unowned1 to be included for arbitrary workspace")
	}
	if !names["unowned2"] {
		t.Error("expected unowned2 to be included for arbitrary workspace")
	}
}

// TestKillSessionByName_CleansOwnership verifies that KillSessionByName
// removes the ownership entry for the killed session.
func TestKillSessionByName_CleansOwnership(t *testing.T) {
	skipIfNoTmux(t)

	prefix := testRunPrefix + "-tesdown06"
	mgr, err := NewTerminalManager("bash", prefix, 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	defer mgr.Shutdown()

	name := "ownkill"
	if _, err := mgr.Spawn(name, "", 80, 24); err != nil {
		t.Fatalf("Spawn() error: %v", err)
	}
	t.Cleanup(func() {
		killTmuxSession(t, prefix+"-"+name)
	})

	mgr.SetSessionOwner(name, "ws-doomed")

	// Verify ownership is set.
	if owner, ok := mgr.SessionOwner(name); !ok || owner != "ws-doomed" {
		t.Fatalf("expected owner %q before kill, got (%q, %v)", "ws-doomed", owner, ok)
	}

	// Kill the session.
	if err := mgr.KillSessionByName(name); err != nil {
		t.Fatalf("KillSessionByName() error: %v", err)
	}

	// Ownership should be cleared.
	owner, ok := mgr.SessionOwner(name)
	if ok {
		t.Errorf("expected SessionOwner to return false after kill, got (%q, true)", owner)
	}
}

// TestKillAllSessions_ClearsOwnership verifies that KillAllSessions resets
// the sessionOwners map so all previous ownership entries are gone.
func TestKillAllSessions_ClearsOwnership(t *testing.T) {
	skipIfNoTmux(t)

	prefix := testRunPrefix + "-tesdown07"
	mgr, err := NewTerminalManager("bash", prefix, 0)
	if err != nil {
		t.Fatalf("NewTerminalManager() error: %v", err)
	}
	defer mgr.Shutdown()

	// Set several ownership entries (no actual tmux sessions needed for the
	// ownership map, but KillAllSessions also cleans PTY state so we keep it
	// simple by just recording ownership).
	mgr.SetSessionOwner("x1", "ws-1")
	mgr.SetSessionOwner("x2", "ws-2")
	mgr.SetSessionOwner("x3", "ws-3")

	if err := mgr.KillAllSessions(); err != nil {
		t.Fatalf("KillAllSessions() error: %v", err)
	}

	for _, name := range []string{"x1", "x2", "x3"} {
		if owner, ok := mgr.SessionOwner(name); ok {
			t.Errorf("expected SessionOwner(%q) to return false after KillAllSessions, got (%q, true)", name, owner)
		}
	}
}
