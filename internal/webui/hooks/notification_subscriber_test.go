package hooks

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
	"github.com/tysonthomas9/loomcli/internal/webui/server/realtime"
)

// safePool implements daemon.Pool but returns errors from Get so the
// DaemonSubscriber's background loop retries without a nil-pointer panic.
type safePool struct{ spyPool }

func (p *safePool) Get(_ context.Context) (*rpc.Client, error) {
	return nil, errors.New("stub pool: no real connection")
}

// newTestNotificationHookEnv creates the test environment for
// NotificationSubscriberHook tests: a Hub, MultiPool, MultiWorkspaceSubscriber,
// and the hook itself. All resources are cleaned up via t.Cleanup.
func newTestNotificationHookEnv(t *testing.T) (*NotificationSubscriberHook, *MultiWorkspaceSubscriber, *daemon.MultiPool) {
	t.Helper()

	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	multiPool := daemon.NewMultiPool(middleware.WorkspaceFromContext, 1)
	t.Cleanup(func() { _ = multiPool.Close() })

	multiSub := NewMultiWorkspaceSubscriber(hub, multiPool, slog.Default())
	t.Cleanup(multiSub.Stop)

	hook := NewNotificationSubscriberHook(multiSub, slog.Default())
	return hook, multiSub, multiPool
}

// registerSafePool registers a safePool in MultiPool for the given workspace ID.
// The safePool returns errors from Get so the subscriber loop retries safely
// rather than panicking on a zero-value rpc.Client.
func registerSafePool(t *testing.T, mp *daemon.MultiPool, wsID string) *safePool {
	t.Helper()
	pool := &safePool{}
	if err := mp.Register(wsID, pool); err != nil {
		t.Fatalf("failed to register stub pool for %q: %v", wsID, err)
	}
	return pool
}

func TestNotificationSubscriberHook_Name(t *testing.T) {
	hook, _, _ := newTestNotificationHookEnv(t)
	if got := hook.Name(); got != "notification-subscriber" {
		t.Errorf("Name() = %q, want %q", got, "notification-subscriber")
	}
}

func TestNotificationSubscriberHook_Critical(t *testing.T) {
	hook, _, _ := newTestNotificationHookEnv(t)
	if hook.Critical() {
		t.Error("Critical() = true, want false")
	}
}

func TestNotificationSubscriberHook_OnRegister_StartsSubscriber(t *testing.T) {
	hook, multiSub, mp := newTestNotificationHookEnv(t)
	registerSafePool(t, mp, "ws-1")

	ctx := regCtx("ws-1", "/tmp/ws1")
	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister returned error: %v", err)
	}

	// Verify subscriber is active.
	ids := multiSub.WorkspaceIDs()
	if len(ids) != 1 || ids[0] != "ws-1" {
		t.Errorf("WorkspaceIDs() = %v, want [ws-1]", ids)
	}

	// Verify resource was provided.
	res, ok := ctx.Resolve(coordinator.ResourceKeySubscriber)
	if !ok {
		t.Fatal("expected ResourceKeySubscriber to be provided")
	}
	if res != multiSub {
		t.Error("expected provided resource to be the MultiWorkspaceSubscriber")
	}
}

func TestNotificationSubscriberHook_OnRegister_NoPool_ReturnsError(t *testing.T) {
	hook, multiSub, _ := newTestNotificationHookEnv(t)

	ctx := regCtx("ws-no-pool", "/tmp/ws")
	err := hook.OnRegister(ctx)
	if err == nil {
		t.Fatal("expected error when no pool registered, got nil")
	}

	// Verify subscriber was NOT added.
	ids := multiSub.WorkspaceIDs()
	for _, id := range ids {
		if id == "ws-no-pool" {
			t.Error("workspace should not appear in WorkspaceIDs after failed register")
		}
	}
}

func TestNotificationSubscriberHook_OnRegister_ReplacesExisting(t *testing.T) {
	hook, multiSub, mp := newTestNotificationHookEnv(t)
	registerSafePool(t, mp, "ws-1")

	// Register twice.
	ctx1 := regCtx("ws-1", "/tmp/ws1")
	if err := hook.OnRegister(ctx1); err != nil {
		t.Fatalf("first OnRegister: %v", err)
	}

	ctx2 := regCtx("ws-1", "/tmp/ws1")
	if err := hook.OnRegister(ctx2); err != nil {
		t.Fatalf("second OnRegister: %v", err)
	}

	// Verify only one entry (not duplicated).
	ids := multiSub.WorkspaceIDs()
	if len(ids) != 1 || ids[0] != "ws-1" {
		t.Errorf("WorkspaceIDs() = %v, want [ws-1]", ids)
	}
}

