package beads

import (
	"context"
	"errors"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/rpc"
)

// Pool provides borrow/return semantics for *rpc.Client connections.
// daemon.ConnectionPool, daemon.ProtectedPool, and daemon.MultiPool all
// satisfy this interface via Go structural typing — no import required.
type Pool interface {
	Get(ctx context.Context) (*rpc.Client, error)
	Put(client *rpc.Client)
	PutAfterError(client *rpc.Client)
	Discard(client *rpc.Client)
}

// PooledBackend implements backend.IssueBackend with automatic connection pooling.
// It is safe for concurrent use — each method borrows a connection from the pool,
// delegates to a temporary BeadsBackend, and returns the connection.
type PooledBackend struct {
	pool Pool
}

// Compile-time interface check.
var _ backend.IssueBackend = (*PooledBackend)(nil)

// NewPooledBackend creates a PooledBackend that borrows connections from pool.
// Panics if pool is nil — a nil pool is a programming error.
func NewPooledBackend(pool Pool) *PooledBackend {
	if pool == nil {
		panic("beads.NewPooledBackend: pool must not be nil")
	}
	return &PooledBackend{pool: pool}
}

func (pb *PooledBackend) BackendName() string { return "beads-pooled" }

// execPool borrows a connection, delegates to a temporary BeadsBackend, and returns the connection.
// The connection is always returned via defer to prevent leaks if fn panics.
func execPool[T any](ctx context.Context, pb *PooledBackend, op string, fn func(*BeadsBackend) (T, error)) (T, error) {
	var zero T
	client, err := pb.pool.Get(ctx)
	if err != nil {
		return zero, backend.ErrUnavailable(op, "pool: failed to acquire connection", err)
	}
	b := New(client)
	var fnErr error
	defer func() { pb.returnClient(client, fnErr) }()
	result, fnErr := fn(b)
	return result, fnErr
}

// returnClient inspects the error to decide how to return the connection to the pool.
func (pb *PooledBackend) returnClient(client *rpc.Client, err error) {
	if err == nil {
		pb.pool.Put(client)
		return
	}
	var be *backend.BackendError
	if !errors.As(err, &be) {
		pb.pool.PutAfterError(client)
		return
	}
	switch be.Kind {
	case backend.KindUnavailable, backend.KindTimeout, backend.KindCanceled:
		pb.pool.Discard(client)
	default:
		pb.pool.Put(client)
	}
}

// --- Query operations ---

func (pb *PooledBackend) Get(ctx context.Context, id string) (*backend.IssueDetailData, error) {
	return execPool(ctx, pb, "Get", func(b *BeadsBackend) (*backend.IssueDetailData, error) {
		return b.Get(ctx, id)
	})
}

func (pb *PooledBackend) List(ctx context.Context, opts backend.ListOpts) ([]backend.IssueData, error) {
	return execPool(ctx, pb, "List", func(b *BeadsBackend) ([]backend.IssueData, error) {
		return b.List(ctx, opts)
	})
}

func (pb *PooledBackend) Ready(ctx context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
	return execPool(ctx, pb, "Ready", func(b *BeadsBackend) ([]backend.IssueData, error) {
		return b.Ready(ctx, opts)
	})
}

func (pb *PooledBackend) Blocked(ctx context.Context, opts backend.BlockedOpts) ([]backend.IssueData, error) {
	return execPool(ctx, pb, "Blocked", func(b *BeadsBackend) ([]backend.IssueData, error) {
		return b.Blocked(ctx, opts)
	})
}

func (pb *PooledBackend) Stats(ctx context.Context) (*backend.StatsData, error) {
	return execPool(ctx, pb, "Stats", func(b *BeadsBackend) (*backend.StatsData, error) {
		return b.Stats(ctx)
	})
}

func (pb *PooledBackend) Count(ctx context.Context, opts backend.CountOpts) (int, error) {
	return execPool(ctx, pb, "Count", func(b *BeadsBackend) (int, error) {
		return b.Count(ctx, opts)
	})
}

func (pb *PooledBackend) GetChildren(ctx context.Context, id string) ([]backend.IssueData, error) {
	return execPool(ctx, pb, "GetChildren", func(b *BeadsBackend) ([]backend.IssueData, error) {
		return b.GetChildren(ctx, id)
	})
}

// --- Mutation operations ---

func (pb *PooledBackend) Create(ctx context.Context, params backend.CreateParams) (*backend.IssueData, error) {
	return execPool(ctx, pb, "Create", func(b *BeadsBackend) (*backend.IssueData, error) {
		return b.Create(ctx, params)
	})
}

