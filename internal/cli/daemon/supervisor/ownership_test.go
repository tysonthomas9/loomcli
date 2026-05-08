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
