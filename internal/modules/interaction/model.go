package interaction

import "time"

type SessionKind string

const (
	SessionKindInteractive SessionKind = "interactive"
	SessionKindTask        SessionKind = "task"
	SessionKindReview      SessionKind = "review"
)

type SessionStatus string

const (
	SessionStarting    SessionStatus = "starting"
	SessionRunning     SessionStatus = "running"
	SessionCompleted   SessionStatus = "completed"
	SessionFailed      SessionStatus = "failed"
	SessionCancelled   SessionStatus = "cancelled"
	SessionExpired     SessionStatus = "expired"
	SessionInterrupted SessionStatus = "interrupted"
)

func (status SessionStatus) Terminal() bool {
	switch status {
	case SessionCompleted, SessionFailed, SessionCancelled, SessionExpired, SessionInterrupted:
		return true
	default:
		return false
	}
}

type AgentSession struct {
	WorkspaceKey             string
	SessionID                string
	AgentID                  string
	NodeID                   string
	Kind                     SessionKind
	TaskID                   string
	TerminalID               string
	ParentSessionID          string
	Status                   SessionStatus
	CurrentLeaseID           string
	CurrentLeaseFencingToken int64
	Phase                    string
	Attempt                  int
	Summary                  string
	ErrorClass               string
	ExitCode                 *int
	TranscriptArtifactID     string
	Metadata                 map[string]string
	StartedAt                time.Time
	LastHeartbeat            time.Time
	FinishedAt               *time.Time
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

type TerminalStatus string

const (
	TerminalStarting TerminalStatus = "starting"
	TerminalRunning  TerminalStatus = "running"
	TerminalExited   TerminalStatus = "exited"
	TerminalFailed   TerminalStatus = "failed"
)

type TerminalSession struct {
	WorkspaceKey         string
	TerminalID           string
	AgentID              string
	SessionID            string
	NodeID               string
	TaskID               string
	Title                string
	Kind                 string
	Status               TerminalStatus
	PTYProvider          string
	StreamRef            string
	TranscriptArtifactID string
	AttachedClients      int
	Metadata             map[string]string
	StartedAt            time.Time
	LastSeenAt           time.Time
	EndedAt              *time.Time
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// SessionLease is safe to persist and return from ordinary reads. It contains
// the hash-only lease identity and never the raw bearer credential.
type SessionLease struct {
	WorkspaceKey  string
	LeaseID       string
	SessionID     string
	AgentID       string
	NodeID        string
	TokenHash     string
	FencingToken  int64
	Status        string
	ExpiresAt     time.Time
	LastHeartbeat time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// LeaseToken is the one-time raw credential returned only by session start.
// Callers must Close it after copying the bytes into the child-launch envelope.
type LeaseToken struct {
	value []byte
}

func NewLeaseToken(value []byte) *LeaseToken {
	return &LeaseToken{value: append([]byte(nil), value...)}
}

func (token *LeaseToken) Bytes() []byte {
	if token == nil {
		return nil
	}
	return append([]byte(nil), token.value...)
}

func (token *LeaseToken) Close() {
	if token == nil {
		return
	}
	for index := range token.value {
		token.value[index] = 0
	}
	token.value = nil
}

type SessionStart struct {
	Session *AgentSession
	Lease   *SessionLease
	Token   *LeaseToken `json:"-"`
}

// SessionFinishResult is the atomic result of finishing a session. Interactive
// sessions include the exact terminal that was terminalized in the same
// transaction as the session outcome and lease release.
type SessionFinishResult struct {
	Session  *AgentSession
	Terminal *TerminalSession
	Lease    *SessionLease
}

type InboxStatus string

const (
	InboxQueued    InboxStatus = "queued"
	InboxDelivered InboxStatus = "delivered"
	InboxFailed    InboxStatus = "failed"
)

type InboxMessage struct {
	WorkspaceKey      string
	MessageID         string
	Cursor            int64
	TargetAgentID     string
	SessionID         string
	Body              string
	Status            InboxStatus
	SourceKind        string
	SourceRef         string
	DriverRunID       string
	TaskRunID         string
	TriggerEventID    string
	TriggerDeliveryID string
	DedupeKey         string
	Attempt           int
	ClaimedBy         string
	ClaimExpiresAt    *time.Time
	ErrorClass        string
	DeliveredThreadID string
	DeliveredAt       *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type ActivityKind string

const (
	ActivityBatchRun ActivityKind = "batch_run"
	ActivitySession  ActivityKind = "agent_session"
)

// Activity is a read-only projection. SourceID remains the immutable identity
// of either an Execution batch run or an Interaction AgentSession; persistence
// for the two aggregates is never merged.
type Activity struct {
	WorkspaceKey string
	AgentID      string
	Kind         ActivityKind
	SourceID     string
	TaskID       string
	Status       string
	Summary      string
	StartedAt    time.Time
	FinishedAt   *time.Time
}

func cloneMetadata(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneSession(in *AgentSession) *AgentSession {
	if in == nil {
		return nil
	}
	out := *in
	out.Metadata = cloneMetadata(in.Metadata)
	if in.ExitCode != nil {
		value := *in.ExitCode
		out.ExitCode = &value
	}
	if in.FinishedAt != nil {
		value := *in.FinishedAt
		out.FinishedAt = &value
	}
	return &out
}

func cloneTerminal(in *TerminalSession) *TerminalSession {
	if in == nil {
		return nil
	}
	out := *in
	out.Metadata = cloneMetadata(in.Metadata)
	if in.EndedAt != nil {
		value := *in.EndedAt
		out.EndedAt = &value
	}
	return &out
}

func cloneLease(in *SessionLease) *SessionLease {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneInbox(in *InboxMessage) *InboxMessage {
	if in == nil {
		return nil
	}
	out := *in
	if in.ClaimExpiresAt != nil {
		value := *in.ClaimExpiresAt
		out.ClaimExpiresAt = &value
	}
	if in.DeliveredAt != nil {
		value := *in.DeliveredAt
		out.DeliveredAt = &value
	}
	return &out
}
