package hooks

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/coordinator"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// spyPool implements daemon.Pool and tracks whether Close was called.
type spyPool struct {
	mu     sync.Mutex
	closed bool
}

func (p *spyPool) Get(_ context.Context) (*rpc.Client, error) { return &rpc.Client{}, nil }
func (p *spyPool) Put(_ *rpc.Client)                          {}
func (p *spyPool) PutAfterError(_ *rpc.Client)                {}
func (p *spyPool) Discard(_ *rpc.Client)                      {}
func (p *spyPool) Stats() daemon.PoolStats                    { return daemon.PoolStats{} }
func (p *spyPool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	return nil
}
func (p *spyPool) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

// newTestHook creates a BeadsPoolHook with a stub pool factory for testing.
func newTestHook(t *testing.T) (*BeadsPoolHook, *daemon.MultiPool) {
	t.Helper()
	mp := daemon.NewMultiPool(middleware.WorkspaceFromContext, 10)
	t.Cleanup(func() { _ = mp.Close() })
	hook := NewBeadsPoolHook(mp, 10, slog.Default())
	// Replace pool factory with a stub that returns a mock ConnectionPool.
	// We can't create real ConnectionPool without a socket, so we'll use
	// the prebuilt pool approach for most tests and a factory-based approach
	// where we intercept at a higher level.
	return hook, mp
}

func regCtx(id, path string) *coordinator.RegistrationContext {
	return &coordinator.RegistrationContext{
		WorkspaceID:   id,
		WorkspacePath: path,
		Logger:        slog.Default(),
	}
}

func deregCtx(id string) coordinator.DeregistrationContext {
	return coordinator.DeregistrationContext{
		WorkspaceID: id,
		Logger:      slog.Default(),
	}
}

func TestBeadsPoolHook_Name(t *testing.T) {
	mp := daemon.NewMultiPool(middleware.WorkspaceFromContext, 10)
	defer mp.Close()
	hook := NewBeadsPoolHook(mp, 10, slog.Default())
	if got := hook.Name(); got != "beads-pool" {
		t.Errorf("Name() = %q, want %q", got, "beads-pool")
	}
}

func TestBeadsPoolHook_Critical(t *testing.T) {
	mp := daemon.NewMultiPool(middleware.WorkspaceFromContext, 10)
	defer mp.Close()
	hook := NewBeadsPoolHook(mp, 10, slog.Default())
	if !hook.Critical() {
		t.Error("Critical() = false, want true")
	}
}

func TestBeadsPoolHook_OnRegister_PrebuiltPool(t *testing.T) {
	hook, mp := newTestHook(t)

	pool := &spyPool{}
	hook.SetPrebuiltPool("ws-1", pool)

	ctx := regCtx("ws-1", "/tmp/ws1")
	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister returned error: %v", err)
	}

	// Verify pool is in MultiPool.
	got := mp.PoolForWorkspace("ws-1")
	if got == nil {
		t.Fatal("expected pool registered in MultiPool, got nil")
	}

	// Verify resource was provided.
	res, ok := ctx.Resolve(coordinator.ResourceKeyPool)
	if !ok {
		t.Fatal("expected ResourceKeyPool to be provided")
	}
	if res != pool {
		t.Error("expected provided resource to be the pre-built pool")
	}
}

func TestBeadsPoolHook_OnRegister_PrebuiltPoolConsumed(t *testing.T) {
	hook, mp := newTestHook(t)

	pool := &spyPool{}
	hook.SetPrebuiltPool("ws-1", pool)

	// First register — should use prebuilt.
	ctx1 := regCtx("ws-1", "/tmp/ws1")
	if err := hook.OnRegister(ctx1); err != nil {
		t.Fatalf("first OnRegister returned error: %v", err)
	}

	// Deregister to clean up.
	mp.Deregister("ws-1")

	// Install a factory that records whether it was called.
	var factoryCalled bool
	hook.poolFactory = func(string, int) (*daemon.ConnectionPool, error) {
		factoryCalled = true
		return nil, errors.New("intentional factory error")
	}

	// Second register — prebuilt was consumed, so factory should be called.
	ctx2 := regCtx("ws-1", "/tmp/ws1")
	_ = hook.OnRegister(ctx2)
	if !factoryCalled {
		t.Fatal("expected pool factory to be called after prebuilt was consumed")
	}
}

func TestBeadsPoolHook_OnRegister_PoolFactoryError(t *testing.T) {
	hook, mp := newTestHook(t)

	factoryErr := errors.New("factory failed")
	hook.poolFactory = func(string, int) (*daemon.ConnectionPool, error) {
		return nil, factoryErr
	}

	ctx := regCtx("ws-1", "/tmp/ws1")
	err := hook.OnRegister(ctx)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, factoryErr) {
		t.Errorf("expected error to wrap factoryErr, got: %v", err)
	}

	// Verify nothing registered in MultiPool.
	if got := mp.PoolForWorkspace("ws-1"); got != nil {
		t.Error("expected no pool in MultiPool after factory error")
	}
}

