package hooks

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/workitems"
	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// stubWorkItemsFleetDB is the minimal Work Items mutation stream used by the hook tests.
type stubWorkItemsFleetDB struct{}

func (stubWorkItemsFleetDB) WaitForMutationsAfter(_ context.Context, _ string, _ int64) ([]workitems.Mutation, error) {
	return nil, errors.New("stub: not configured")
}
func (stubWorkItemsFleetDB) GetMutationsAfter(_ context.Context, _ string) ([]workitems.Mutation, error) {
	return nil, errors.New("stub: not configured")
}

// newFleetSubscriberHookEnv builds the test env: hub, multiSub, registry,
// and the hook itself. Cleanup is registered on t.
func newFleetSubscriberHookEnv(t *testing.T) (*FleetSubscriberHook, *MultiWorkspaceSubscriber, *coordinator.WorkspaceRegistry) {
	t.Helper()

	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	multiSub := NewMultiWorkspaceSubscriber(hub, slog.Default())
	t.Cleanup(multiSub.Stop)

	registry := coordinator.NewWorkspaceRegistry(slog.Default())
	hook := NewFleetSubscriberHook(multiSub, registry, slog.Default())
	return hook, multiSub, registry
}

// registerFleetWorkspaceWithBackend adds a stub fleet backend as a registered
// workspace resource. Returns the backend so the test can confirm it landed
// in the resource bag.
func registerFleetWorkspaceWithBackend(t *testing.T, registry *coordinator.WorkspaceRegistry, wsID string) workitems.MutationStream {
	t.Helper()
	be := stubWorkItemsFleetDB{}

	// Use a stub hook that just provides the resource — exactly what
	// WorkItemsFleetDBHook does in production but without the network call.
	provider := &resourceProviderHook{
		name:  "stub-workitems-fleetdb-provider",
		key:   coordinator.ResourceKeyWorkItemsFleetDB,
		value: be,
	}
	if err := registry.AddHook(provider); err != nil {
		t.Fatalf("AddHook(provider): %v", err)
	}
	if err := registry.Register(wsID, "/tmp/"+wsID); err != nil {
		t.Fatalf("Register(%q): %v", wsID, err)
	}
	return be
}

// resourceProviderHook is a tiny LifecycleHook that puts a fixed value
// into the resource bag during OnRegister. Used to inject a fleet backend
// resource without standing up the real WorkItemsFleetDBHook (which requires
// a non-empty BaseURL and a working HTTP client).
type resourceProviderHook struct {
	name  string
	key   string
	value any
}

func (h *resourceProviderHook) Name() string   { return h.name }
func (h *resourceProviderHook) Critical() bool { return false }
func (h *resourceProviderHook) OnRegister(ctx *coordinator.RegistrationContext) error {
	ctx.Provide(h.key, h.value)
	return nil
}
func (h *resourceProviderHook) OnDeregister(_ coordinator.DeregistrationContext) {}
func (h *resourceProviderHook) OnRollback(_ coordinator.DeregistrationContext)   {}

func TestFleetSubscriberHook_Name(t *testing.T) {
	hook, _, _ := newFleetSubscriberHookEnv(t)
	if got := hook.Name(); got != "fleet-subscriber" {
		t.Errorf("Name() = %q, want %q", got, "fleet-subscriber")
	}
}

func TestFleetSubscriberHook_Critical(t *testing.T) {
	hook, _, _ := newFleetSubscriberHookEnv(t)
	if hook.Critical() {
		t.Error("Critical() = true, want false")
	}
}

func TestFleetSubscriberHook_OnRegister_Defers(t *testing.T) {
	hook, multiSub, _ := newFleetSubscriberHookEnv(t)

	ctx := regCtx("ws-defer", "/tmp/ws-defer")
	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister: %v", err)
	}

	// OnRegister must not start the subscriber.
	if ids := multiSub.WorkspaceIDs(); len(ids) != 0 {
		t.Errorf("expected 0 subscribers after OnRegister, got %v", ids)
	}
}

func TestFleetSubscriberHook_Activate_StartsSubscriber(t *testing.T) {
	hook, multiSub, registry := newFleetSubscriberHookEnv(t)

	// Add hook to registry AFTER the resource provider, mirroring what
	// app.RegisterHooks does (WorkItemsFleetDBHook before FleetSubscriberHook).
	registerFleetWorkspaceWithBackend(t, registry, "ws-fleet-1")
	if err := registry.AddHook(hook); err != nil {
		t.Fatalf("AddHook(fleet-subscriber): %v", err)
	}

	if err := hook.Activate("ws-fleet-1"); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	ids := multiSub.WorkspaceIDs()
	if len(ids) != 1 || ids[0] != "ws-fleet-1" {
		t.Errorf("WorkspaceIDs() = %v, want [ws-fleet-1]", ids)
	}
}

