package supervisor

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/cli/clitest"
	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type renewingActorBackend struct {
	*clitest.MockIssueBackend
	claims atomic.Int64
	taskID string
	actor  string
	ttl    time.Duration
}

func (b *renewingActorBackend) RenewIssueClaimAsActor(_ context.Context, taskID string, ttl time.Duration, actor string) error {
	b.taskID = taskID
	b.actor = actor
	b.ttl = ttl
	b.claims.Add(1)
	return nil
}

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

func TestStartWorkerHeartbeatEvery_RenewsAssignedIssueClaim(t *testing.T) {
	issueBackend := &renewingActorBackend{MockIssueBackend: clitest.NewMockIssueBackend()}
	issueBackend.GetResult = &backend.IssueDetailData{IssueData: backend.IssueData{ID: "BUG-23", Status: "in_progress"}}
	s := &Supervisor{
		IssueBackend: issueBackend,
		WorkspaceID:  "WS",
		Shutdown:     make(chan struct{}),
	}
	ap := &AgentProcess{
		Entry:          cfgpkg.AgentEntry{Worktree: "bug-triage"},
		AssignedTaskID: "BUG-23",
	}

	stop := s.startWorkerHeartbeatEvery(ap, 10*time.Millisecond)
	waitForCount(t, issueBackend.claims.Load, 2)
	stop()

	if issueBackend.taskID != "BUG-23" || issueBackend.actor != "bug-triage" {
		t.Fatalf("renewed claim = task %q actor %q, want BUG-23/bug-triage", issueBackend.taskID, issueBackend.actor)
	}
	if issueBackend.ttl != 0 {
		t.Fatalf("renewed claim TTL = %v, want server default", issueBackend.ttl)
	}
}

func TestRenewAssignedTaskClaim_DelegatesReviewRaceToRenewOnlyContract(t *testing.T) {
	issueBackend := &renewingActorBackend{MockIssueBackend: clitest.NewMockIssueBackend()}
	issueBackend.GetResult = &backend.IssueDetailData{IssueData: backend.IssueData{ID: "BUG-23", Status: "review"}}
	s := &Supervisor{
		IssueBackend: issueBackend,
		WorkspaceID:  "WS",
		Shutdown:     make(chan struct{}),
	}
	ap := &AgentProcess{
		Entry:          cfgpkg.AgentEntry{Worktree: "bug-triage"},
		AssignedTaskID: "BUG-23",
	}

	if err := s.renewAssignedTaskClaim(ap); err != nil {
		t.Fatalf("renewAssignedTaskClaim: %v", err)
	}
	if got := issueBackend.claims.Load(); got != 1 {
		t.Fatalf("renew-only calls after Review handoff = %d, want 1 authoritative server check", got)
	}
}

func TestDeregisterWorker_CallsStore(t *testing.T) {
	s, cw := newWorkerHeartbeatSupervisor()
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "agent-wt"}}
	s.deregisterWorker(ap)
	if got := cw.deregisters.Load(); got != 1 {
		t.Errorf("deregisters = %d, want 1", got)
	}
}
