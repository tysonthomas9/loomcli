package hooks

import (
	"log/slog"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"

	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
	"github.com/tysonthomas9/loomcli/internal/webui/fleet"
)

// newTestFleetStoreHookEnv creates a fleet.StoreRegistry backed by miniredis and
// a FleetStoreHook for testing. All resources are cleaned up via t.Cleanup.
func newTestFleetStoreHookEnv(t *testing.T) (*FleetStoreHook, *fleet.StoreRegistry) {
	t.Helper()
	mr := miniredis.RunT(t)
	fleetReg, err := fleet.NewStoreRegistry(
		fleet.RedisConfig{Address: mr.Addr()},
		fleet.TimeoutConfig{TaskTimeout: 30 * time.Minute, CheckInterval: 1 * time.Minute},
		nil,
	)
	if err != nil {
		t.Fatalf("NewStoreRegistry failed: %v", err)
	}
	t.Cleanup(func() { _ = fleetReg.Close() })

	hook := NewFleetStoreHook(fleetReg, slog.Default())
	return hook, fleetReg
}

func TestFleetStoreHook_Name(t *testing.T) {
	hook, _ := newTestFleetStoreHookEnv(t)
	if got := hook.Name(); got != "fleet-store" {
		t.Errorf("Name() = %q, want %q", got, "fleet-store")
	}
}

func TestFleetStoreHook_Critical(t *testing.T) {
	hook, _ := newTestFleetStoreHookEnv(t)
	if hook.Critical() {
		t.Error("Critical() = true, want false")
	}
}

func TestFleetStoreHook_OnRegister_RegistersAndProvidesStore(t *testing.T) {
	hook, fleetReg := newTestFleetStoreHookEnv(t)

	ctx := regCtx("ws-fleet-1", "/tmp/ws1")
	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister returned error: %v", err)
	}

	// Verify the store was registered in the fleet registry.
	store, ok := fleetReg.Get("ws-fleet-1")
	if !ok {
		t.Fatal("expected fleet registry to contain workspace ws-fleet-1")
	}
	if store == nil {
		t.Fatal("expected non-nil store from fleet registry")
	}

	// Verify the store was provided in the resource bag.
	res, ok := ctx.Resolve(coordinator.ResourceKeyFleetStore)
	if !ok {
		t.Fatal("expected ResourceKeyFleetStore to be provided")
	}
	if res != store {
		t.Error("expected provided resource to be the fleet Store from registry")
	}
}

func TestFleetStoreHook_OnRegister_Idempotent(t *testing.T) {
	hook, fleetReg := newTestFleetStoreHookEnv(t)

	// Register the same workspace twice. fleet.StoreRegistry.Register is
	// idempotent, so this should succeed both times.
	ctx1 := regCtx("ws-fleet-idem", "/tmp/ws1")
	if err := hook.OnRegister(ctx1); err != nil {
		t.Fatalf("first OnRegister returned error: %v", err)
	}

	ctx2 := regCtx("ws-fleet-idem", "/tmp/ws1")
	if err := hook.OnRegister(ctx2); err != nil {
		t.Fatalf("second OnRegister returned error: %v", err)
	}

	// Store should still be retrievable.
	store, ok := fleetReg.Get("ws-fleet-idem")
	if !ok || store == nil {
		t.Fatal("expected fleet store to be present after idempotent register")
	}
}

func TestFleetStoreHook_OnRegister_ClosedRegistry_ReturnsError(t *testing.T) {
	hook, fleetReg := newTestFleetStoreHookEnv(t)

	// Close the fleet registry before registering.
	_ = fleetReg.Close()

	ctx := regCtx("ws-fleet-closed", "/tmp/ws1")
	err := hook.OnRegister(ctx)
	if err == nil {
		t.Fatal("expected error when fleet registry is closed, got nil")
	}

	// Resource should NOT be provided on failure.
	if _, ok := ctx.Resolve(coordinator.ResourceKeyFleetStore); ok {
		t.Error("expected ResourceKeyFleetStore NOT to be provided after failed register")
	}
}

func TestFleetStoreHook_OnDeregister_RemovesFromFleet(t *testing.T) {
	hook, fleetReg := newTestFleetStoreHookEnv(t)

	ctx := regCtx("ws-fleet-dereg", "/tmp/ws1")
	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister: %v", err)
	}

	// Sanity check: store exists.
	if _, ok := fleetReg.Get("ws-fleet-dereg"); !ok {
		t.Fatal("expected store to exist before deregister")
	}

	hook.OnDeregister(deregCtx("ws-fleet-dereg"))

	// Store should be gone.
	store, ok := fleetReg.Get("ws-fleet-dereg")
	if ok {
		t.Error("expected fleet registry Get to return false after deregister")
	}
	if store != nil {
		t.Error("expected fleet registry Get to return nil after deregister")
	}
}

