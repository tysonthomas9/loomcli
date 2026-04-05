package webui

import (
	"log/slog"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
)

// newTestTerminalHookEnv creates a TerminalManager and TerminalHook for testing.
// Requires tmux to be available; skips the test otherwise.
func newTestTerminalHookEnv(t *testing.T) (*TerminalHook, *TerminalManager) {
	t.Helper()
	skipIfNoTmux(t)

	prefix := testRunPrefix + "-thook"
	mgr, err := NewTerminalManager("bash", prefix, 20)
	if err != nil {
		t.Fatalf("NewTerminalManager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Shutdown() })

	hook := NewTerminalHook(mgr, slog.Default())
	return hook, mgr
}

func TestTerminalHook_Name(t *testing.T) {
	hook, _ := newTestTerminalHookEnv(t)
	if got := hook.Name(); got != "terminal" {
		t.Errorf("Name() = %q, want %q", got, "terminal")
	}
}

func TestTerminalHook_Critical(t *testing.T) {
	hook, _ := newTestTerminalHookEnv(t)
	if hook.Critical() {
		t.Error("Critical() = true, want false")
	}
}

func TestTerminalHook_OnRegister_ProvidesTerminalManager(t *testing.T) {
	hook, mgr := newTestTerminalHookEnv(t)

	ctx := regCtx("ws-term-1", "/tmp/ws1")
	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister returned error: %v", err)
	}

	res, ok := ctx.Resolve(coordinator.ResourceKeyTerminal)
	if !ok {
		t.Fatal("expected ResourceKeyTerminal to be provided")
	}
	if res != mgr {
		t.Error("expected provided resource to be the TerminalManager")
	}
}

func TestTerminalHook_OnRegister_AlwaysSucceeds(t *testing.T) {
	hook, _ := newTestTerminalHookEnv(t)

	ctx := regCtx("ws-term-2", "/tmp/ws2")
	if err := hook.OnRegister(ctx); err != nil {
		t.Errorf("OnRegister returned unexpected error: %v", err)
	}
}

func TestTerminalHook_OnDeregister_KillsOwnedSessions(t *testing.T) {
	hook, mgr := newTestTerminalHookEnv(t)

	prefix := testRunPrefix + "-thook"

	// Spawn sessions and assign ownership.
	for _, name := range []string{"sess-a", "sess-b"} {
		if _, err := mgr.Spawn(name, "", 80, 24); err != nil {
			t.Fatalf("Spawn(%q) error: %v", name, err)
		}
		t.Cleanup(func() { killTmuxSession(t, prefix+"-"+name) })
	}

	mgr.SetSessionOwner("sess-a", "ws-target")
	mgr.SetSessionOwner("sess-b", "ws-other")

	// Deregister ws-target — should kill sess-a but leave sess-b.
	hook.OnDeregister(deregCtx("ws-target"))

	// sess-a should have been killed (ownership cleared).
	if _, ok := mgr.SessionOwner("sess-a"); ok {
		t.Error("expected sess-a ownership to be cleared after deregister")
	}

	// sess-b should be untouched.
	owner, ok := mgr.SessionOwner("sess-b")
	if !ok || owner != "ws-other" {
		t.Errorf("expected sess-b owner to be %q, got (%q, %v)", "ws-other", owner, ok)
	}
}

func TestTerminalHook_OnDeregister_SkipsUnownedSessions(t *testing.T) {
	hook, mgr := newTestTerminalHookEnv(t)

	prefix := testRunPrefix + "-thook"

	// Spawn a session without setting an owner.
	if _, err := mgr.Spawn("unowned-1", "", 80, 24); err != nil {
		t.Fatalf("Spawn error: %v", err)
	}
	t.Cleanup(func() { killTmuxSession(t, prefix+"-unowned-1") })

	// Deregister a workspace — the unowned session should survive.
	hook.OnDeregister(deregCtx("ws-whatever"))

	// Verify the tmux session still exists.
	if !tmuxSessionExists(prefix + "-unowned-1") {
		t.Error("expected unowned session to survive deregister")
	}
}

func TestTerminalHook_OnDeregister_NoSessions_NoOp(t *testing.T) {
	hook, _ := newTestTerminalHookEnv(t)

	// Should not panic or error.
	hook.OnDeregister(deregCtx("ws-empty"))
}

func TestTerminalHook_OnDeregister_MixedOwnership(t *testing.T) {
	hook, mgr := newTestTerminalHookEnv(t)

	prefix := testRunPrefix + "-thook"

	// Create 4 sessions: A(ws-1), B(ws-2), C(ws-1), D(unowned).
	sessions := []struct {
		name  string
		owner string
	}{
		{"mix-a", "ws-1"},
		{"mix-b", "ws-2"},
		{"mix-c", "ws-1"},
		{"mix-d", ""},
	}
	for _, s := range sessions {
		if _, err := mgr.Spawn(s.name, "", 80, 24); err != nil {
			t.Fatalf("Spawn(%q) error: %v", s.name, err)
		}
		t.Cleanup(func() { killTmuxSession(t, prefix+"-"+s.name) })
		if s.owner != "" {
			mgr.SetSessionOwner(s.name, s.owner)
		}
	}

	// Deregister ws-1 — should kill A and C, leave B and D.
	hook.OnDeregister(deregCtx("ws-1"))

	// A and C should be killed (ownership cleared by KillSessionByName).
	for _, name := range []string{"mix-a", "mix-c"} {
		if _, ok := mgr.SessionOwner(name); ok {
			t.Errorf("expected %q ownership to be cleared after deregister", name)
		}
	}

	// B should still be owned by ws-2.
	if owner, ok := mgr.SessionOwner("mix-b"); !ok || owner != "ws-2" {
		t.Errorf("expected mix-b owner %q, got (%q, %v)", "ws-2", owner, ok)
	}

	// D (unowned) tmux session should still exist.
	if !tmuxSessionExists(prefix + "-mix-d") {
		t.Error("expected unowned session mix-d to survive deregister")
	}
}

func TestTerminalHook_OnRollback_SameAsDeregister(t *testing.T) {
	hook, mgr := newTestTerminalHookEnv(t)

	prefix := testRunPrefix + "-thook"

	if _, err := mgr.Spawn("rollback-s", "", 80, 24); err != nil {
		t.Fatalf("Spawn error: %v", err)
	}
	t.Cleanup(func() { killTmuxSession(t, prefix+"-rollback-s") })
	mgr.SetSessionOwner("rollback-s", "ws-rb")

	hook.OnRollback(deregCtx("ws-rb"))

	if _, ok := mgr.SessionOwner("rollback-s"); ok {
		t.Error("expected rollback-s ownership to be cleared after rollback")
	}
}

func TestTerminalHook_NilTermMgr_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic with nil termMgr, got none")
		}
	}()
	NewTerminalHook(nil, slog.Default())
}