func (pb *PooledBackend) Update(ctx context.Context, id string, params backend.UpdateParams) error {
	_, err := execPool(ctx, pb, "Update", func(b *BeadsBackend) (struct{}, error) {
		return struct{}{}, b.Update(ctx, id, params)
	})
	return err
}

func (pb *PooledBackend) ClaimIssue(ctx context.Context, id string, lockTTL time.Duration) error {
	_, err := execPool(ctx, pb, "ClaimIssue", func(b *BeadsBackend) (struct{}, error) {
		return struct{}{}, b.ClaimIssue(ctx, id, lockTTL)
	})
	return err
}

func (pb *PooledBackend) Close(ctx context.Context, id string, params backend.CloseParams) (*backend.CloseResult, error) {
	return execPool(ctx, pb, "Close", func(b *BeadsBackend) (*backend.CloseResult, error) {
		return b.Close(ctx, id, params)
	})
}

func (pb *PooledBackend) Reopen(ctx context.Context, id string, params backend.ReopenParams) error {
	_, err := execPool(ctx, pb, "Reopen", func(b *BeadsBackend) (struct{}, error) {
		return struct{}{}, b.Reopen(ctx, id, params)
	})
	return err
}

func (pb *PooledBackend) Delete(ctx context.Context, params backend.DeleteParams) error {
	_, err := execPool(ctx, pb, "Delete", func(b *BeadsBackend) (struct{}, error) {
		return struct{}{}, b.Delete(ctx, params)
	})
	return err
}

// --- Dependency operations ---

func (pb *PooledBackend) AddDependency(ctx context.Context, params backend.DepAddParams) error {
	_, err := execPool(ctx, pb, "AddDependency", func(b *BeadsBackend) (struct{}, error) {
		return struct{}{}, b.AddDependency(ctx, params)
	})
	return err
}

func (pb *PooledBackend) RemoveDependency(ctx context.Context, params backend.DepRemoveParams) error {
	_, err := execPool(ctx, pb, "RemoveDependency", func(b *BeadsBackend) (struct{}, error) {
		return struct{}{}, b.RemoveDependency(ctx, params)
	})
	return err
}

// --- Label operations ---

func (pb *PooledBackend) AddLabel(ctx context.Context, id string, label string) error {
	_, err := execPool(ctx, pb, "AddLabel", func(b *BeadsBackend) (struct{}, error) {
		return struct{}{}, b.AddLabel(ctx, id, label)
	})
	return err
}

func (pb *PooledBackend) RemoveLabel(ctx context.Context, id string, label string) error {
	_, err := execPool(ctx, pb, "RemoveLabel", func(b *BeadsBackend) (struct{}, error) {
		return struct{}{}, b.RemoveLabel(ctx, id, label)
	})
	return err
}

// --- Comment operations ---

func (pb *PooledBackend) ListComments(ctx context.Context, id string) ([]backend.CommentData, error) {
	return execPool(ctx, pb, "ListComments", func(b *BeadsBackend) ([]backend.CommentData, error) {
		return b.ListComments(ctx, id)
	})
}

func (pb *PooledBackend) AddComment(ctx context.Context, params backend.CommentAddParams) (*backend.CommentData, error) {
	return execPool(ctx, pb, "AddComment", func(b *BeadsBackend) (*backend.CommentData, error) {
		return b.AddComment(ctx, params)
	})
}

// --- Event operations ---

func (pb *PooledBackend) ListEvents(ctx context.Context, id string, limit int) ([]backend.EventData, error) {
	return execPool(ctx, pb, "ListEvents", func(b *BeadsBackend) ([]backend.EventData, error) {
		return b.ListEvents(ctx, id, limit)
	})
}

// --- Batch operations ---

func (pb *PooledBackend) Batch(ctx context.Context, ops []backend.BatchOp) ([]backend.BatchResult, error) {
	return execPool(ctx, pb, "Batch", func(b *BeadsBackend) ([]backend.BatchResult, error) {
		return b.Batch(ctx, ops)
	})
}

// --- Mutation polling ---

func (pb *PooledBackend) GetMutations(ctx context.Context, sinceMs int64) ([]backend.MutationData, error) {
	return execPool(ctx, pb, "GetMutations", func(b *BeadsBackend) ([]backend.MutationData, error) {
		return b.GetMutations(ctx, sinceMs)
	})
}

func (pb *PooledBackend) WaitForMutations(ctx context.Context, sinceMs int64, timeoutMs int64) ([]backend.MutationData, error) {
	return execPool(ctx, pb, "WaitForMutations", func(b *BeadsBackend) ([]backend.MutationData, error) {
		return b.WaitForMutations(ctx, sinceMs, timeoutMs)
	})
}
