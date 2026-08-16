package interaction

import (
	"context"
	"time"
)

type AgentInboxMessageCreate struct {
	WorkspaceKey      string
	InboxMessageID    string
	TargetAgentID     string
	SessionID         string
	Body              string
	SourceKind        string
	SourceRef         string
	DriverRunID       string
	TaskRunID         string
	TriggerEventID    string
	TriggerDeliveryID string
	DedupeKey         string
}

type AgentInboxMessageFilter struct {
	TargetAgentID string
	SessionID     string
	Status        InboxRecordStatus
	SourceKind    string
	SourceRef     string
	DriverRunID   string
	TaskRunID     string
	AfterCursor   int64
	Limit         int
}

type AgentInboxMessageClaim struct {
	WorkspaceKey  string
	TargetAgentID string
	SessionID     string
	ClaimedBy     string
	LeaseTTL      time.Duration
}

type AgentInboxMessageComplete struct {
	Outcome           string `json:"outcome"`
	DeliveredThreadID string `json:"delivered_thread_id,omitempty"`
	ErrorClass        string `json:"error_class,omitempty"`
	Error             string `json:"error,omitempty"`
}

type AgentInboxMessageStore interface {
	Create(ctx context.Context, in AgentInboxMessageCreate) (*InboxRecord, error)
	Get(ctx context.Context, workspaceKey, inboxMessageID string) (*InboxRecord, error)
	List(ctx context.Context, workspaceKey string, filter AgentInboxMessageFilter) ([]*InboxRecord, error)
	ClaimNext(ctx context.Context, in AgentInboxMessageClaim) (*InboxRecord, error)
	Complete(ctx context.Context, workspaceKey, inboxMessageID string, update AgentInboxMessageComplete) (*InboxRecord, error)
}

// WorkerStore renews and removes fleet-db worker registrations. A worker's
// registration is created server-side as a side-effect of claiming an issue
// (keyed by the claim actor) and carries a lease TTL; the client must renew it
// while the agent process is alive, and should deregister on graceful exit.
// Methods return only an error — callers do not consume a worker object.