func TestFleetStoreHook_OnDeregister_UnknownWorkspace(t *testing.T) {
	hook, _ := newTestFleetStoreHookEnv(t)
	// Should not panic.
	hook.OnDeregister(deregCtx("nonexistent"))
}

func TestFleetStoreHook_OnRollback_SameAsDeregister(t *testing.T) {
	hook, fleetReg := newTestFleetStoreHookEnv(t)

	ctx := regCtx("ws-fleet-rb", "/tmp/ws1")
	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister: %v", err)
	}

	hook.OnRollback(deregCtx("ws-fleet-rb"))

	store, ok := fleetReg.Get("ws-fleet-rb")
	if ok {
		t.Error("expected fleet registry Get to return false after rollback")
	}
	if store != nil {
		t.Error("expected fleet registry Get to return nil after rollback")
	}
}

func TestFleetStoreHook_OnRollback_AfterFailedRegister(t *testing.T) {
	hook, fleetReg := newTestFleetStoreHookEnv(t)

	// Close fleet registry so OnRegister fails.
	_ = fleetReg.Close()

	ctx := regCtx("ws-fleet-rbfail", "/tmp/ws1")
	_ = hook.OnRegister(ctx)

	// Rollback should be safe even though register failed (Deregister is no-op
	// for unknown IDs).
	hook.OnRollback(deregCtx("ws-fleet-rbfail"))
}

func TestFleetStoreHook_NilRegistry_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic with nil registry, got none")
		}
	}()
	NewFleetStoreHook(nil, slog.Default())
}

func TestFleetStoreHook_DefaultLogger(t *testing.T) {
	mr := miniredis.RunT(t)
	fleetReg, err := fleet.NewStoreRegistry(
		fleet.RedisConfig{Address: mr.Addr()},
		fleet.TimeoutConfig{TaskTimeout: 30 * time.Minute, CheckInterval: 1 * time.Minute},
		nil,
	)
	if err != nil {
		t.Fatalf("NewStoreRegistry failed: %v", err)
	}
	defer fleetReg.Close()

	// Should not panic with nil logger.
	hook := NewFleetStoreHook(fleetReg, nil)
	if hook.logger == nil {
		t.Error("expected default logger to be set, got nil")
	}
}

func TestFleetStoreHook_MultipleWorkspaces(t *testing.T) {
	hook, fleetReg := newTestFleetStoreHookEnv(t)

	workspaces := []string{"ws-a", "ws-b", "ws-c"}
	for _, wsID := range workspaces {
		ctx := regCtx(wsID, "/tmp/"+wsID)
		if err := hook.OnRegister(ctx); err != nil {
			t.Fatalf("OnRegister(%q): %v", wsID, err)
		}
	}

	// All three should be in the fleet registry.
	for _, wsID := range workspaces {
		store, ok := fleetReg.Get(wsID)
		if !ok || store == nil {
			t.Errorf("expected fleet store for %q, got (nil=%v, ok=%v)", wsID, store == nil, ok)
		}
	}

	// Deregister one; others should remain.
	hook.OnDeregister(deregCtx("ws-b"))

	if _, ok := fleetReg.Get("ws-b"); ok {
		t.Error("expected ws-b to be deregistered from fleet")
	}
	for _, wsID := range []string{"ws-a", "ws-c"} {
		if _, ok := fleetReg.Get(wsID); !ok {
			t.Errorf("expected %q to remain in fleet after deregistering ws-b", wsID)
		}
	}
}

func TestFleetStoreHook_IntegrationWithCoordinatorRegistry(t *testing.T) {
	mr := miniredis.RunT(t)
	fleetReg, err := fleet.NewStoreRegistry(
		fleet.RedisConfig{Address: mr.Addr()},
		fleet.TimeoutConfig{TaskTimeout: 30 * time.Minute, CheckInterval: 1 * time.Minute},
		nil,
	)
	if err != nil {
		t.Fatalf("NewStoreRegistry failed: %v", err)
	}
	t.Cleanup(func() { _ = fleetReg.Close() })

	fleetHook := NewFleetStoreHook(fleetReg, slog.Default())

	registry := coordinator.NewWorkspaceRegistry(slog.Default())
	if err := registry.AddHook(fleetHook); err != nil {
		t.Fatalf("AddHook(fleet-store): %v", err)
	}

	// Register workspace via coordinator registry.
	if err := registry.Register("ws-int", "/tmp/ws-int"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Fleet store should be registered.
	store, ok := fleetReg.Get("ws-int")
	if !ok {
		t.Fatal("expected fleet store to be registered after coordinator.Register")
	}
	if store == nil {
		t.Fatal("expected non-nil fleet store after coordinator.Register")
	}

	// Deregister via coordinator registry.
	registry.Deregister("ws-int")

	store, ok = fleetReg.Get("ws-int")
	if ok {
		t.Error("expected fleet store to be removed after coordinator.Deregister")
	}
	if store != nil {
		t.Error("expected nil fleet store after coordinator.Deregister")
	}
}
