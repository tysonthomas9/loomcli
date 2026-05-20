package supervisor

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	cfgpkg "github.com/tysonthomas9/loomcli/internal/cli/config"
	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestAcquireAgentOwnershipContinuesWhenBackendDoesNotSupportOwnershipLeases(t *testing.T) {
	base := memstore.New()
	s := newControlPlaneTestSupervisor(base)
	s.ControlStore = &controlPlaneStoreOverrides{
		Store:     base,
		ownership: unsupportedAgentOwnershipLeaseStore{AgentOwnershipLeaseStore: base.AgentOwnershipLeases()},
	}
	ap := &AgentProcess{
		Entry:  cfgpkg.AgentEntry{Worktree: "worker-1", Role: "task"},
		StopCh: make(chan struct{}),
	}
	ap.OwnershipLeaseID = "stale-lease"
	ap.OwnershipLeaseToken = "stale-token"
	ap.OwnershipFencingToken = 99
	ap.OwnershipLastHeartbeat = time.Now()

	if !s.acquireAgentOwnership(ap) {
		t.Fatal("acquireAgentOwnership returned false for unsupported ownership lease backend")
	}

	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.OwnershipLeaseID != "" || ap.OwnershipLeaseToken != "" || ap.OwnershipFencingToken != 0 || !ap.OwnershipLastHeartbeat.IsZero() {
		t.Fatalf("ownership state was not cleared: lease=%q token=%q fencing=%d heartbeat=%s", ap.OwnershipLeaseID, ap.OwnershipLeaseToken, ap.OwnershipFencingToken, ap.OwnershipLastHeartbeat)
	}
}

type unsupportedAgentOwnershipLeaseStore struct {
	store.AgentOwnershipLeaseStore
}

func (s unsupportedAgentOwnershipLeaseStore) Acquire(context.Context, store.AgentOwnershipLeaseAcquire) (*domain.AgentOwnershipLease, error) {
	return nil, fmt.Errorf("agent ownership leases unsupported: %w", domain.ErrNotFound)
}

func TestOwnershipNoopAndErrorBranches(t *testing.T) {
	if (&Supervisor{}).acquireAgentOwnership(&AgentProcess{}) != true {
		t.Fatal("acquireAgentOwnership without control store should continue")
	}

	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })
	boom := errors.New("boom")
	s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})
	s.ControlStore = &controlPlaneStoreOverrides{
		Store:     st,
		ownership: erroringOwnershipLeaseStore{AgentOwnershipLeaseStore: st.AgentOwnershipLeases(), acquireErr: boom},
	}
	s.WorkspaceID = "WS"
	s.NodeID = "node-1"
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "worker", Role: "task"}}
	if s.acquireAgentOwnership(ap) {
		t.Fatal("acquireAgentOwnership returned true for generic acquire error")
	}

	ap.OwnershipLeaseToken = ""
	if s.heartbeatAgentOwnership(ap, time.Minute) {
		t.Fatal("heartbeatAgentOwnership returned true with empty token")
	}
}

func TestOwnershipAcquireSuccessConflictAndReleaseEmptyToken(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "Workspace"}); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	s := newControlPlaneTestSupervisor(st)
	s.NodeID = ""
	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "worker", Role: "task"}}
	if !s.acquireAgentOwnership(ap) {
		t.Fatal("acquireAgentOwnership returned false for fresh lease")
	}
	if ap.OwnershipLeaseID == "" || ap.OwnershipLeaseToken == "" || ap.OwnershipFencingToken == 0 || ap.OwnershipLastHeartbeat.IsZero() {
		t.Fatalf("ownership lease state was not populated: %+v", ap)
	}
	if !s.heartbeatAgentOwnership(ap, time.Minute) {
		t.Fatal("heartbeatAgentOwnership returned false for active lease")
	}

	s2 := newControlPlaneTestSupervisor(st)
	s2.NodeID = "other-node"
	conflict := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "worker", Role: "task"}}
	if s2.acquireAgentOwnership(conflict) {
		t.Fatal("second supervisor acquired an already-owned agent")
	}

	emptyToken := &AgentProcess{
		Entry:            cfgpkg.AgentEntry{Worktree: "no-token", Role: "task"},
		OwnershipLeaseID: "lease-without-token",
	}
	s.releaseAgentOwnership(emptyToken)
	if emptyToken.OwnershipLeaseID != "" || emptyToken.OwnershipFencingToken != 0 || !emptyToken.OwnershipLastHeartbeat.IsZero() {
		t.Fatalf("release with empty token did not clear state: %+v", emptyToken)
	}

	s.releaseAgentOwnership(ap)
}

