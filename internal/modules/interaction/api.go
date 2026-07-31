package interaction

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	ActionStartSession      authority.Action = "interaction.start-session"
	ActionRecoverStart      authority.Action = "interaction.recover-session-start"
	ActionPatchSession      authority.Action = "interaction.patch-session"
	ActionHeartbeatSession  authority.Action = "interaction.heartbeat-session"
	ActionFinishSession     authority.Action = "interaction.finish-session"
	ActionForceInterrupt    authority.Action = "interaction.force-interrupt"
	ActionOpenTerminal      authority.Action = "interaction.open-terminal"
	ActionUpdateTerminal    authority.Action = "interaction.update-terminal"
	ActionEnqueueInbox      authority.Action = "interaction.enqueue-inbox"
	ActionClaimInbox        authority.Action = "interaction.claim-inbox"
	ActionCompleteInbox     authority.Action = "interaction.complete-inbox"
	ActionReadActivity      authority.Action = "interaction.read-activity"
	ActionReconcileSessions authority.Action = "interaction.reconcile-sessions"
)

// SessionAuthorityResolver is the Interaction-owned port for exchanging a
// one-use lease proof for a request-scoped, fenced session authority.
type SessionAuthorityResolver interface {
	ResolveSessionAuthority(
		context.Context,
		authority.Action,
		SessionAuthorityProof,
	) (authority.SessionAuthority, error)
}

// OperationRules is Interaction's complete default-deny command registry.
// Session lifecycle, terminal, and inbox-delivery mutations require a
// server-derived SessionAuthority bound to one live AgentLease generation.
func OperationRules() []authority.OperationRule {
	return []authority.OperationRule{
		authority.OperatorOnly(ActionStartSession),
		authority.Allow(ActionRecoverStart, authority.ClassOperator, authority.ClassSystem),
		authority.Allow(ActionPatchSession, authority.ClassSession),
		authority.Allow(ActionHeartbeatSession, authority.ClassSession),
		authority.Allow(ActionFinishSession, authority.ClassSession),
		authority.Allow(ActionForceInterrupt, authority.ClassSystem),
		authority.Allow(ActionOpenTerminal, authority.ClassSession),
		authority.Allow(ActionUpdateTerminal, authority.ClassSession),
		authority.Allow(ActionEnqueueInbox, authority.ClassOperator, authority.ClassSystem),
		authority.Allow(ActionClaimInbox, authority.ClassSession),
		authority.Allow(ActionCompleteInbox, authority.ClassSession),
		authority.OperatorOnly(ActionReadActivity),
		authority.Allow(ActionReconcileSessions, authority.ClassSystem),
	}
}

type StartSessionCommand struct {
	WorkspaceKey    string
	SessionID       string
	AgentID         string
	NodeID          string
	Kind            SessionKind
	TaskID          string
	TerminalID      string
	ParentSessionID string
	Phase           string
	Attempt         int
	LeaseID         string
	LeaseTTL        time.Duration
	Metadata        map[string]string
}

type RecoverSessionStartCommand struct {
	Original                  StartSessionCommand
	ExpectedLeaseID           string
	ExpectedLeaseFencingToken int64
	ReplacementLeaseID        string
	ReplacementLeaseTTL       time.Duration
}

type HeartbeatSessionCommand struct {
	WorkspaceKey string
	SessionID    string
	Phase        string
	LeaseTTL     time.Duration
}

// PatchSessionCommand is the bounded live-runtime context surface. Identity,
// ownership, lifecycle status, outcome, task placement, and timestamps are
// intentionally absent.
type PatchSessionCommand struct {
	WorkspaceKey         string
	SessionID            string
	Phase                *string
	MetadataUpserts      map[string]string
	MetadataRemovals     []string
	TranscriptArtifactID *string
}

type FinishSessionCommand struct {
	WorkspaceKey         string
	SessionID            string
	Status               SessionStatus
	Summary              string
	ErrorClass           string
	ExitCode             *int
	TranscriptArtifactID string
}

// ForceInterruptCommand contains only the durable identities and
// server-owned terminal placement evidence needed to converge a PTY-backed
// interactive lifecycle after its child can no longer authenticate.
type ForceInterruptCommand struct {
	WorkspaceKey              string
	SessionID                 string
	AgentID                   string
	TerminalID                string
	ExpectedLeaseID           string
	ExpectedLeaseFencingToken int64
	StreamRef                 string
	TerminalTab               string
	Reason                    string
}

