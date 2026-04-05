package beads

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
)

// ---------------------------------------------------------------------------
// mockPool implements Pool for testing
// ---------------------------------------------------------------------------

type mockPool struct {
	getFn         func(ctx context.Context) (*rpc.Client, error)
	putCalls      atomic.Int32
	putAfterCalls atomic.Int32
	discardCalls  atomic.Int32
}

func (p *mockPool) Get(ctx context.Context) (*rpc.Client, error) {
	if p.getFn != nil {
		return p.getFn(ctx)
	}
	return nil, errors.New("Get not mocked")
}

func (p *mockPool) Put(_ *rpc.Client) {
	p.putCalls.Add(1)
}

func (p *mockPool) PutAfterError(_ *rpc.Client) {
	p.putAfterCalls.Add(1)
}

func (p *mockPool) Discard(_ *rpc.Client) {
	p.discardCalls.Add(1)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestPooledBackend_BackendName(t *testing.T) {
	pb := NewPooledBackend(&mockPool{})
	if got := pb.BackendName(); got != "beads-pooled" {
		t.Errorf("BackendName() = %q, want %q", got, "beads-pooled")
	}
}

func TestPooledBackend_NilPoolPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for nil pool, got none")
		}
	}()
	NewPooledBackend(nil)
}

func TestPooledBackend_GetPoolFails(t *testing.T) {
	poolErr := errors.New("pool exhausted")
	mp := &mockPool{
		getFn: func(_ context.Context) (*rpc.Client, error) {
			return nil, poolErr
		},
	}
	pb := NewPooledBackend(mp)

	_, err := pb.Get(context.Background(), "test-id")
	if err == nil {
		t.Fatal("expected error when pool.Get fails")
	}
	var be *backend.BackendError
	if !errors.As(err, &be) {
		t.Fatalf("expected *BackendError, got %T: %v", err, err)
	}
	if be.Kind != backend.KindUnavailable {
		t.Errorf("Kind = %q, want %q", be.Kind, backend.KindUnavailable)
	}
	if !errors.Is(be.Cause, poolErr) {
		t.Errorf("Cause = %v, want %v", be.Cause, poolErr)
	}
}

func TestPooledBackend_ReturnClient(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantPut      int32
		wantPutAfter int32
		wantDiscard  int32
	}{
		{
			name:    "nil error → Put",
			err:     nil,
			wantPut: 1,
		},
		{
			name:        "KindUnavailable → Discard",
			err:         backend.ErrUnavailable("op", "msg", nil),
			wantDiscard: 1,
		},
		{
			name:        "KindTimeout → Discard",
			err:         backend.ErrTimeout("op", "msg", nil),
			wantDiscard: 1,
		},
		{
			name:        "KindCanceled → Discard",
			err:         backend.ErrCanceled("op", "msg", nil),
			wantDiscard: 1,
		},
		{
			name:    "KindNotFound → Put",
			err:     backend.ErrNotFound("op", "msg"),
			wantPut: 1,
		},
		{
			name:    "KindValidation → Put",
			err:     backend.ErrValidation("op", "msg"),
			wantPut: 1,
		},
		{
			name:    "KindConflict → Put",
			err:     backend.ErrConflict("op", "msg"),
			wantPut: 1,
		},
		{
			name:    "KindInternal → Put",
			err:     backend.ErrInternal("op", "msg", nil),
			wantPut: 1,
		},
		{
			name:    "KindNotImplemented → Put",
			err:     backend.ErrNotImplemented("op", "msg"),
			wantPut: 1,
		},
		{
			name:         "non-BackendError → PutAfterError",
			err:          errors.New("unknown error"),
			wantPutAfter: 1,
		},
		{
			name:         "wrapped non-BackendError → PutAfterError",
			err:          fmt.Errorf("wrap: %w", errors.New("inner")),
			wantPutAfter: 1,
		},
		{
			name:        "wrapped KindTimeout → Discard",
			err:         fmt.Errorf("wrap: %w", backend.ErrTimeout("op", "msg", nil)),
			wantDiscard: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mp := &mockPool{}
			pb := NewPooledBackend(mp)
			pb.returnClient(nil, tt.err)

			if got := mp.putCalls.Load(); got != tt.wantPut {
				t.Errorf("Put calls = %d, want %d", got, tt.wantPut)
			}
			if got := mp.putAfterCalls.Load(); got != tt.wantPutAfter {
				t.Errorf("PutAfterError calls = %d, want %d", got, tt.wantPutAfter)
			}
			if got := mp.discardCalls.Load(); got != tt.wantDiscard {
				t.Errorf("Discard calls = %d, want %d", got, tt.wantDiscard)
			}
		})
	}
}

func TestPooledBackend_CompileTimeCheck(t *testing.T) {
	// Verify the compile-time interface satisfaction declared in pool.go.
	var _ backend.IssueBackend = (*PooledBackend)(nil)
}

func TestPooledBackend_PoolGetFailVoidMethod(t *testing.T) {
	// Verify void-returning methods also propagate pool.Get errors correctly.
	poolErr := errors.New("connection refused")
	mp := &mockPool{
		getFn: func(_ context.Context) (*rpc.Client, error) {
			return nil, poolErr
		},
	}
	pb := NewPooledBackend(mp)

	err := pb.Update(context.Background(), "id", backend.UpdateParams{})
	if err == nil {
		t.Fatal("expected error when pool.Get fails")
	}
	if !backend.IsKind(err, backend.KindUnavailable) {
		t.Errorf("error kind = %v, want KindUnavailable", err)
	}
}
