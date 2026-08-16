package execution

import (
	"context"
)

type WorkerStore interface {
	// Heartbeat renews the worker's registration lease. Best-effort: a worker
	// whose lease already lapsed is reported via error, not resurrected.
	Heartbeat(ctx context.Context, workspaceKey, workerID string) error
	// Deregister removes the worker registration and releases any issue lock it
	// still holds. Idempotent.
	Deregister(ctx context.Context, workspaceKey, workerID string) error
}
