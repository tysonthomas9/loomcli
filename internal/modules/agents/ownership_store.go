package agents

import (
	"context"
	"time"
)

type AgentOwnershipLeaseAcquire struct {
	WorkspaceKey    string
	AgentID         string
	LeaseID         string
	OwnerID         string
	RuntimeProvider RuntimeProvider
	NodeID          string
	TTL             time.Duration
}

type AgentOwnershipLeaseFilter struct {
	OwnerID         string
	NodeID          string
	RuntimeProvider RuntimeProvider
	Status          OwnershipStatus
	Limit           int
}

// AgentOwnershipLeaseProof identifies one exact ownership generation. It is
// request-only: LeaseToken is a bearer secret and must never be persisted or
// returned in a public projection.
type AgentOwnershipLeaseProof struct {
	WorkspaceKey    string
	AgentID         string
	LeaseID         string
	LeaseToken      string
	OwnerID         string
	RuntimeProvider RuntimeProvider
	NodeID          string
	FencingToken    int64
}

// AgentOwnershipLeaseOwnedStore validates an exact ownership generation for
// the Agents desired-state runtime. Implementations validate the complete proof
// atomically; callers may use AgentOwnershipLeaseStore only when no proof is
// available yet.
type AgentOwnershipLeaseOwnedStore interface {
	HeartbeatOwned(ctx context.Context, proof AgentOwnershipLeaseProof, ttl time.Duration) (*OwnershipRecord, error)
	ReleaseOwned(ctx context.Context, proof AgentOwnershipLeaseProof) (*OwnershipRecord, error)
}

type AgentOwnershipLeaseStore interface {
	Acquire(ctx context.Context, in AgentOwnershipLeaseAcquire) (*OwnershipRecord, error)
	Get(ctx context.Context, workspaceKey, agentID string) (*OwnershipRecord, error)
	List(ctx context.Context, workspaceKey string, filter AgentOwnershipLeaseFilter) ([]*OwnershipRecord, error)
	Heartbeat(ctx context.Context, workspaceKey, agentID, token string, ttl time.Duration) (*OwnershipRecord, error)
	Release(ctx context.Context, workspaceKey, agentID, token string) (*OwnershipRecord, error)
}
