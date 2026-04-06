package hooks

import (
	"log/slog"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
)

const testFleetURL = "http://localhost:0"

func newTestFleetBackendHook(t *testing.T) *FleetBackendHook {
	t.Helper()
	return NewFleetBackendHook(testFleetURL, "test-ws", "test-key", slog.Default())
}

func TestFleetBackendHook_Name(t *testing.T) {
	hook := newTestFleetBackendHook(t)
	if got := hook.Name(); got != "fleet-backend" {
		t.Errorf("Name() = %q, want %q", got, "fleet-backend")
	}
}

func TestFleetBackendHook_Critical(t *testing.T) {
	hook := newTestFleetBackendHook(t)
	if hook.Critical() {
		t.Error("Critical() = true, want false")
	}
}

func TestFleetBackendHook_OnRegister_CreatesBackend(t *testing.T) {
	hook := newTestFleetBackendHook(t)

	ctx := regCtx("ws-fleet-be-1", "/tmp/ws1")
	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister returned error: %v", err)
	}

	be, ok := hook.BackendForWorkspace("ws-fleet-be-1")
	if !ok {
		t.Fatal("expected BackendForWorkspace to return true after OnRegister")
	}
	if be == nil {
		t.Fatal("expected non-nil backend from BackendForWorkspace")
	}
	if got := be.BackendName(); got != "fleet" {
		t.Errorf("BackendName() = %q, want %q", got, "fleet")
	}
}

func TestFleetBackendHook_OnRegister_ProvidesResource(t *testing.T) {
	hook := newTestFleetBackendHook(t)

	ctx := regCtx("ws-fleet-res", "/tmp/ws-res")
	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister returned error: %v", err)
	}

	res, ok := ctx.Resolve(coordinator.ResourceKeyFleetBackend)
	if !ok {
		t.Fatal("expected ResourceKeyFleetBackend to be provided")
	}
	if res == nil {
		t.Fatal("expected non-nil resource for ResourceKeyFleetBackend")
	}

	// The provided resource should be the same backend returned by BackendForWorkspace.
	be, _ := hook.BackendForWorkspace("ws-fleet-res")
	if res != be {
		t.Error("expected provided resource to be the same backend from BackendForWorkspace")
	}
}

func TestFleetBackendHook_OnDeregister_RemovesBackend(t *testing.T) {
	hook := newTestFleetBackendHook(t)

	ctx := regCtx("ws-fleet-dereg", "/tmp/ws-dereg")
	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister: %v", err)
	}

	// Sanity check: backend exists.
	if _, ok := hook.BackendForWorkspace("ws-fleet-dereg"); !ok {
		t.Fatal("expected backend to exist before deregister")
	}

	hook.OnDeregister(deregCtx("ws-fleet-dereg"))

	be, ok := hook.BackendForWorkspace("ws-fleet-dereg")
	if ok {
		t.Error("expected BackendForWorkspace to return false after deregister")
	}
	if be != nil {
		t.Error("expected BackendForWorkspace to return nil after deregister")
	}
}

func TestFleetBackendHook_BackendForWorkspace_Unknown(t *testing.T) {
	hook := newTestFleetBackendHook(t)

	be, ok := hook.BackendForWorkspace("nonexistent-ws")
	if ok {
		t.Error("expected BackendForWorkspace to return false for unknown workspace")
	}
	if be != nil {
		t.Error("expected BackendForWorkspace to return nil for unknown workspace")
	}
}

func TestFleetBackendHook_OnRollback_SameAsDeregister(t *testing.T) {
	hook := newTestFleetBackendHook(t)

	ctx := regCtx("ws-fleet-rb", "/tmp/ws-rb")
	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister: %v", err)
	}

	hook.OnRollback(deregCtx("ws-fleet-rb"))

	be, ok := hook.BackendForWorkspace("ws-fleet-rb")
	if ok {
		t.Error("expected BackendForWorkspace to return false after rollback")
	}
	if be != nil {
		t.Error("expected BackendForWorkspace to return nil after rollback")
	}
}

func TestFleetBackendHook_MultipleWorkspaces(t *testing.T) {
	hook := newTestFleetBackendHook(t)

	workspaces := []string{"ws-alpha", "ws-beta", "ws-gamma"}
	for _, wsID := range workspaces {
		ctx := regCtx(wsID, "/tmp/"+wsID)
		if err := hook.OnRegister(ctx); err != nil {
			t.Fatalf("OnRegister(%q): %v", wsID, err)
		}
	}

	// All three should have backends.
	for _, wsID := range workspaces {
		be, ok := hook.BackendForWorkspace(wsID)
		if !ok || be == nil {
			t.Errorf("expected backend for %q, got (nil=%v, ok=%v)", wsID, be == nil, ok)
		}
	}

	// Deregister one; others should remain.
	hook.OnDeregister(deregCtx("ws-beta"))

	if _, ok := hook.BackendForWorkspace("ws-beta"); ok {
		t.Error("expected ws-beta to be removed after deregister")
	}
	for _, wsID := range []string{"ws-alpha", "ws-gamma"} {
		if _, ok := hook.BackendForWorkspace(wsID); !ok {
			t.Errorf("expected %q to remain after deregistering ws-beta", wsID)
		}
	}
}
