package interaction

import (
	"context"
	"time"
)

type AgentSessionCreate struct {
	WorkspaceKey    string
	SessionID       string
	AgentID         string
	NodeID          string
	Kind            SessionRecordKind
	TaskID          string
	TerminalID      string
	ParentSessionID string
	Status          SessionRecordStatus
	Phase           string
	Attempt         int
	Metadata        map[string]string
}

type AgentSessionFilter struct {
	AgentID string
	NodeID  string
	TaskID  string
	Status  SessionRecordStatus
	// Kind narrows the query to one session kind (orchestration, task,
	// terminal, maintenance, ad_hoc). The data model has always carried
	// AgentSession.Kind, but the filter interface didn't expose it, so
	// callers couldn't ask "which orchestration session spawned this
	// worker?" without listing every session and filtering client-side.
	// Required by the migration off Agent.OrchestratorSessionID - readers
	// look up the parent lead via {Kind=orchestration, TerminalID=<id>} or
	// {Kind=task, ParentSessionID=<id>} joins.
	Kind SessionRecordKind
	// ParentSessionID restricts results to sessions whose ParentSessionID
	// field equals this value (typically the lead's orchestration session).
	// Companion to Kind for the same migration: "list task sessions that
	// were spawned by orchestration session X".
	ParentSessionID string
	Limit           int
}

type AgentSessionUpdate struct {
	NodeID        *string
	TaskID        *string
	Status        *SessionRecordStatus
	Phase         *string
	LastHeartbeat *time.Time
	FinishedAt    **time.Time
	Summary       *string
	ErrorClass    *string
	ExitCode      **int
	Metadata      *map[string]string
}

type AgentSessionStore interface {
	Create(ctx context.Context, in AgentSessionCreate) (*SessionRecord, error)
	Get(ctx context.Context, workspaceKey, sessionID string) (*SessionRecord, error)
	List(ctx context.Context, workspaceKey string, filter AgentSessionFilter) ([]*SessionRecord, error)
	Heartbeat(ctx context.Context, workspaceKey, sessionID string) (*SessionRecord, error)
	Update(ctx context.Context, workspaceKey, sessionID string, patch AgentSessionUpdate) (*SessionRecord, error)
}

type TerminalSessionCreate struct {
	WorkspaceKey    string
	TerminalID      string
	AgentID         string
	SessionID       string
	NodeID          string
	TaskID          string
	Title           string
	Kind            string
	Status          TerminalRecordStatus
	PTYProvider     string
	StreamRef       string
	TranscriptRef   string
	AttachedClients int
	Metadata        map[string]string
}

type TerminalSessionFilter struct {
	AgentID   string
	SessionID string
	NodeID    string
	TaskID    string
	Status    TerminalRecordStatus
	Limit     int
}

type TerminalSessionUpdate struct {
	AgentID         *string
	SessionID       *string
	NodeID          *string
	TaskID          *string
	Title           *string
	Kind            *string
	Status          *TerminalRecordStatus
	PTYProvider     *string
	StreamRef       *string
	TranscriptRef   *string
	AttachedClients *int
	LastSeenAt      *time.Time
	EndedAt         **time.Time
	Metadata        *map[string]string
}

type TerminalSessionStore interface {
	Create(ctx context.Context, in TerminalSessionCreate) (*TerminalRecord, error)
	Get(ctx context.Context, workspaceKey, terminalID string) (*TerminalRecord, error)
	List(ctx context.Context, workspaceKey string, filter TerminalSessionFilter) ([]*TerminalRecord, error)
	Update(ctx context.Context, workspaceKey, terminalID string, patch TerminalSessionUpdate) (*TerminalRecord, error)
}

type AgentLeaseCreate struct {
	WorkspaceKey string
	SessionID    string
	LeaseID      string
	AgentID      string
	NodeID       string
	TTL          time.Duration
}

type AgentLeaseFilter struct {
	SessionID string
	AgentID   string
	NodeID    string
	Status    LeaseRecordStatus
	Limit     int
}

type AgentLeaseStore interface {
	Create(ctx context.Context, in AgentLeaseCreate) (*LeaseRecord, error)
	Get(ctx context.Context, workspaceKey, leaseID string) (*LeaseRecord, error)
	List(ctx context.Context, workspaceKey string, filter AgentLeaseFilter) ([]*LeaseRecord, error)
	Heartbeat(ctx context.Context, workspaceKey, leaseID, token string, ttl time.Duration) (*LeaseRecord, error)
	Release(ctx context.Context, workspaceKey, leaseID, token string) (*LeaseRecord, error)
}
