package supervisor

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// countingWorkerStore is a store.WorkerStore that counts calls.
type countingWorkerStore struct {
	heartbeats  atomic.Int64
	deregisters atomic.Int64
}

func (c *countingWorkerStore) Heartbeat(context.Context, string, string) error {
	c.heartbeats.Add(1)
	return nil
}

func (c *countingWorkerStore) Deregister(context.Context, string, string) error {
	c.deregisters.Add(1)
	return nil
}

// workerCountingStore embeds a real store.Store and overrides Workers() with a
// counting fake, so the supervisor's ControlStore.Workers() calls are observed.
type workerCountingStore struct {
	store.Store
	workers *countingWorkerStore
}

func (s *workerCountingStore) Workers() store.WorkerStore { return s.workers }

func newWorkerHeartbeatSupervisor() (*Supervisor, *countingWorkerStore) {
	cw := &countingWorkerStore{}
	return &Supervisor{
		ControlStore: &workerCountingStore{Store: memstore.New(), workers: cw},
		WorkspaceID:  "WS",
		Shutdown:     make(chan struct{}),
	}, cw
}

func waitForCount(t *testing.T, get func() int64, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for get() < want {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for count >= %d (got %d)", want, get())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestStartWorkerHeartbeatEvery_RenewsUntilStopped(t *testing.T) {
	s, cw := newWorkerHeartbeatSupervisor()
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "agent-wt"}}

	stop := s.startWorkerHeartbeatEvery(ap, 10*time.Millisecond)
	waitForCount(t, cw.heartbeats.Load, 2)
	stop() // blocks until the goroutine exits, so no beat can land afterward

	got := cw.heartbeats.Load()
	time.Sleep(40 * time.Millisecond)
	if after := cw.heartbeats.Load(); after != got {
		t.Errorf("heartbeats continued after stop: %d -> %d", got, after)
	}
}

func TestStartWorkerHeartbeatEvery_NoControlStoreIsNoOp(t *testing.T) {
	s := &Supervisor{Shutdown: make(chan struct{})} // no ControlStore / WorkspaceID
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "wt"}}
	// Must return a usable no-op stop and never panic.
	stop := s.startWorkerHeartbeatEvery(ap, time.Millisecond)
	stop()
}

func TestDeregisterWorker_CallsStore(t *testing.T) {
	s, cw := newWorkerHeartbeatSupervisor()
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "agent-wt"}}
	s.deregisterWorker(ap)
	if got := cw.deregisters.Load(); got != 1 {
		t.Errorf("deregisters = %d, want 1", got)
	}
}
