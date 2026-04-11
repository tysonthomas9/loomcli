package hooks

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

	// Spawn sessions in their respective workspaces.
	if _, err := mgr.Spawn("ws-target", "sess-a", "", 80, 24); err != nil {
		t.Fatalf("Spawn(sess-a) error: %v", err)
	}
	if _, err := mgr.Spawn("ws-other", "sess-b", "", 80, 24); err != nil {
		t.Fatalf("Spawn(sess-b) error: %v", err)
	}
	_ = prefix

	mgr.SetSessionOwner("sess-a", "ws-target")
	mgr.SetSessionOwner("sess-b", "ws-other")

	// Deregister ws-target — should kill sess-a but leave sess-b.
	hook.OnDeregister(deregCtx("ws-target"))

	// sess-b should survive (different workspace prefix).
	if !mgr.SessionExists("ws-other", "sess-b") {
		t.Error("expected sess-b to survive deregister of ws-target")
	}

	// sess-a should be gone.
	if mgr.SessionExists("ws-target", "sess-a") {
		t.Error("expected sess-a to be killed by deregister of ws-target")
	}
}

func TestTerminalHook_OnDeregister_SkipsUnownedSessions(t *testing.T) {
	hook, mgr := newTestTerminalHookEnv(t)

	// With v2's workspace-qualified prefix scheme, a session spawned for
	// ws-alpha lives under the "<prefix>-<wsShort(ws-alpha)>-" namespace and
	// is untouched when a different workspace is deregistered.
	if _, err := mgr.Spawn("ws-alpha", "unowned-1", "", 80, 24); err != nil {
		t.Fatalf("Spawn error: %v", err)
	}

	hook.OnDeregister(deregCtx("ws-whatever"))

	if !mgr.SessionExists("ws-alpha", "unowned-1") {
		t.Error("expected session in ws-alpha to survive deregister of ws-whatever")
	}
	_ = mgr.KillSession("ws-alpha", "unowned-1")
}

func TestTerminalHook_OnDeregister_NoSessions_NoOp(t *testing.T) {
	hook, _ := newTestTerminalHookEnv(t)

	// Should not panic or error.
	hook.OnDeregister(deregCtx("ws-empty"))
}

func TestTerminalHook_OnDeregister_MixedOwnership(t *testing.T) {
	hook, mgr := newTestTerminalHookEnv(t)

	// Spawn sessions in distinct workspaces so the workspace prefix filter
	// kills only ws-1's sessions on deregister.
	sessions := []struct {
		name string
		ws   string
	}{
		{"mix-a", "ws-1"},
		{"mix-b", "ws-2"},
		{"mix-c", "ws-1"},
		{"mix-d", "ws-3"},
	}
	for _, s := range sessions {
		if _, err := mgr.Spawn(s.ws, s.name, "", 80, 24); err != nil {
			t.Fatalf("Spawn(%q) error: %v", s.name, err)
		}
	}

	hook.OnDeregister(deregCtx("ws-1"))

	// A and C (ws-1) should be gone.
	for _, name := range []string{"mix-a", "mix-c"} {
		if mgr.SessionExists("ws-1", name) {
			t.Errorf("expected %q in ws-1 to be killed", name)
		}
	}

	// B (ws-2) and D (ws-3) should still exist.
	if !mgr.SessionExists("ws-2", "mix-b") {
		t.Error("expected mix-b in ws-2 to survive")
	}
	if !mgr.SessionExists("ws-3", "mix-d") {
		t.Error("expected mix-d in ws-3 to survive")
	}
	_ = mgr.KillSession("ws-2", "mix-b")
	_ = mgr.KillSession("ws-3", "mix-d")
}

func TestTerminalHook_OnRollback_SameAsDeregister(t *testing.T) {
	hook, mgr := newTestTerminalHookEnv(t)

	if _, err := mgr.Spawn("ws-rb", "rollback-s", "", 80, 24); err != nil {
		t.Fatalf("Spawn error: %v", err)
	}
	mgr.SetSessionOwner("rollback-s", "ws-rb")

	hook.OnRollback(deregCtx("ws-rb"))

	if mgr.SessionExists("ws-rb", "rollback-s") {
		t.Error("expected rollback-s to be killed by OnRollback")
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

	// Spawn a session in this workspace.
	if _, err := mgr.Spawn("ws-int", "int-sess", "", 80, 24); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	mgr.SetSessionOwner("int-sess", "ws-int")

	// Deregister — terminal session should be cleaned up.
	registry.Deregister("ws-int")

	if mgr.SessionExists("ws-int", "int-sess") {
		t.Error("expected int-sess to be killed after deregister via registry")
	}
}
