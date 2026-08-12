package memstore

import (
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestAgentOwnershipLeaseAcquireSameOwnerRefreshesAndAdvancesFence(t *testing.T) {
	leases := New().AgentOwnershipLeases()
	ctx := t.Context()

	first, err := leases.Acquire(ctx, store.AgentOwnershipLeaseAcquire{
		WorkspaceKey: "WS",
		AgentID:      "agent-1",
		LeaseID:      "lease-1",
		OwnerID:      "owner-1",
		NodeID:       "node-1",
		TTL:          time.Minute,
	})
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	refreshed, err := leases.Acquire(ctx, store.AgentOwnershipLeaseAcquire{
		WorkspaceKey:    "WS",
		AgentID:         "agent-1",
		LeaseID:         "lease-2",
		OwnerID:         "owner-1",
		RuntimeProvider: domain.RuntimeProviderE2B,
		NodeID:          "node-2",
		TTL:             2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("same-owner reacquire: %v", err)
	}
	if refreshed.Token != first.Token {
		t.Fatalf("same-owner token = %q, want preserved token %q", refreshed.Token, first.Token)
	}
	if refreshed.FencingToken <= first.FencingToken {
		t.Fatalf("same-owner fencing token = %d, want > %d", refreshed.FencingToken, first.FencingToken)
	}
	if refreshed.LeaseID != "lease-2" || refreshed.NodeID != "node-2" || refreshed.RuntimeProvider != domain.RuntimeProviderE2B {
		t.Fatalf("same-owner refreshed lease = %+v, want replacement acquire fields", refreshed)
	}
	if !refreshed.ExpiresAt.After(first.ExpiresAt) {
		t.Fatalf("same-owner expiry = %s, want after original expiry %s", refreshed.ExpiresAt, first.ExpiresAt)
	}
	if _, err := leases.Heartbeat(ctx, "WS", "agent-1", first.Token, time.Minute); err != nil {
		t.Fatalf("heartbeat with preserved token: %v", err)
	}
}

func TestAgentOwnershipLeaseAcquireDifferentOwnerConflictsWithoutMutation(t *testing.T) {
	leases := New().AgentOwnershipLeases()
	ctx := t.Context()

	first, err := leases.Acquire(ctx, store.AgentOwnershipLeaseAcquire{
		WorkspaceKey: "WS",
		AgentID:      "agent-1",
		LeaseID:      "lease-1",
		OwnerID:      "owner-1",
		NodeID:       "node-1",
		TTL:          time.Minute,
	})
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}

	_, err = leases.Acquire(ctx, store.AgentOwnershipLeaseAcquire{
		WorkspaceKey: "WS",
		AgentID:      "agent-1",
		LeaseID:      "lease-2",
		OwnerID:      "owner-2",
		NodeID:       "node-2",
		TTL:          2 * time.Minute,
	})
	if !errors.Is(err, domain.ErrAlreadyClaimed) {
		t.Fatalf("different-owner acquire error = %v, want ErrAlreadyClaimed", err)
	}

	stored, err := leases.Get(ctx, "WS", "agent-1")
	if err != nil {
		t.Fatalf("get after conflicting acquire: %v", err)
	}
	if stored.OwnerID != first.OwnerID || stored.LeaseID != first.LeaseID || stored.Token != first.Token || stored.FencingToken != first.FencingToken {
		t.Fatalf("lease mutated by conflicting acquire: got %+v, want %+v", stored, first)
	}
}
