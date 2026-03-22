package webui

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/rpc"
	"github.com/tysonthomas9/loomcli/internal/webui/daemon"
)

// issueUpdater is an internal interface for testing issue updates.
// The production code uses *rpc.Client which implements this interface.
type issueUpdater interface {
	Update(args *rpc.UpdateArgs) (*rpc.Response, error)
}

// patchConnectionGetter is an internal interface for testing PATCH handler pool operations.
type patchConnectionGetter interface {
	Get(ctx context.Context) (issueUpdater, error)
	Put(client issueUpdater)
}

// patchPoolAdapter wraps daemon.Pool to implement patchConnectionGetter.
type patchPoolAdapter struct {
	pool daemon.Pool
}

func (p *patchPoolAdapter) Get(ctx context.Context) (issueUpdater, error) {
	return p.pool.Get(ctx)
}

func (p *patchPoolAdapter) Put(client issueUpdater) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

// issueCloser is an internal interface for testing issue close operations.
// The production code uses *rpc.Client which implements this interface.
type issueCloser interface {
	CloseIssue(args *rpc.CloseArgs) (*rpc.Response, error)
}

// closeConnectionGetter is an internal interface for testing close handler pool operations.
type closeConnectionGetter interface {
	Get(ctx context.Context) (issueCloser, error)
	Put(client issueCloser)
}

// closePoolAdapter wraps daemon.Pool to implement closeConnectionGetter.
type closePoolAdapter struct {
	pool daemon.Pool
}

func (p *closePoolAdapter) Get(ctx context.Context) (issueCloser, error) {
	return p.pool.Get(ctx)
}

func (p *closePoolAdapter) Put(client issueCloser) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

// issueCreator is an internal interface for testing issue creation.
// The production code uses *rpc.Client which implements this interface.
type issueCreator interface {
	Create(args *rpc.CreateArgs) (*rpc.Response, error)
}

// createConnectionGetter is an internal interface for testing connection pool operations for create.
type createConnectionGetter interface {
	Get(ctx context.Context) (issueCreator, error)
	Put(client issueCreator)
}

// createPoolAdapter wraps daemon.Pool to implement createConnectionGetter.
type createPoolAdapter struct {
	pool daemon.Pool
}

func (p *createPoolAdapter) Get(ctx context.Context) (issueCreator, error) {
	return p.pool.Get(ctx)
}

func (p *createPoolAdapter) Put(client issueCreator) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}

// issueDeleter is an internal interface for testing issue delete operations.
// The production code uses *rpc.Client which implements this interface.
type issueDeleter interface {
	Delete(args *rpc.DeleteArgs) (*rpc.Response, error)
}

// deleteConnectionGetter is an internal interface for testing delete handler pool operations.
type deleteConnectionGetter interface {
	Get(ctx context.Context) (issueDeleter, error)
	Put(client issueDeleter)
}

// deletePoolAdapter wraps daemon.Pool to implement deleteConnectionGetter.
type deletePoolAdapter struct {
	pool daemon.Pool
}

func (p *deletePoolAdapter) Get(ctx context.Context) (issueDeleter, error) {
	return p.pool.Get(ctx)
}

func (p *deletePoolAdapter) Put(client issueDeleter) {
	if c, ok := client.(*rpc.Client); ok {
		p.pool.Put(c)
	}
}
