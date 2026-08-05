package supervisor

import (
	"context"
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

	if got := s.acquireAgentOwnership(ap); got != ownershipAcquired {
		t.Fatalf("acquireAgentOwnership = %v, want ownershipAcquired for unsupported ownership lease backend", got)
	}

	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.OwnershipLeaseID != "" || ap.OwnershipLeaseToken != "" || ap.OwnershipFencingToken != 0 || !ap.OwnershipLastHeartbeat.IsZero() {
		t.Fatalf("ownership state was not cleared: lease=%q token=%q fencing=%d heartbeat=%s", ap.OwnershipLeaseID, ap.OwnershipLeaseToken, ap.OwnershipFencingToken, ap.OwnershipLastHeartbeat)
	}
}

// restartOwnershipLeaseStore models fleet-db's guarded acquire contract: a
// live lease may be re-acquired by the same logical OwnerID (token and fence
// rotated), while a different owner receives ErrAlreadyClaimed.
type restartOwnershipLeaseStore struct {
	store.AgentOwnershipLeaseStore
	lease  *domain.AgentOwnershipLease
	inputs []store.AgentOwnershipLeaseAcquire
}

func (s *restartOwnershipLeaseStore) Acquire(_ context.Context, in store.AgentOwnershipLeaseAcquire) (*domain.AgentOwnershipLease, error) {
	s.inputs = append(s.inputs, in)
	if s.lease != nil && s.lease.Status == domain.AgentLeaseActive && s.lease.ExpiresAt.After(time.Now()) && s.lease.OwnerID != in.OwnerID {
		return nil, fmt.Errorf("ownership held by %s: %w", s.lease.OwnerID, domain.ErrAlreadyClaimed)
	}
	now := time.Now().UTC()
	fence := int64(1)
	createdAt := now
	if s.lease != nil {
		fence = s.lease.FencingToken + 1
		createdAt = s.lease.CreatedAt
	}
	s.lease = &domain.AgentOwnershipLease{
		WorkspaceKey:    in.WorkspaceKey,
		AgentID:         in.AgentID,
		LeaseID:         "restart-lease",
		OwnerID:         in.OwnerID,
		RuntimeProvider: in.RuntimeProvider,
		NodeID:          in.NodeID,
		Token:           fmt.Sprintf("restart-token-%d", fence),
		FencingToken:    fence,
		Status:          domain.AgentLeaseActive,
		ExpiresAt:       now.Add(in.TTL),
		LastHeartbeat:   now,
		CreatedAt:       createdAt,
		UpdatedAt:       now,
	}
	out := *s.lease
	return &out, nil
}

