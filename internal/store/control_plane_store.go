package store

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

type NodeCreate struct {
	WorkspaceKey    string
	NodeID          string
	OwnerActor      string
	RuntimeProvider domain.RuntimeProvider
	Labels          []string
	Capabilities    []string
	ToolInventory   []string
	Version         string
	Capacity        int
	DrainState      domain.NodeDrainState
	TTL             time.Duration
}

type NodeUpdate struct {
	OwnerActor      *string
	RuntimeProvider *domain.RuntimeProvider
	Labels          *[]string
	Capabilities    *[]string
	ToolInventory   *[]string
	Version         *string
	Capacity        *int
	DrainState      *domain.NodeDrainState
	ExpiresAt       *time.Time
}

type NodeStore interface {
	Create(ctx context.Context, in NodeCreate) (*domain.Node, error)
	Get(ctx context.Context, workspaceKey, nodeID string) (*domain.Node, error)
	List(ctx context.Context, workspaceKey string) ([]*domain.Node, error)
	Heartbeat(ctx context.Context, workspaceKey, nodeID string, ttl time.Duration) (*domain.Node, error)
	Update(ctx context.Context, workspaceKey, nodeID string, patch NodeUpdate) (*domain.Node, error)
}

type AgentSessionCreate struct {
	WorkspaceKey    string
	SessionID       string
	AgentID         string
	NodeID          string
	Kind            domain.AgentSessionKind
	TaskID          string
	TerminalID      string
	ParentSessionID string
	Status          domain.AgentSessionStatus
	Phase           string
	Attempt         int
	Metadata        map[string]string
}

type AgentSessionFilter struct {
	AgentID string
	NodeID  string
	TaskID  string
	Status  domain.AgentSessionStatus
	Limit   int
}

type AgentSessionUpdate struct {
	NodeID        *string
	Status        *domain.AgentSessionStatus
	Phase         *string
	LastHeartbeat *time.Time
	FinishedAt    **time.Time
	Summary       *string
	ErrorClass    *string
	ExitCode      **int
	Metadata      *map[string]string
}

type AgentSessionStore interface {
	Create(ctx context.Context, in AgentSessionCreate) (*domain.AgentSession, error)
	Get(ctx context.Context, workspaceKey, sessionID string) (*domain.AgentSession, error)
	List(ctx context.Context, workspaceKey string, filter AgentSessionFilter) ([]*domain.AgentSession, error)
	Heartbeat(ctx context.Context, workspaceKey, sessionID string) (*domain.AgentSession, error)
	Update(ctx context.Context, workspaceKey, sessionID string, patch AgentSessionUpdate) (*domain.AgentSession, error)
}
