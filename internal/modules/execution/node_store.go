package execution

import (
	"context"
	"time"
)

type NodeCreate struct {
	WorkspaceKey    string
	NodeID          string
	OwnerActor      string
	RuntimeProvider RuntimeProvider
	Labels          []string
	Capabilities    []string
	ToolInventory   []string
	Version         string
	Capacity        int
	DrainState      WorkerNodeDrainState
	TTL             time.Duration
}

type NodeUpdate struct {
	OwnerActor      *string
	RuntimeProvider *RuntimeProvider
	Labels          *[]string
	Capabilities    *[]string
	ToolInventory   *[]string
	Version         *string
	Capacity        *int
	DrainState      *WorkerNodeDrainState
	ExpiresAt       *time.Time
}

type NodeStore interface {
	Create(ctx context.Context, in NodeCreate) (*WorkerNode, error)
	Get(ctx context.Context, workspaceKey, nodeID string) (*WorkerNode, error)
	List(ctx context.Context, workspaceKey string) ([]*WorkerNode, error)
	Heartbeat(ctx context.Context, workspaceKey, nodeID string, ttl time.Duration) (*WorkerNode, error)
	Update(ctx context.Context, workspaceKey, nodeID string, patch NodeUpdate) (*WorkerNode, error)
}
