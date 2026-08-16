package memstore

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

// workerStore is a no-op in-memory WorkerStore. In production a worker
// registration is created server-side as a side-effect of claiming an issue
// (fleet-db); the in-memory store has nothing to renew or remove, so both
// methods succeed without tracking state.
type workerStore struct{}

var _ execution.WorkerStore = (*workerStore)(nil)

func newWorkerStore() *workerStore { return &workerStore{} }

func (s *workerStore) Heartbeat(_ context.Context, _, _ string) error  { return nil }
func (s *workerStore) Deregister(_ context.Context, _, _ string) error { return nil }
