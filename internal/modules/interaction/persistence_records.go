package interaction

import "time"

// SessionRecordKind is the complete durable session vocabulary. SessionKind
// remains the smaller public interaction vocabulary.
type SessionRecordKind string

const (
	SessionRecordTask        SessionRecordKind = "task"
	SessionRecordInteractive SessionRecordKind = "interactive"
	SessionRecordTerminal    SessionRecordKind = "terminal"
	SessionRecordMaintenance SessionRecordKind = "maintenance"
	SessionRecordAdHoc       SessionRecordKind = "ad_hoc"
)

type SessionRecordStatus string

const (
	SessionRecordQueued    SessionRecordStatus = "queued"
	SessionRecordLeased    SessionRecordStatus = "leased"
	SessionRecordStarting  SessionRecordStatus = "starting"
	SessionRecordRunning   SessionRecordStatus = "running"
	SessionRecordIdle      SessionRecordStatus = "idle"
	SessionRecordYielded   SessionRecordStatus = "yielded"
	SessionRecordCompleted SessionRecordStatus = "completed"
	SessionRecordFailed    SessionRecordStatus = "failed"
	SessionRecordCancelled SessionRecordStatus = "cancelled"
	SessionRecordExpired   SessionRecordStatus = "expired"
)

type SessionRecord struct {
	WorkspaceKey             string              `json:"workspace_key"`
	SessionID                string              `json:"session_id"`
	AgentID                  string              `json:"agent_id"`
	NodeID                   string              `json:"node_id,omitempty"`
	Kind                     SessionRecordKind   `json:"kind"`
	TaskID                   string              `json:"task_id,omitempty"`
	TerminalID               string              `json:"terminal_id,omitempty"`
	ParentSessionID          string              `json:"parent_session_id,omitempty"`
	Status                   SessionRecordStatus `json:"status"`
	CurrentLeaseID           string              `json:"current_lease_id,omitempty"`
	CurrentLeaseFencingToken int64               `json:"current_lease_fencing_token,omitempty"`
	Phase                    string              `json:"phase,omitempty"`
	Attempt                  int                 `json:"attempt,omitempty"`
	StartedAt                time.Time           `json:"started_at,omitempty"`
	LastHeartbeat            time.Time           `json:"last_heartbeat,omitempty"`
	FinishedAt               *time.Time          `json:"finished_at,omitempty"`
	Summary                  string              `json:"summary,omitempty"`
	ErrorClass               string              `json:"error_class,omitempty"`
	ExitCode                 *int                `json:"exit_code,omitempty"`
	Metadata                 map[string]string   `json:"metadata,omitempty"`
	CreatedAt                time.Time           `json:"created_at"`
	UpdatedAt                time.Time           `json:"updated_at"`
}

type TerminalRecordStatus string

const (
	TerminalRecordOpen   TerminalRecordStatus = "open"
	TerminalRecordClosed TerminalRecordStatus = "closed"
	TerminalRecordLost   TerminalRecordStatus = "lost"
)

type TerminalRecord struct {
	WorkspaceKey    string               `json:"workspace_key"`
	TerminalID      string               `json:"terminal_id"`
	AgentID         string               `json:"agent_id,omitempty"`
	SessionID       string               `json:"session_id,omitempty"`
	NodeID          string               `json:"node_id,omitempty"`
	TaskID          string               `json:"task_id,omitempty"`
	Title           string               `json:"title,omitempty"`
	Kind            string               `json:"kind,omitempty"`
	Status          TerminalRecordStatus `json:"status"`
	PTYProvider     string               `json:"pty_provider,omitempty"`
	StreamRef       string               `json:"stream_ref,omitempty"`
	TranscriptRef   string               `json:"transcript_ref,omitempty"`
	AttachedClients int                  `json:"attached_clients,omitempty"`
	Metadata        map[string]string    `json:"metadata,omitempty"`
	StartedAt       time.Time            `json:"started_at"`
	LastSeenAt      time.Time            `json:"last_seen_at,omitempty"`
	EndedAt         *time.Time           `json:"ended_at,omitempty"`
	CreatedAt       time.Time            `json:"created_at"`
	UpdatedAt       time.Time            `json:"updated_at"`
}

type LeaseRecordStatus string

const (
	LeaseRecordActive   LeaseRecordStatus = "active"
	LeaseRecordReleased LeaseRecordStatus = "released"
	LeaseRecordExpired  LeaseRecordStatus = "expired"
)

// LeaseRecord is private adapter state. Token contains the backend's stored
// token representation; it is never part of Interaction's public projection.
type LeaseRecord struct {
	WorkspaceKey  string            `json:"workspace_key"`
	LeaseID       string            `json:"lease_id"`
	SessionID     string            `json:"session_id"`
	AgentID       string            `json:"agent_id,omitempty"`
	NodeID        string            `json:"node_id,omitempty"`
	Token         string            `json:"token,omitempty"`
	FencingToken  int64             `json:"fencing_token"`
	Status        LeaseRecordStatus `json:"status"`
	ExpiresAt     time.Time         `json:"expires_at"`
	LastHeartbeat time.Time         `json:"last_heartbeat"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

type InboxRecordStatus string

const (
	InboxRecordQueued    InboxRecordStatus = "queued"
	InboxRecordDelivered InboxRecordStatus = "delivered"
	InboxRecordFailed    InboxRecordStatus = "failed"
)

type InboxRecord struct {
	WorkspaceKey      string            `json:"workspace_key"`
	InboxMessageID    string            `json:"inbox_message_id"`
	Cursor            int64             `json:"cursor"`
	TargetAgentID     string            `json:"target_agent_id"`
	SessionID         string            `json:"session_id,omitempty"`
	Body              string            `json:"body"`
	Status            InboxRecordStatus `json:"status"`
	SourceKind        string            `json:"source_kind,omitempty"`
	SourceRef         string            `json:"source_ref,omitempty"`
	DriverRunID       string            `json:"driver_run_id,omitempty"`
	TaskRunID         string            `json:"task_run_id,omitempty"`
	TriggerEventID    string            `json:"trigger_event_id,omitempty"`
	TriggerDeliveryID string            `json:"trigger_delivery_id,omitempty"`
	DedupeKey         string            `json:"dedupe_key,omitempty"`
	Attempt           int               `json:"attempt,omitempty"`
	ClaimedBy         string            `json:"claimed_by,omitempty"`
	ClaimExpiresAt    *time.Time        `json:"claim_expires_at,omitempty"`
	LastError         string            `json:"last_error,omitempty"`
	ErrorClass        string            `json:"error_class,omitempty"`
	DeliveredThreadID string            `json:"delivered_thread_id,omitempty"`
	DeliveredAt       *time.Time        `json:"delivered_at,omitempty"`
	CreatedAt         time.Time         `json:"created_at"`
	UpdatedAt         time.Time         `json:"updated_at"`
}
