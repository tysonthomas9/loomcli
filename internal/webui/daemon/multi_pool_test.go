package daemon

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/rpc"
)

// mockPool is a minimal Pool implementation for testing MultiPool routing.
type mockPool struct {
	mu                 sync.Mutex
	getCalls           int
	putCalls           int
	putAfterErrorCalls int
	discardCalls       int
	closed             bool
	getErr             error
}

func (m *mockPool) Get(_ context.Context) (*rpc.Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.getCalls++
	if m.getErr != nil {
		return nil, m.getErr
	}
	return &rpc.Client{}, nil
}

func (m *mockPool) Put(_ *rpc.Client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.putCalls++
}

func (m *mockPool) PutAfterError(_ *rpc.Client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.putAfterErrorCalls++
}

func (m *mockPool) Discard(_ *rpc.Client) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.discardCalls++
}

func (m *mockPool) Stats() PoolStats {
	return PoolStats{Size: 10, Created: 1}
}

func (m *mockPool) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	return nil
}

func extractWS(ctx context.Context) string {
	v, _ := ctx.Value("workspace").(string)
	return v
}

func ctxWithWS(ws string) context.Context {
	return context.WithValue(context.Background(), "workspace", ws) //nolint:staticcheck // test-only context key
}

func TestMultiPool_Get_DispatchesToCorrectPool(t *testing.T) {
	mp := NewMultiPool(extractWS, 10)

	poolA := &mockPool{}
	poolB := &mockPool{}

	if err := mp.Register("ws-a", poolA); err != nil {
		t.Fatal(err)
	}
	if err := mp.Register("ws-b", poolB); err != nil {
		t.Fatal(err)
	}

	// Get from ws-a
	client, err := mp.Get(ctxWithWS("ws-a"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if poolA.getCalls != 1 {
		t.Errorf("expected poolA.getCalls=1, got %d", poolA.getCalls)
	}
	if poolB.getCalls != 0 {
		t.Errorf("expected poolB.getCalls=0, got %d", poolB.getCalls)
	}

	// Get from ws-b
	_, err = mp.Get(ctxWithWS("ws-b"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if poolB.getCalls != 1 {
		t.Errorf("expected poolB.getCalls=1, got %d", poolB.getCalls)
	}
}

func TestMultiPool_Get_NoWorkspaceInContext(t *testing.T) {
	mp := NewMultiPool(extractWS, 10)

	_, err := mp.Get(context.Background())
	if err == nil {
		t.Fatal("expected error for missing workspace ID")
	}
	if !errors.Is(err, ErrNoWorkspaceInContext) {
		t.Errorf("expected ErrNoWorkspaceInContext, got: %v", err)
	}
}

func TestMultiPool_Get_WorkspaceNotRegistered(t *testing.T) {
	mp := NewMultiPool(extractWS, 10)

	_, err := mp.Get(ctxWithWS("nonexistent"))
	if err == nil {
		t.Fatal("expected error for unregistered workspace")
	}
	if !errors.Is(err, ErrWorkspaceNotRegistered) {
		t.Errorf("expected ErrWorkspaceNotRegistered, got: %v", err)
	}
}

func TestMultiPool_Register_ReplacesExisting(t *testing.T) {
	mp := NewMultiPool(extractWS, 10)

	old := &mockPool{}
	replacement := &mockPool{}

	if err := mp.Register("ws", old); err != nil {
		t.Fatal(err)
	}
	if err := mp.Register("ws", replacement); err != nil {
		t.Fatal(err)
	}

	if !old.closed {
		t.Error("expected old pool to be closed on replacement")
	}

	// New Get should go to replacement
	_, err := mp.Get(ctxWithWS("ws"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if replacement.getCalls != 1 {
		t.Errorf("expected replacement.getCalls=1, got %d", replacement.getCalls)
	}
}

func TestMultiPool_Deregister(t *testing.T) {
	mp := NewMultiPool(extractWS, 10)

	p := &mockPool{}
	if err := mp.Register("ws", p); err != nil {
		t.Fatal(err)
	}

	mp.Deregister("ws")

	if !p.closed {
		t.Error("expected pool to be closed on deregister")
	}

	_, err := mp.Get(ctxWithWS("ws"))
	if !errors.Is(err, ErrWorkspaceNotRegistered) {
		t.Errorf("expected ErrWorkspaceNotRegistered after deregister, got: %v", err)
	}
}

func TestMultiPool_Deregister_Nonexistent(t *testing.T) {
	mp := NewMultiPool(extractWS, 10)
	// Should not panic
	mp.Deregister("nonexistent")
}

func TestMultiPool_Close(t *testing.T) {
	mp := NewMultiPool(extractWS, 10)

	poolA := &mockPool{}
	poolB := &mockPool{}

	_ = mp.Register("a", poolA)
	_ = mp.Register("b", poolB)

	if err := mp.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !poolA.closed {
		t.Error("expected poolA to be closed")
	}
	if !poolB.closed {
		t.Error("expected poolB to be closed")
	}

	// Register after close should fail
	if err := mp.Register("c", &mockPool{}); !errors.Is(err, ErrPoolClosed) {
		t.Errorf("expected ErrPoolClosed after Close, got: %v", err)
	}
}

func TestMultiPool_Close_Idempotent(t *testing.T) {
	mp := NewMultiPool(extractWS, 10)
	if err := mp.Close(); err != nil {
		t.Fatal(err)
	}
	if err := mp.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMultiPool_Stats_Aggregated(t *testing.T) {
	mp := NewMultiPool(extractWS, 10)
	_ = mp.Register("a", &mockPool{})
	_ = mp.Register("b", &mockPool{})

	stats := mp.Stats()
	// Each mockPool returns Size=10, Created=1
	if stats.Size != 20 {
		t.Errorf("expected aggregated Size=20, got %d", stats.Size)
	}
	if stats.Created != 2 {
		t.Errorf("expected aggregated Created=2, got %d", stats.Created)
	}
}

func TestMultiPool_WorkspaceIDs(t *testing.T) {
	mp := NewMultiPool(extractWS, 10)
	_ = mp.Register("alpha", &mockPool{})
	_ = mp.Register("beta", &mockPool{})

	ids := mp.WorkspaceIDs()
	if len(ids) != 2 {
		t.Fatalf("expected 2 workspace IDs, got %d", len(ids))
	}

	found := map[string]bool{}
	for _, id := range ids {
		found[id] = true
	}
	if !found["alpha"] || !found["beta"] {
		t.Errorf("expected alpha and beta in IDs, got %v", ids)
	}
}

func TestMultiPool_PoolForWorkspace(t *testing.T) {
	mp := NewMultiPool(extractWS, 10)
	p := &mockPool{}
	_ = mp.Register("ws", p)

	got := mp.PoolForWorkspace("ws")
	if got != p {
		t.Error("expected to get back the registered pool")
	}

	got = mp.PoolForWorkspace("nonexistent")
	if got != nil {
		t.Error("expected nil for nonexistent workspace")
	}
}

func TestMultiPool_PutAfterError(t *testing.T) {
	mp := NewMultiPool(extractWS, 10)

	p := &mockPool{}
	if err := mp.Register("ws", p); err != nil {
		t.Fatal(err)
	}

	client, err := mp.Get(ctxWithWS("ws"))
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}

	mp.PutAfterError(client)

	p.mu.Lock()
	puts := p.putAfterErrorCalls
	p.mu.Unlock()

	if puts != 1 {
		t.Errorf("expected putAfterErrorCalls=1, got %d", puts)
	}
}

func TestMultiPool_PutAfterError_Nil(t *testing.T) {
	mp := NewMultiPool(extractWS, 10)
	_ = mp.Register("ws", &mockPool{})

	// PutAfterError(nil) should be safe (no panic)
	mp.PutAfterError(nil)
}

func TestMultiPool_ReturnsClientsToIssuingPool(t *testing.T) {
	mp := NewMultiPool(extractWS, 10)
	p := &mockPool{}
	if err := mp.Register("ws", p); err != nil {
		t.Fatal(err)
	}

	client, err := mp.Get(ctxWithWS("ws"))
	if err != nil {
		t.Fatalf("Get for Put: %v", err)
	}
	mp.Put(client)
	mp.Put(client)
	if p.putCalls != 1 {
		t.Fatalf("putCalls = %d, want 1", p.putCalls)
	}

	client, err = mp.Get(ctxWithWS("ws"))
	if err != nil {
		t.Fatalf("Get for PutAfterError: %v", err)
	}
	mp.PutAfterError(client)
	if p.putAfterErrorCalls != 1 {
		t.Fatalf("putAfterErrorCalls = %d, want 1", p.putAfterErrorCalls)
	}

	client, err = mp.Get(ctxWithWS("ws"))
	if err != nil {
		t.Fatalf("Get for Discard: %v", err)
	}
	mp.Discard(client)
	mp.Discard(client)
	if p.discardCalls != 1 {
		t.Fatalf("discardCalls = %d, want 1", p.discardCalls)
	}

	mp.Put(nil)
	mp.Discard(nil)
}

func TestMultiPool_ConcurrentAccess(t *testing.T) {
	mp := NewMultiPool(extractWS, 10)
	_ = mp.Register("ws", &mockPool{})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = mp.Get(ctxWithWS("ws"))
		}()
	}
	wg.Wait()
}