func TestOwnershipHeartbeatStartStopAndFailureBranches(t *testing.T) {
	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })
	s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})
	s.ControlStore = st
	s.WorkspaceID = "WS"
	s.NodeID = "node-1"
	s.Shutdown = make(chan struct{})

	ap := &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "worker", Role: "task"}}
	stop := s.startOwnershipHeartbeat(ap)
	stop()

	ap.OwnershipLeaseToken = "token"
	stop = s.startOwnershipHeartbeat(ap)
	stop()

	s.ControlStore = &controlPlaneStoreOverrides{
		Store:     st,
		ownership: erroringOwnershipLeaseStore{AgentOwnershipLeaseStore: st.AgentOwnershipLeases(), heartbeatErr: errors.New("heartbeat failed")},
	}
	if s.heartbeatAgentOwnership(ap, time.Minute) {
		t.Fatal("heartbeatAgentOwnership returned true for heartbeat error")
	}
	if ap.LastError == nil || ap.LastError.Message == "" {
		t.Fatalf("LastError = %+v, want heartbeat failure", ap.LastError)
	}
}

func TestReleaseAgentOwnershipLogsReleaseError(t *testing.T) {
	st := memstore.New()
	t.Cleanup(func() { _ = st.Close() })
	s := newTestSupervisorWithConfig(&cfgpkg.DaemonConfig{})
	s.ControlStore = &controlPlaneStoreOverrides{
		Store:     st,
		ownership: erroringOwnershipLeaseStore{AgentOwnershipLeaseStore: st.AgentOwnershipLeases(), releaseErr: errors.New("release failed")},
	}
	s.WorkspaceID = "WS"
	ap := &AgentProcess{
		Entry:               cfgpkg.AgentEntry{Worktree: "worker", Role: "task"},
		OwnershipLeaseID:    "lease-1",
		OwnershipLeaseToken: "token",
	}

	s.releaseAgentOwnership(ap)
	if ap.OwnershipLeaseToken != "" || ap.OwnershipLeaseID != "" || ap.OwnershipFencingToken != 0 {
		t.Fatalf("ownership state was not cleared: %+v", ap)
	}
}

type erroringOwnershipLeaseStore struct {
	store.AgentOwnershipLeaseStore
	acquireErr   error
	heartbeatErr error
	releaseErr   error
}

func (s erroringOwnershipLeaseStore) Acquire(ctx context.Context, in store.AgentOwnershipLeaseAcquire) (*domain.AgentOwnershipLease, error) {
	if s.acquireErr != nil {
		return nil, s.acquireErr
	}
	return s.AgentOwnershipLeaseStore.Acquire(ctx, in)
}

func (s erroringOwnershipLeaseStore) Heartbeat(ctx context.Context, workspaceKey, agentID, token string, ttl time.Duration) (*domain.AgentOwnershipLease, error) {
	if s.heartbeatErr != nil {
		return nil, s.heartbeatErr
	}
	return s.AgentOwnershipLeaseStore.Heartbeat(ctx, workspaceKey, agentID, token, ttl)
}

func (s erroringOwnershipLeaseStore) Release(ctx context.Context, workspaceKey, agentID, token string) (*domain.AgentOwnershipLease, error) {
	if s.releaseErr != nil {
		return nil, s.releaseErr
	}
	return s.AgentOwnershipLeaseStore.Release(ctx, workspaceKey, agentID, token)
}