func TestBeadsPoolHook_OnRegister_MultiPoolClosed(t *testing.T) {
	mp := daemon.NewMultiPool(middleware.WorkspaceFromContext, 10)
	hook := NewBeadsPoolHook(mp, 10, slog.Default())

	pool := &spyPool{}
	hook.SetPrebuiltPool("ws-1", pool)

	// Close MultiPool before registering.
	mp.Close()

	ctx := regCtx("ws-1", "/tmp/ws1")
	err := hook.OnRegister(ctx)
	if err == nil {
		t.Fatal("expected error when MultiPool is closed, got nil")
	}

	// Pre-built pool should NOT be closed (caller owns it).
	if pool.isClosed() {
		t.Error("pre-built pool should not be closed on MultiPool.Register failure")
	}
}

func TestBeadsPoolHook_OnDeregister_RemovesPool(t *testing.T) {
	hook, mp := newTestHook(t)

	pool := &spyPool{}
	hook.SetPrebuiltPool("ws-1", pool)

	ctx := regCtx("ws-1", "/tmp/ws1")
	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister: %v", err)
	}

	// Deregister.
	hook.OnDeregister(deregCtx("ws-1"))

	// Pool should be gone from MultiPool.
	if got := mp.PoolForWorkspace("ws-1"); got != nil {
		t.Error("expected pool to be deregistered from MultiPool")
	}

	// Pool should be closed (MultiPool.Deregister closes it).
	if !pool.isClosed() {
		t.Error("expected pool to be closed after deregister")
	}
}

func TestBeadsPoolHook_OnDeregister_UnknownWorkspace(t *testing.T) {
	hook, _ := newTestHook(t)
	// Should not panic.
	hook.OnDeregister(deregCtx("nonexistent"))
}

func TestBeadsPoolHook_OnRollback_SameAsDeregister(t *testing.T) {
	hook, mp := newTestHook(t)

	pool := &spyPool{}
	hook.SetPrebuiltPool("ws-1", pool)

	ctx := regCtx("ws-1", "/tmp/ws1")
	if err := hook.OnRegister(ctx); err != nil {
		t.Fatalf("OnRegister: %v", err)
	}

	hook.OnRollback(deregCtx("ws-1"))

	if got := mp.PoolForWorkspace("ws-1"); got != nil {
		t.Error("expected pool to be deregistered after rollback")
	}
	if !pool.isClosed() {
		t.Error("expected pool to be closed after rollback")
	}
}

func TestBeadsPoolHook_NilMultiPool_Panics(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic with nil multiPool, got none")
		}
	}()
	NewBeadsPoolHook(nil, 10, slog.Default())
}

func TestBeadsPoolHook_DefaultLogger(t *testing.T) {
	mp := daemon.NewMultiPool(middleware.WorkspaceFromContext, 10)
	defer mp.Close()
	// Should not panic with nil logger.
	hook := NewBeadsPoolHook(mp, 10, nil)
	if hook.logger == nil {
		t.Error("expected default logger to be set, got nil")
	}
}

func TestBeadsPoolHook_SetPrebuiltPool_MultipleWorkspaces(t *testing.T) {
	hook, mp := newTestHook(t)

	pool1 := &spyPool{}
	pool2 := &spyPool{}
	hook.SetPrebuiltPool("ws-1", pool1)
	hook.SetPrebuiltPool("ws-2", pool2)

	if err := hook.OnRegister(regCtx("ws-1", "/tmp/ws1")); err != nil {
		t.Fatalf("OnRegister ws-1: %v", err)
	}
	if err := hook.OnRegister(regCtx("ws-2", "/tmp/ws2")); err != nil {
		t.Fatalf("OnRegister ws-2: %v", err)
	}

	if mp.PoolForWorkspace("ws-1") == nil {
		t.Error("ws-1 pool not registered")
	}
	if mp.PoolForWorkspace("ws-2") == nil {
		t.Error("ws-2 pool not registered")
	}
}

func TestBeadsPoolHook_IntegrationWithCoordinatorRegistry(t *testing.T) {
	mp := daemon.NewMultiPool(middleware.WorkspaceFromContext, 10)
	t.Cleanup(func() { _ = mp.Close() })

	registry := coordinator.NewWorkspaceRegistry(slog.Default())
	hook := NewBeadsPoolHook(mp, 10, slog.Default())
	hook.SetPrebuiltPool("ws-1", &spyPool{})

	if err := registry.AddHook(hook); err != nil {
		t.Fatalf("AddHook: %v", err)
	}

	if err := registry.Register("ws-1", "/tmp/ws1"); err != nil {
		t.Fatalf("Register: %v", err)
	}

	// Pool should be in MultiPool.
	if mp.PoolForWorkspace("ws-1") == nil {
		t.Error("expected pool in MultiPool after registry.Register")
	}

	// Deregister via registry.
	registry.Deregister("ws-1")
	if mp.PoolForWorkspace("ws-1") != nil {
		t.Error("expected pool removed from MultiPool after registry.Deregister")
	}
}