// TestAcquireAgentOwnershipReacquiresAfterDaemonRestart is the container/
// daemon restart regression: NodeID changes with hostname/PID, but the durable
// workspace-scoped OwnerID does not, so fleet-db permits immediate guarded
// re-acquire instead of blocking for the old 30-minute task-session TTL.
func TestAcquireAgentOwnershipReacquiresAfterDaemonRestart(t *testing.T) {
	projectDir := t.TempDir()
	fake := &restartOwnershipLeaseStore{}
	base := memstore.New()
	controlStore := &controlPlaneStoreOverrides{Store: base, ownership: fake}
	newSupervisor := func(nodeID, dir string) *Supervisor {
		return &Supervisor{
			ProjectDir:   dir,
			WorkspaceID:  "WS",
			NodeID:       nodeID,
			ControlStore: controlStore,
		}
	}
	newAgent := func() *AgentProcess {
		return &AgentProcess{Entry: cfgpkg.AgentEntry{Worktree: "worker-1", Role: "task"}}
	}

	first := newSupervisor("loom-supervisor-old-host-101", projectDir)
	if got := first.acquireAgentOwnership(newAgent()); got != ownershipAcquired {
		t.Fatalf("first acquire = %v, want acquired", got)
	}
	replacementAgent := newAgent()
	replacement := newSupervisor("loom-supervisor-new-host-202", projectDir)
	if got := replacement.acquireAgentOwnership(replacementAgent); got != ownershipAcquired {
		t.Fatalf("replacement acquire = %v, want acquired as the same durable owner", got)
	}

	if len(fake.inputs) != 2 {
		t.Fatalf("acquire calls = %d, want 2", len(fake.inputs))
	}
	firstIn, replacementIn := fake.inputs[0], fake.inputs[1]
	if firstIn.OwnerID == "" || replacementIn.OwnerID != firstIn.OwnerID {
		t.Fatalf("owner ids = %q, %q; want one durable non-empty identity", firstIn.OwnerID, replacementIn.OwnerID)
	}
	if firstIn.NodeID == replacementIn.NodeID {
		t.Fatalf("node ids = %q, %q; restart fixture must use distinct process identities", firstIn.NodeID, replacementIn.NodeID)
	}
	if firstIn.TTL != defaultNodeTTL || replacementIn.TTL != defaultNodeTTL {
		t.Fatalf("ownership TTLs = %s, %s; want node TTL %s", firstIn.TTL, replacementIn.TTL, defaultNodeTTL)
	}
	if replacementAgent.OwnershipFencingToken != 2 || replacementAgent.OwnershipLeaseToken != "restart-token-2" {
		t.Fatalf("replacement lease = token %q fence %d, want rotated token and fence 2",
			replacementAgent.OwnershipLeaseToken, replacementAgent.OwnershipFencingToken)
	}

	// A different runtime directory must not inherit this owner's authority.
	otherRuntime := newSupervisor("loom-supervisor-other-host-303", t.TempDir())
	if got := otherRuntime.acquireAgentOwnership(newAgent()); got != ownershipHeldByOther {
		t.Fatalf("different-runtime acquire = %v, want held-by-other", got)
	}
	if len(fake.inputs) != 3 || fake.inputs[2].OwnerID == firstIn.OwnerID {
		t.Fatalf("different runtime owner = %q, want identity distinct from %q", fake.inputs[2].OwnerID, firstIn.OwnerID)
	}
}

func TestAgentOwnershipOperationsSerializeAcquireAndRelease(t *testing.T) {
	base := memstore.New()
	ownership := &blockingAcquireOwnershipLeaseStore{
		AgentOwnershipLeaseStore: base.AgentOwnershipLeases(),
		acquireStarted:           make(chan struct{}),
		allowAcquire:             make(chan struct{}),
		releaseToken:             make(chan string, 1),
	}
	control := &controlPlaneStoreOverrides{Store: base, ownership: ownership}
	s := &Supervisor{
		WorkspaceID:  "WS",
		NodeID:       "node-1",
		ControlStore: control,
	}
	ap := &AgentProcess{
		Entry:             cfgpkg.AgentEntry{Worktree: "worker-1", Role: "task"},
		OwnershipAcquired: make(chan struct{}),
	}
	acquired := make(chan ownershipAcquireOutcome, 1)
	go func() {
		acquired <- s.acquireAgentOwnership(ap)
	}()
	<-ownership.acquireStarted

	releaseStarted := make(chan struct{})
	released := make(chan struct{})
	go func() {
		close(releaseStarted)
		s.releaseAgentOwnership(ap)
		close(released)
	}()
	<-releaseStarted
	select {
	case <-released:
		t.Fatal("Release overtook the in-flight Acquire and allowed ownership resurrection")
	case <-time.After(20 * time.Millisecond):
	}

	close(ownership.allowAcquire)
	if outcome := <-acquired; outcome != ownershipAcquired {
		t.Fatalf("Acquire outcome = %v, want acquired", outcome)
	}
	select {
	case <-released:
	case <-time.After(time.Second):
		t.Fatal("Release did not run after Acquire completed")
	}
	select {
	case token := <-ownership.releaseToken:
		if token != "rotated-token" {
			t.Fatalf("Release token = %q, want newly acquired token", token)
		}
	default:
		t.Fatal("serialized Release did not reach ownership store")
	}
	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.OwnershipLeaseID != "" ||
		ap.OwnershipLeaseToken != "" ||
		ap.OwnershipFencingToken != 0 {
		t.Fatalf("ownership state resurrected after Release: lease=%q token=%q fence=%d",
			ap.OwnershipLeaseID,
			ap.OwnershipLeaseToken,
			ap.OwnershipFencingToken,
		)
	}
}

