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
// live lease may be re-acquired by the same logical OwnerID (token preserved,
// fence advanced), while a different owner receives ErrAlreadyClaimed.
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
	token := "restart-token"
	fence := int64(1)
	createdAt := now
	if s.lease != nil {
		token = s.lease.Token
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
		Token:           token,
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
	if replacementAgent.OwnershipFencingToken != 2 || replacementAgent.OwnershipLeaseToken != "restart-token" {
		t.Fatalf("replacement lease = token %q fence %d, want preserved token and fence 2",
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

type unsupportedAgentOwnershipLeaseStore struct {
	store.AgentOwnershipLeaseStore
}

func (s unsupportedAgentOwnershipLeaseStore) Acquire(context.Context, store.AgentOwnershipLeaseAcquire) (*domain.AgentOwnershipLease, error) {
	return nil, fmt.Errorf("agent ownership leases unsupported: %w", domain.ErrNotFound)
}