func TestNotificationSubscriberHook_OnDeregister_StopsSubscriber(t *testing.T) {
	hook, multiSub, mp := newTestNotificationHookEnv(t)
	registerSafePool(t, mp, "ws-1")

	ctx := regCtx("ws-1", "/tmp/ws1")
	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister: %v", err)
	}

	hook.OnDeregister(deregCtx("ws-1"))

	ids := multiSub.WorkspaceIDs()
	for _, id := range ids {
		if id == "ws-1" {
			t.Error("workspace should not appear in WorkspaceIDs after deregister")
		}
	}
}

func TestNotificationSubscriberHook_OnDeregister_UnknownWorkspace(t *testing.T) {
	hook, _, _ := newTestNotificationHookEnv(t)
	// Should not panic.
	hook.OnDeregister(deregCtx("nonexistent"))
}

func TestNotificationSubscriberHook_OnRollback_SameAsDeregister(t *testing.T) {
	hook, multiSub, mp := newTestNotificationHookEnv(t)
	registerSafePool(t, mp, "ws-1")

	ctx := regCtx("ws-1", "/tmp/ws1")
	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister: %v", err)
	}

	hook.OnRollback(deregCtx("ws-1"))

	ids := multiSub.WorkspaceIDs()
	for _, id := range ids {
		if id == "ws-1" {
			t.Error("workspace should not appear in WorkspaceIDs after rollback")
		}
	}
}

func TestNotificationSubscriberHook_OnRollback_AfterFailedRegister(t *testing.T) {
	hook, _, _ := newTestNotificationHookEnv(t)

	// OnRegister fails (no pool).
	ctx := regCtx("ws-fail", "/tmp/ws")
	_ = hook.OnRegister(ctx)

	// OnRollback should be safe (RemoveWorkspace is no-op for unknown IDs).
	hook.OnRollback(deregCtx("ws-fail"))
}

func TestNotificationSubscriberHook_NilMultiSub_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic with nil multiSub, got none")
		}
	}()
	NewNotificationSubscriberHook(nil, slog.Default())
}

func TestNotificationSubscriberHook_DefaultLogger(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	defer hub.Stop()

	mp := daemon.NewMultiPool(middleware.WorkspaceFromContext, 1)
	defer mp.Close()

	multiSub := NewMultiWorkspaceSubscriber(hub, mp, slog.Default())
	defer multiSub.Stop()

	// Should not panic with nil logger.
	hook := NewNotificationSubscriberHook(multiSub, nil)
	if hook.logger == nil {
		t.Error("expected default logger to be set, got nil")
	}
}

func TestNotificationSubscriberHook_IntegrationWithCoordinatorRegistry(t *testing.T) {
	hub := realtime.NewHub()
	go hub.Run()
	t.Cleanup(hub.Stop)

	mp := daemon.NewMultiPool(middleware.WorkspaceFromContext, 1)
	t.Cleanup(func() { _ = mp.Close() })

	multiSub := NewMultiWorkspaceSubscriber(hub, mp, slog.Default())
	t.Cleanup(multiSub.Stop)

	// Set up beads-pool hook first (provides the pool that the subscriber needs).
	beadsHook := NewBeadsPoolHook(mp, 1, slog.Default())
	beadsHook.SetPrebuiltPool("ws-1", &safePool{})

	notifHook := NewNotificationSubscriberHook(multiSub, slog.Default())

	registry := coordinator.NewWorkspaceRegistry(slog.Default())
	if err := registry.AddHook(beadsHook); err != nil {
		t.Fatalf("AddHook(beads): %v", err)
	}
	if err := registry.AddHook(notifHook); err != nil {
		t.Fatalf("AddHook(notif): %v", err)
	}

	// Register workspace — both hooks should fire in order.
	if err := registry.Register("ws-1", "/tmp/ws1"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Subscriber should be active.
	ids := multiSub.WorkspaceIDs()
	if len(ids) != 1 || ids[0] != "ws-1" {
		t.Errorf("WorkspaceIDs() = %v, want [ws-1]", ids)
	}

	// Deregister — subscriber should be cleaned up (reverse order: notif then beads).
	registry.Deregister("ws-1")

	ids = multiSub.WorkspaceIDs()
	if len(ids) != 0 {
		t.Errorf("WorkspaceIDs() after deregister = %v, want []", ids)
	}
}