func TestFleetSubscriberHook_Activate_Idempotent(t *testing.T) {
	hook, multiSub, registry := newFleetSubscriberHookEnv(t)

	registerFleetWorkspaceWithBackend(t, registry, "ws-fleet-idem")
	if err := registry.AddHook(hook); err != nil {
		t.Fatalf("AddHook: %v", err)
	}

	for i := 0; i < 3; i++ {
		if err := hook.Activate("ws-fleet-idem"); err != nil {
			t.Fatalf("Activate iter=%d: %v", i, err)
		}
	}

	ids := multiSub.WorkspaceIDs()
	if len(ids) != 1 {
		t.Errorf("expected exactly 1 subscriber after repeated activate, got %v", ids)
	}
}

func TestFleetSubscriberHook_Activate_EmptyWorkspaceID(t *testing.T) {
	hook, _, _ := newFleetSubscriberHookEnv(t)
	if err := hook.Activate(""); err == nil {
		t.Error("expected error from Activate with empty workspace id")
	}
}

func TestFleetSubscriberHook_Activate_UnregisteredWorkspace_NoOp(t *testing.T) {
	// Workspace was never registered; Activate must be a benign no-op
	// (mirrors WorkspaceRegistry.ActivateSubscriber's "must not resurrect
	// a deregistered workspace" contract).
	hook, multiSub, _ := newFleetSubscriberHookEnv(t)

	if err := hook.Activate("ws-never-registered"); err != nil {
		t.Errorf("expected nil error for unregistered workspace, got %v", err)
	}
	if ids := multiSub.WorkspaceIDs(); len(ids) != 0 {
		t.Errorf("expected 0 subscribers, got %v", ids)
	}
}

func TestFleetSubscriberHook_Activate_NoWorkItemsFleetDBResource(t *testing.T) {
	// Workspace is registered but no WorkItemsFleetDBHook ran, so the
	// resource bag is missing the fleet backend. Activate should log and
	// return nil (not crash) — this is the "WorkItemsFleetDBHook misconfigured"
	// safety net.
	hook, multiSub, registry := newFleetSubscriberHookEnv(t)

	if err := registry.Register("ws-no-be", "/tmp/ws-no-be"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	if err := hook.Activate("ws-no-be"); err != nil {
		t.Errorf("expected nil error when resource missing, got %v", err)
	}
	if ids := multiSub.WorkspaceIDs(); len(ids) != 0 {
		t.Errorf("expected 0 subscribers, got %v", ids)
	}
}

func TestFleetSubscriberHook_OnDeregister_StopsSubscriber(t *testing.T) {
	hook, multiSub, registry := newFleetSubscriberHookEnv(t)

	registerFleetWorkspaceWithBackend(t, registry, "ws-dereg")
	if err := registry.AddHook(hook); err != nil {
		t.Fatalf("AddHook: %v", err)
	}
	if err := hook.Activate("ws-dereg"); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if !multiSub.HasSubscriber("ws-dereg") {
		t.Fatal("expected subscriber after Activate")
	}

	hook.OnDeregister(deregCtx("ws-dereg"))

	if multiSub.HasSubscriber("ws-dereg") {
		t.Error("expected subscriber removed after OnDeregister")
	}
}

func TestFleetSubscriberHook_OnRollback_SameAsDeregister(t *testing.T) {
	hook, multiSub, registry := newFleetSubscriberHookEnv(t)

	registerFleetWorkspaceWithBackend(t, registry, "ws-rb")
	if err := registry.AddHook(hook); err != nil {
		t.Fatalf("AddHook: %v", err)
	}
	if err := hook.Activate("ws-rb"); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	hook.OnRollback(deregCtx("ws-rb"))

	if multiSub.HasSubscriber("ws-rb") {
		t.Error("expected subscriber removed after OnRollback")
	}
}

func TestFleetSubscriberHook_NilMultiSub_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic with nil multiSub")
		}
	}()
	registry := coordinator.NewWorkspaceRegistry(slog.Default())
	NewFleetSubscriberHook(nil, registry, slog.Default())
}

func TestFleetSubscriberHook_NilRegistry_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic with nil registry")
		}
	}()
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()
	multiSub := NewMultiWorkspaceSubscriber(hub, slog.Default())
	defer multiSub.Stop()
	NewFleetSubscriberHook(multiSub, nil, slog.Default())
}

func TestFleetSubscriberHook_DefaultLogger(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()
	multiSub := NewMultiWorkspaceSubscriber(hub, slog.Default())
	defer multiSub.Stop()
	registry := coordinator.NewWorkspaceRegistry(slog.Default())

	// Should not panic with nil logger; defaults to slog.Default().
	hook := NewFleetSubscriberHook(multiSub, registry, nil)
	if hook.logger == nil {
		t.Error("expected default logger when nil passed, got nil")
	}
}
