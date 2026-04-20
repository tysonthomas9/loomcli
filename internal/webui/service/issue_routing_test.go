package service

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
	"github.com/tysonthomas9/loomcli/internal/webui/server/middleware"
)

// countingPool counts Get calls so the regression test can verify which
// per-workspace pool the IssueService routed to. Each Get returns a fresh
// rpc.Client so MultiPool's owner tracking can route Put back to the right
// pool via its internal clientOwner map.
type countingPool struct {
	getCalls int
	putCalls int
	mu       sync.Mutex
}

func (p *countingPool) Get(_ context.Context) (*rpc.Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.getCalls++
	// Allocate a fresh client each call so MultiPool's clientOwner map can
	// track this client back to this pool when Put is called.
	return &rpc.Client{}, nil
}
func (p *countingPool) Put(_ *rpc.Client) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.putCalls++
}
func (p *countingPool) PutAfterError(_ *rpc.Client) {}
func (p *countingPool) Discard(_ *rpc.Client)       {}
func (p *countingPool) Stats() daemon.PoolStats     { return daemon.PoolStats{} }
func (p *countingPool) Close() error                { return nil }

func (p *countingPool) Gets() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.getCalls
}

// TestIssueService_AcquireClient_RoutesByWorkspaceContext is a regression test
// for the cross-workspace data leak: every workspace's /issues endpoint used
// to return the same data because issueServiceImpl.acquireClient called
// s.pool.Get() (a single default pool) instead of s.multiPool.Get(ctx) (the
// per-workspace router). After the fix the service must hit pool A when the
// context carries workspace A, pool B for workspace B, and error out when no
// workspace is in context.
func TestIssueService_AcquireClient_RoutesByWorkspaceContext(t *testing.T) {
	mp := daemon.NewMultiPool(middleware.WorkspaceFromContext, 10)
	poolA := &countingPool{}
	poolB := &countingPool{}
	if err := mp.Register("ws-A", poolA); err != nil {
		t.Fatalf("register A: %v", err)
	}
	if err := mp.Register("ws-B", poolB); err != nil {
		t.Fatalf("register B: %v", err)
	}

	svc := &issueServiceImpl{multiPool: mp, withWorkspaceFn: middleware.WithWorkspace}

	assertRoutes := func(t *testing.T, wsID string, target, other *countingPool) {
		t.Helper()
		startTarget, startOther := target.Gets(), other.Gets()
		ctx := middleware.WithWorkspace(context.Background(), wsID)
		client, err := svc.acquireClient(ctx)
		if err != nil {
			t.Fatalf("acquireClient(%s): %v", wsID, err)
		}
		defer func() {
			ok := true
			svc.releaseClient(client, &ok)
		}()
		if got := target.Gets(); got != startTarget+1 {
			t.Errorf("target pool Gets = %d, want %d", got, startTarget+1)
		}
		if got := other.Gets(); got != startOther {
			t.Errorf("other pool Gets = %d, want %d (must not be touched)", got, startOther)
		}
	}

	t.Run("workspace A in context routes to pool A", func(t *testing.T) {
		assertRoutes(t, "ws-A", poolA, poolB)
	})
	t.Run("workspace B in context routes to pool B", func(t *testing.T) {
		assertRoutes(t, "ws-B", poolB, poolA)
	})

	t.Run("no workspace in context fails loudly", func(t *testing.T) {
		_, err := svc.acquireClient(context.Background())
		if err == nil {
			t.Fatal("acquireClient with no workspace in context: expected error, got nil")
		}
		var serr *ServiceError
		if !errors.As(err, &serr) {
			t.Fatalf("expected *ServiceError, got %T: %v", err, err)
		}
		if serr.Kind != KindInternal {
			t.Errorf("expected KindInternal (server bug — middleware missing), got %q", serr.Kind)
		}
	})

	t.Run("unknown workspace returns NotFound", func(t *testing.T) {
		ctx := middleware.WithWorkspace(context.Background(), "ws-does-not-exist")
		_, err := svc.acquireClient(ctx)
		if err == nil {
			t.Fatal("expected error for unregistered workspace, got nil")
		}
		var serr *ServiceError
		if !errors.As(err, &serr) {
			t.Fatalf("expected *ServiceError, got %T: %v", err, err)
		}
		if serr.Kind != KindNotFound {
			t.Errorf("expected KindNotFound, got %q", serr.Kind)
		}
	})
}