type ownedOwnershipLifecycleStore struct {
	store.AgentOwnershipLeaseStore
	heartbeat store.AgentOwnershipLeaseProof
	release   store.AgentOwnershipLeaseProof
}

func (s *ownedOwnershipLifecycleStore) HeartbeatOwned(
	_ context.Context,
	proof store.AgentOwnershipLeaseProof,
	ttl time.Duration,
) (*domain.AgentOwnershipLease, error) {
	s.heartbeat = proof
	now := time.Now().UTC()
	return &domain.AgentOwnershipLease{
		WorkspaceKey: proof.WorkspaceKey, AgentID: proof.AgentID, LeaseID: proof.LeaseID,
		OwnerID: proof.OwnerID, RuntimeProvider: proof.RuntimeProvider, NodeID: proof.NodeID,
		FencingToken: proof.FencingToken, Status: domain.AgentLeaseActive,
		LastHeartbeat: now, ExpiresAt: now.Add(ttl), CreatedAt: now, UpdatedAt: now,
	}, nil
}

func (s *ownedOwnershipLifecycleStore) ReleaseOwned(
	_ context.Context,
	proof store.AgentOwnershipLeaseProof,
) (*domain.AgentOwnershipLease, error) {
	s.release = proof
	return &domain.AgentOwnershipLease{
		WorkspaceKey: proof.WorkspaceKey, AgentID: proof.AgentID, LeaseID: proof.LeaseID,
		OwnerID: proof.OwnerID, RuntimeProvider: proof.RuntimeProvider, NodeID: proof.NodeID,
		FencingToken: proof.FencingToken, Status: domain.AgentLeaseReleased,
	}, nil
}

func TestAgentOwnershipHeartbeatAndReleasePreferOwnerFencedCommands(t *testing.T) {
	base := memstore.New()
	owned := &ownedOwnershipLifecycleStore{AgentOwnershipLeaseStore: base.AgentOwnershipLeases()}
	s := &Supervisor{
		WorkspaceID:  "WS",
		ControlStore: &controlPlaneStoreOverrides{Store: base, ownership: owned},
	}
	ap := &AgentProcess{
		Entry:                 cfgpkg.AgentEntry{Worktree: "worker-1", Role: "task"},
		OwnershipLeaseID:      "ownership-1",
		OwnershipOwnerID:      "runtime-1",
		OwnershipNodeID:       "node-1",
		OwnershipLeaseToken:   "raw-ownership-token",
		OwnershipFencingToken: 11,
		OwnershipRenewedAt:    time.Now(),
	}

	if err := s.doOwnershipHeartbeat(ap, 45*time.Second); err != nil {
		t.Fatalf("doOwnershipHeartbeat: %v", err)
	}
	s.releaseAgentOwnership(ap)

	want := store.AgentOwnershipLeaseProof{
		WorkspaceKey: "WS", AgentID: "worker-1", LeaseID: "ownership-1",
		LeaseToken: "raw-ownership-token", OwnerID: "runtime-1",
		RuntimeProvider: domain.RuntimeProviderLocal, NodeID: "node-1", FencingToken: 11,
	}
	if owned.heartbeat != want {
		t.Fatalf("heartbeat proof = %+v, want %+v", owned.heartbeat, want)
	}
	if owned.release != want {
		t.Fatalf("release proof = %+v, want %+v", owned.release, want)
	}
}