type ForceInterruptResult struct {
	Session  *AgentSession
	Terminal *TerminalSession
	Lease    *SessionLease
	Changed  bool
}

type OpenTerminalCommand struct {
	WorkspaceKey string
	TerminalID   string
	SessionID    string
	AgentID      string
	NodeID       string
	TaskID       string
	Title        string
	Kind         string
	PTYProvider  string
	StreamRef    string
	Metadata     map[string]string
}

type UpdateTerminalCommand struct {
	WorkspaceKey         string
	TerminalID           string
	Status               TerminalStatus
	StreamRef            *string
	TranscriptArtifactID *string
	AttachedClients      *int
}

type EnqueueInboxCommand struct {
	WorkspaceKey      string
	MessageID         string
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

type ClaimInboxCommand struct {
	WorkspaceKey string
	AgentID      string
	SessionID    string
	LeaseTTL     time.Duration
}

type CompleteInboxCommand struct {
	WorkspaceKey      string
	MessageID         string
	SessionID         string
	Attempt           int
	Status            InboxStatus
	DeliveredThreadID string
	ErrorClass        string
}

type ActivityQuery struct {
	WorkspaceKey string
	AgentID      string
	Limit        int
}

type API interface {
	StartSession(context.Context, authority.OperatorAuthority, StartSessionCommand) (SessionStart, error)
	RecoverSessionStart(context.Context, authority.OperatorAuthority, RecoverSessionStartCommand) (SessionStart, error)
	PatchSession(context.Context, authority.SessionAuthority, PatchSessionCommand) (*AgentSession, error)
	HeartbeatSession(context.Context, authority.SessionAuthority, HeartbeatSessionCommand) (*AgentSession, error)
	FinishSession(context.Context, authority.SessionAuthority, FinishSessionCommand) (*AgentSession, error)
	ForceInterrupt(context.Context, authority.SystemAuthority, ForceInterruptCommand) (ForceInterruptResult, error)
	OpenTerminal(context.Context, authority.SessionAuthority, OpenTerminalCommand) (*TerminalSession, error)
	UpdateTerminal(context.Context, authority.SessionAuthority, UpdateTerminalCommand) (*TerminalSession, error)
	EnqueueInbox(context.Context, authority.OperatorAuthority, EnqueueInboxCommand) (*InboxMessage, error)
	ClaimInbox(context.Context, authority.SessionAuthority, ClaimInboxCommand) (*InboxMessage, error)
	CompleteInbox(context.Context, authority.SessionAuthority, CompleteInboxCommand) (*InboxMessage, error)
	ListActivity(context.Context, authority.OperatorAuthority, ActivityQuery) ([]Activity, error)
	ReconcileSessions(context.Context, authority.SystemAuthority, string, time.Time) (int, error)
}

type RuntimeStartRecoveryAPI interface {
	RecoverSessionStartAsSystem(
		context.Context,
		authority.SystemAuthority,
		RecoverSessionStartCommand,
	) (SessionStart, error)
}

// RuntimeForceInterruptAPI is the system-authorized side of the force
// interrupt command. UI lifecycle callers receive only ForceInterrupter, never
// the issuer or a reusable SystemAuthority.
type RuntimeForceInterruptAPI interface {
	ForceInterrupt(context.Context, authority.SystemAuthority, ForceInterruptCommand) (ForceInterruptResult, error)
}

type ForceInterrupter interface {
	ForceInterrupt(context.Context, ForceInterruptCommand) (ForceInterruptResult, error)
}

// RuntimeInboxAPI is the system-authorized side of the inbox enqueue command.
// It is separate from API so operator entrypoints remain concretely typed and
// callers cannot substitute a generic authority union.
type RuntimeInboxAPI interface {
	EnqueueInboxAsSystem(
		context.Context,
		authority.SystemAuthority,
		EnqueueInboxCommand,
	) (*InboxMessage, error)
}

// InboxEnqueuer is the authority-free application port supplied only by
// server composition to registered delivery adapters. Callers never receive
// the issuer or a reusable SystemAuthority.
type InboxEnqueuer interface {
	Enqueue(context.Context, EnqueueInboxCommand) (*InboxMessage, error)
}
