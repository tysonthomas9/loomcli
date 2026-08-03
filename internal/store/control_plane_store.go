package store

import (
	"context"
	"errors"
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
	// Kind narrows the query to one session kind (orchestration, task,
	// terminal, maintenance, ad_hoc). The data model has always carried
	// AgentSession.Kind, but the filter interface didn't expose it, so
	// callers couldn't ask "which orchestration session spawned this
	// worker?" without listing every session and filtering client-side.
	// Required by the migration off Agent.OrchestratorSessionID - readers
	// look up the parent lead via {Kind=orchestration, TerminalID=<id>} or
	// {Kind=task, ParentSessionID=<id>} joins.
	Kind domain.AgentSessionKind
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

// ArtifactContentReader is implemented by artifact stores that can read back
// uploaded content bytes. It is optional so older/control-plane stores can
// still expose metadata-only artifact APIs while callers retain URI fallbacks.
type ArtifactContentReader interface {
	ReadContent(ctx context.Context, workspaceKey, artifactID string) ([]byte, error)
}

// ErrArtifactContentUnavailable identifies a temporary failure of the managed
// artifact content plane. Services should preserve it as an unavailable
// response rather than collapsing a retryable FleetDB/content-store outage into
// an internal error.
var ErrArtifactContentUnavailable = errors.New("artifact content temporarily unavailable")

// ErrControlPlaneUnavailable identifies a retryable failure to reach or serve
// the durable control plane. Infrastructure adapters wrap this sentinel while
// preserving their concrete transport error so service layers can return 503
// without importing a FleetDB implementation or exposing transport details.
//
// Deprecated: use domain.ErrUnavailable at capability and transport
// boundaries. This alias preserves errors.Is compatibility for legacy Store
// consumers without making upper layers import persistence contracts.
var ErrControlPlaneUnavailable = domain.ErrUnavailable

// ErrControlPlaneRateLimited is the narrower retryable admission failure.
// Services preserve it as 429 so clients can apply bounded backoff rather than
// presenting a durable transcript or session failure.
//
// Deprecated: use domain.ErrRateLimited at capability and transport
// boundaries. This alias preserves errors.Is compatibility for legacy Store
// consumers without making upper layers import persistence contracts.
var ErrControlPlaneRateLimited = domain.ErrRateLimited

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

// AgentOwnershipLeaseProof identifies one exact ownership generation. It is
// request-only: LeaseToken is a bearer secret and must never be persisted or
// returned in a public projection.
type AgentOwnershipLeaseProof struct {
	WorkspaceKey    string
	AgentID         string
	LeaseID         string
	LeaseToken      string
	OwnerID         string
	RuntimeProvider domain.RuntimeProvider
	NodeID          string
	FencingToken    int64
}

// AgentOwnershipLeaseOwnedStore validates an exact ownership generation for
// the Agents desired-state runtime. Implementations validate the complete proof
// atomically; callers may use AgentOwnershipLeaseStore only when no proof is
// available yet.
type AgentOwnershipLeaseOwnedStore interface {
	HeartbeatOwned(ctx context.Context, proof AgentOwnershipLeaseProof, ttl time.Duration) (*domain.AgentOwnershipLease, error)
	ReleaseOwned(ctx context.Context, proof AgentOwnershipLeaseProof) (*domain.AgentOwnershipLease, error)
}

type AgentOwnershipLeaseStore interface {
	Acquire(ctx context.Context, in AgentOwnershipLeaseAcquire) (*domain.AgentOwnershipLease, error)
	Get(ctx context.Context, workspaceKey, agentID string) (*domain.AgentOwnershipLease, error)
	List(ctx context.Context, workspaceKey string, filter AgentOwnershipLeaseFilter) ([]*domain.AgentOwnershipLease, error)
	Heartbeat(ctx context.Context, workspaceKey, agentID, token string, ttl time.Duration) (*domain.AgentOwnershipLease, error)
	Release(ctx context.Context, workspaceKey, agentID, token string) (*domain.AgentOwnershipLease, error)
}

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
	Status        domain.AgentInboxMessageStatus
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
	Create(ctx context.Context, in AgentInboxMessageCreate) (*domain.AgentInboxMessage, error)
	Get(ctx context.Context, workspaceKey, inboxMessageID string) (*domain.AgentInboxMessage, error)
	List(ctx context.Context, workspaceKey string, filter AgentInboxMessageFilter) ([]*domain.AgentInboxMessage, error)
	ClaimNext(ctx context.Context, in AgentInboxMessageClaim) (*domain.AgentInboxMessage, error)
	Complete(ctx context.Context, workspaceKey, inboxMessageID string, update AgentInboxMessageComplete) (*domain.AgentInboxMessage, error)
}

// WorkerStore renews and removes fleet-db worker registrations. A worker's
// registration is created server-side as a side-effect of claiming an issue
// (keyed by the claim actor) and carries a lease TTL; the client must renew it
// while the agent process is alive, and should deregister on graceful exit.
// Methods return only an error — callers do not consume a worker object.
type WorkerStore interface {
	// Heartbeat renews the worker's registration lease. Best-effort: a worker
	// whose lease already lapsed is reported via error, not resurrected.
	Heartbeat(ctx context.Context, workspaceKey, workerID string) error
	// Deregister removes the worker registration and releases any issue lock it
	// still holds. Idempotent.
	Deregister(ctx context.Context, workspaceKey, workerID string) error
}
