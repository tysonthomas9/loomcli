package interaction

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

// SessionAuthorityProof is the one-use credential envelope presented by an
// Interaction child. Validator implementations must send Token only to the
// server-side credential-validation endpoint and must never persist, log, or
// return it. The caller closes Token immediately after authority derivation.
type SessionAuthorityProof struct {
	WorkspaceKey string
	SessionID    string
	AgentID      string
	TerminalID   string
	NodeID       string
	LeaseID      string
	FencingToken int64
	Token        *LeaseToken
}

// SessionAuthorityValidation is the credential-free result of durable
// validation. TerminalID is derived from the immutable AgentSession identity,
// not trusted from the child request.
type SessionAuthorityValidation struct {
	WorkspaceKey string
	SessionID    string
	AgentID      string
	TerminalID   string
	NodeID       string
	LeaseID      string
	FencingToken int64
	ExpiresAt    time.Time
}

// SessionAuthorityValidator verifies the raw session credential against one
// exact live AgentLease generation and returns only server-derived identity.
type SessionAuthorityValidator interface {
	ValidateSessionAuthority(context.Context, SessionAuthorityProof) (*SessionAuthorityValidation, error)
}

// SessionHeartbeat is committed atomically with renewal of the exact
// AgentSession lease generation identified by SessionOwner.
type SessionHeartbeat struct {
	Phase    string
	At       time.Time
	LeaseTTL time.Duration
}

type SessionPatch struct {
	Phase                *string
	MetadataUpserts      map[string]string
	MetadataRemovals     []string
	TranscriptArtifactID *string
	At                   time.Time
}

// SessionFinish is committed atomically with release of the exact
// AgentSession lease generation identified by SessionOwner.
type SessionFinish struct {
	Status               SessionStatus
	FinishedAt           time.Time
	Summary              string
	ErrorClass           string
	ExitCode             *int
	TranscriptArtifactID string
}

type SessionStore interface {
	// Start atomically creates the AgentSession and its first AgentLease
	// generation. A transport must never implement this with independent
	// session-create and lease-create requests.
	Start(context.Context, StartSessionCommand) (SessionStart, error)
	// RecoverStart atomically rotates the credential for one exact starting
	// session after the original response may have been lost.
	RecoverStart(context.Context, RecoverSessionStartCommand) (SessionStart, error)
	Get(context.Context, string, string) (*AgentSession, error)
	List(context.Context, SessionArchiveQuery) ([]*AgentSession, error)
	PatchOwned(context.Context, string, authority.SessionOwner, SessionPatch) (*AgentSession, *SessionLease, error)
	HeartbeatOwned(context.Context, string, authority.SessionOwner, SessionHeartbeat) (*AgentSession, *SessionLease, error)
	FinishOwned(context.Context, string, authority.SessionOwner, SessionFinish) (SessionFinishResult, error)
	ForceInterrupt(context.Context, ForceInterruptCommand) (ForceInterruptResult, error)
	InterruptIfLeaseMissing(context.Context, string, string, time.Time) (*AgentSession, bool, error)
	ListRecoverable(context.Context, string, time.Time) ([]*AgentSession, error)
}

// TranscriptArtifactCreate is the complete server-derived identity for one
// session-owned canonical transcript. The child supplies only Content and
// bounded metadata; Interaction derives every ownership field.
type TranscriptArtifactCreate struct {
	WorkspaceKey string
	ArtifactID   string
	AgentID      string
	SessionID    string
	TaskID       string
	Content      []byte
	Metadata     map[string]string
}

// TranscriptArtifactFailure is the server-derived identity for a capture
// failure that happened before canonical transcript bytes were available.
type TranscriptArtifactFailure struct {
	WorkspaceKey string
	ArtifactID   string
	AgentID      string
	SessionID    string
	TaskID       string
	FailureClass string
}

// TranscriptArtifactStore is the narrow Artifacts capability consumed by
// Interaction. It deliberately exposes no generic artifact CRUD surface.
type TranscriptArtifactStore interface {
	CreateContent(context.Context, authority.SessionAuthority, TranscriptArtifactCreate) (string, error)
	RecordFailure(context.Context, authority.SessionAuthority, TranscriptArtifactFailure) error
}

type TerminalUpdate struct {
	Status               *TerminalStatus
	StreamRef            *string
	TranscriptArtifactID *string
	AttachedClients      *int
	LastSeenAt           *time.Time
	EndedAt              *time.Time
}

type TerminalStore interface {
	Create(context.Context, authority.SessionOwner, OpenTerminalCommand) (*TerminalSession, error)
	Get(context.Context, string, string) (*TerminalSession, error)
	Update(context.Context, authority.SessionOwner, string, string, TerminalUpdate) (*TerminalSession, error)
}

type InboxStore interface {
	Enqueue(context.Context, EnqueueInboxCommand) (*InboxMessage, error)
	ClaimNext(context.Context, authority.SessionOwner, ClaimInboxCommand) (*InboxMessage, error)
	Complete(context.Context, authority.SessionOwner, CompleteInboxCommand) (*InboxMessage, error)
}

// ActivitySource returns the globally merged AgentSession and batch-run
// projection. The persistence aggregates remain distinct behind this port;
// implementations must apply the requested limit only after merging.
type ActivitySource interface {
	ListActivity(context.Context, string, string, int) ([]Activity, error)
}