func TestAcquireAgentOwnershipRejectsNonAdvancingFence(t *testing.T) {
	base := memstore.New()
	ownership := &fixedAcquireOwnershipLeaseStore{
		AgentOwnershipLeaseStore: base.AgentOwnershipLeases(),
		fencingToken:             7,
		token:                    "stale-token",
	}
	s := &Supervisor{
		WorkspaceID: "WS",
		NodeID:      "node-1",
		ControlStore: &controlPlaneStoreOverrides{
			Store:     base,
			ownership: ownership,
		},
	}
	ap := &AgentProcess{
		Entry:                 cfgpkg.AgentEntry{Worktree: "worker-1", Role: "task"},
		OwnershipLeaseID:      "current-lease",
		OwnershipOwnerID:      "node-1",
		OwnershipNodeID:       "node-1",
		OwnershipLeaseToken:   "current-token",
		OwnershipFencingToken: 8,
	}

	if got := s.acquireAgentOwnership(ap); got != ownershipAcquireInconclusive {
		t.Fatalf("acquireAgentOwnership = %v, want inconclusive for a non-advancing fence", got)
	}

	ap.Mu.Lock()
	defer ap.Mu.Unlock()
	if ap.OwnershipLeaseID != "current-lease" ||
		ap.OwnershipLeaseToken != "current-token" ||
		ap.OwnershipFencingToken != 8 {
		t.Fatalf("stale acquire response replaced current authority: lease=%q token=%q fence=%d",
			ap.OwnershipLeaseID,
			ap.OwnershipLeaseToken,
			ap.OwnershipFencingToken,
		)
	}
}

type fixedAcquireOwnershipLeaseStore struct {
	store.AgentOwnershipLeaseStore
	fencingToken int64
	token        string
}

func (s *fixedAcquireOwnershipLeaseStore) Acquire(
	_ context.Context,
	in store.AgentOwnershipLeaseAcquire,
) (*domain.AgentOwnershipLease, error) {
	now := time.Now().UTC()
	return &domain.AgentOwnershipLease{
		WorkspaceKey:    in.WorkspaceKey,
		AgentID:         in.AgentID,
		LeaseID:         "returned-lease",
		OwnerID:         in.OwnerID,
		RuntimeProvider: in.RuntimeProvider,
		NodeID:          in.NodeID,
		Token:           s.token,
		FencingToken:    s.fencingToken,
		Status:          domain.AgentLeaseActive,
		ExpiresAt:       now.Add(time.Minute),
		LastHeartbeat:   now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}, nil
}

type blockingAcquireOwnershipLeaseStore struct {
	store.AgentOwnershipLeaseStore
	acquireStarted chan struct{}
	allowAcquire   chan struct{}
	releaseToken   chan string
}

func (s *blockingAcquireOwnershipLeaseStore) Acquire(
	_ context.Context,
	in store.AgentOwnershipLeaseAcquire,
) (*domain.AgentOwnershipLease, error) {
	close(s.acquireStarted)
	<-s.allowAcquire
	now := time.Now().UTC()
	return &domain.AgentOwnershipLease{
		WorkspaceKey:    in.WorkspaceKey,
		AgentID:         in.AgentID,
		LeaseID:         "rotated-lease",
		OwnerID:         in.OwnerID,
		RuntimeProvider: in.RuntimeProvider,
		NodeID:          in.NodeID,
		Token:           "rotated-token",
		FencingToken:    2,
		Status:          domain.AgentLeaseActive,
		ExpiresAt:       now.Add(time.Minute),
		LastHeartbeat:   now,
		CreatedAt:       now,
		UpdatedAt:       now,
	}, nil
}

func (s *blockingAcquireOwnershipLeaseStore) Release(
	_ context.Context,
	_,
	_ string,
	token string,
) (*domain.AgentOwnershipLease, error) {
	s.releaseToken <- token
	return &domain.AgentOwnershipLease{Status: domain.AgentLeaseReleased}, nil
}

type unsupportedAgentOwnershipLeaseStore struct {
	store.AgentOwnershipLeaseStore
}

func (s unsupportedAgentOwnershipLeaseStore) Acquire(context.Context, store.AgentOwnershipLeaseAcquire) (*domain.AgentOwnershipLease, error) {
	return nil, fmt.Errorf("agent ownership leases unsupported: %w", domain.ErrNotFound)
}
