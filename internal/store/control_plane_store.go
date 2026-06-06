package store

import (
	"context"
	"io"
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
	TaskID        *string
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

type TerminalSessionCreate struct {
	WorkspaceKey    string
	TerminalID      string
	AgentID         string
	SessionID       string
	NodeID          string
	TaskID          string
	Title           string
	Kind            string
	Status          domain.TerminalSessionStatus
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
	Status    domain.TerminalSessionStatus
	Limit     int
}

type TerminalSessionUpdate struct {
	AgentID         *string
	SessionID       *string
	NodeID          *string
	TaskID          *string
	Title           *string
	Kind            *string
	Status          *domain.TerminalSessionStatus
	PTYProvider     *string
	StreamRef       *string
	TranscriptRef   *string
	AttachedClients *int
	LastSeenAt      *time.Time
	EndedAt         **time.Time
	Metadata        *map[string]string
}

type TerminalSessionStore interface {
	Create(ctx context.Context, in TerminalSessionCreate) (*domain.TerminalSession, error)
	Get(ctx context.Context, workspaceKey, terminalID string) (*domain.TerminalSession, error)
	List(ctx context.Context, workspaceKey string, filter TerminalSessionFilter) ([]*domain.TerminalSession, error)
	Update(ctx context.Context, workspaceKey, terminalID string, patch TerminalSessionUpdate) (*domain.TerminalSession, error)
}

type ArtifactCreate struct {
	WorkspaceKey    string
	ArtifactID      string
	AgentID         string
	SessionID       string
	TerminalID      string
	TaskID          string
	OwnerType       string
	OwnerID         string
	Type            string
	URI             string
	Summary         string
	MIMEType        string
	SizeBytes       int64
	Checksum        string
	ContentHash     string
	Visibility      string
	RedactionStatus string
	DurableStatus   string
	Metadata        map[string]string
}

type ArtifactFilter struct {
	AgentID    string
	SessionID  string
	TerminalID string
	TaskID     string
	OwnerType  string
	OwnerID    string
	Type       string
	Status     string
	Limit      int
}

type ArtifactUpdate struct {
	AgentID         *string
	SessionID       *string
	TerminalID      *string
	TaskID          *string
	OwnerType       *string
	OwnerID         *string
	Type            *string
	URI             *string
	Summary         *string
	MIMEType        *string
	SizeBytes       *int64
	Checksum        *string
	ContentHash     *string
	Visibility      *string
	RedactionStatus *string
	DurableStatus   *string
	Metadata        *map[string]string
	FinalizedAt     *time.Time
}

type ArtifactFinalize struct {
	URI             *string
	Summary         *string
	MIMEType        *string
	SizeBytes       *int64
	Checksum        *string
	ContentHash     *string
	Visibility      *string
	RedactionStatus *string
	Metadata        *map[string]string
}

type ArtifactContentUpload struct {
	Body     io.Reader
	MIMEType string
}

type ArtifactStore interface {
	Create(ctx context.Context, in ArtifactCreate) (*domain.Artifact, error)
	Get(ctx context.Context, workspaceKey, artifactID string) (*domain.Artifact, error)
	List(ctx context.Context, workspaceKey string, filter ArtifactFilter) ([]*domain.Artifact, error)
	UploadContent(ctx context.Context, workspaceKey, artifactID string, upload ArtifactContentUpload) (*domain.Artifact, error)
	Finalize(ctx context.Context, workspaceKey, artifactID string, finalize ArtifactFinalize) (*domain.Artifact, error)
	Update(ctx context.Context, workspaceKey, artifactID string, patch ArtifactUpdate) (*domain.Artifact, error)
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
	Status    domain.AgentLeaseStatus
	Limit     int
}

type AgentLeaseStore interface {
	Create(ctx context.Context, in AgentLeaseCreate) (*domain.AgentLease, error)
	Get(ctx context.Context, workspaceKey, leaseID string) (*domain.AgentLease, error)
	List(ctx context.Context, workspaceKey string, filter AgentLeaseFilter) ([]*domain.AgentLease, error)
	Heartbeat(ctx context.Context, workspaceKey, leaseID, token string, ttl time.Duration) (*domain.AgentLease, error)
	Release(ctx context.Context, workspaceKey, leaseID, token string) (*domain.AgentLease, error)
}

type AgentOwnershipLeaseAcquire struct {
	WorkspaceKey    string
	AgentID         string
	LeaseID         string
	OwnerID         string
	RuntimeProvider domain.RuntimeProvider
	NodeID          string
	TTL             time.Duration
}

type AgentOwnershipLeaseFilter struct {
	OwnerID         string
	NodeID          string
	RuntimeProvider domain.RuntimeProvider
	Status          domain.AgentLeaseStatus
	Limit           int
}

type AgentOwnershipLeaseStore interface {
	Acquire(ctx context.Context, in AgentOwnershipLeaseAcquire) (*domain.AgentOwnershipLease, error)
	Get(ctx context.Context, workspaceKey, agentID string) (*domain.AgentOwnershipLease, error)
	List(ctx context.Context, workspaceKey string, filter AgentOwnershipLeaseFilter) ([]*domain.AgentOwnershipLease, error)
	Heartbeat(ctx context.Context, workspaceKey, agentID, token string, ttl time.Duration) (*domain.AgentOwnershipLease, error)
	Release(ctx context.Context, workspaceKey, agentID, token string) (*domain.AgentOwnershipLease, error)
}

type AgentCommandCreate struct {
	WorkspaceKey  string
	CommandID     string
	TargetAgentID string
	TargetNodeID  string
	SessionID     string
	Type          string
	Payload       map[string]string
}

type AgentCommandFilter struct {
	TargetAgentID string
	TargetNodeID  string
	Status        domain.AgentCommandStatus
	AfterCursor   int64
	Limit         int
}

type AgentCommandComplete struct {
	Status     domain.AgentCommandStatus `json:"status"`
	Result     string                    `json:"result,omitempty"`
	ErrorClass string                    `json:"error_class,omitempty"`
}

type AgentCommandStore interface {
	Create(ctx context.Context, in AgentCommandCreate) (*domain.AgentCommand, error)
	Get(ctx context.Context, workspaceKey, commandID string) (*domain.AgentCommand, error)
	List(ctx context.Context, workspaceKey string, filter AgentCommandFilter) ([]*domain.AgentCommand, error)
	Ack(ctx context.Context, workspaceKey, commandID string) (*domain.AgentCommand, error)
	Complete(ctx context.Context, workspaceKey, commandID string, update AgentCommandComplete) (*domain.AgentCommand, error)
}