func TestTerminalHook_DefaultLogger(t *testing.T) {
	skipIfNoTmux(t)

	mgr, err := NewTerminalManager("bash", testRunPrefix+"-thooklog", 20)
	if err != nil {
		t.Fatalf("NewTerminalManager: %v", err)
	}
	defer mgr.Shutdown()

	// Should not panic with nil logger.
	hook := NewTerminalHook(mgr, nil)
	if hook.logger == nil {
		t.Error("expected default logger to be set, got nil")
	}
}

func TestTerminalHook_IntegrationWithCoordinatorRegistry(t *testing.T) {
	skipIfNoTmux(t)

	prefix := testRunPrefix + "-thookint"
	mgr, err := NewTerminalManager("bash", prefix, 20)
	if err != nil {
		t.Fatalf("NewTerminalManager: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Shutdown() })

	termHook := NewTerminalHook(mgr, slog.Default())

	registry := coordinator.NewWorkspaceRegistry(slog.Default())
	if err := registry.AddHook(termHook); err != nil {
		t.Fatalf("AddHook(terminal): %v", err)
	}

	// Register workspace — hook should fire.
	if err := registry.Register("ws-int", "/tmp/ws-int"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Spawn a session owned by this workspace.
	if _, err := mgr.Spawn("int-sess", "", 80, 24); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() { killTmuxSession(t, prefix+"-int-sess") })
	mgr.SetSessionOwner("int-sess", "ws-int")

	// Deregister — terminal session should be cleaned up.
	registry.Deregister("ws-int")

	if _, ok := mgr.SessionOwner("int-sess"); ok {
		t.Error("expected int-sess ownership to be cleared after deregister via registry")
	}
}
