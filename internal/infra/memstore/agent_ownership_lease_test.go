package memstore

import (
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// The fake mirrors fleet-db's compare-and-steal: a live lease is broken
// only by a takeover that still names its current owner.
func TestAgentOwnershipLeaseAcquire_Takeover(t *testing.T) {
	s := New()
	leases := s.AgentOwnershipLeases()
	base := store.AgentOwnershipLeaseAcquire{
		WorkspaceKey: "WS", AgentID: "agent-1", OwnerID: "owner-dead", TTL: time.Minute,
	}
	if _, err := leases.Acquire(t.Context(), base); err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	contend := base
	contend.OwnerID = "owner-new"
	if _, err := leases.Acquire(t.Context(), contend); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("contended acquire err = %v, want ErrAlreadyExists", err)
	}

	stale := contend
	stale.TakeoverFromOwnerID = "owner-someone-else"
	if _, err := leases.Acquire(t.Context(), stale); !errors.Is(err, domain.ErrAlreadyExists) {
		t.Fatalf("stale takeover err = %v, want ErrAlreadyExists", err)
	}

	steal := contend
	steal.TakeoverFromOwnerID = "owner-dead"
	stolen, err := leases.Acquire(t.Context(), steal)
	if err != nil {
		t.Fatalf("takeover acquire: %v", err)
	}
	if stolen.OwnerID != "owner-new" {
		t.Fatalf("owner after takeover = %q, want owner-new", stolen.OwnerID)
	}
}
