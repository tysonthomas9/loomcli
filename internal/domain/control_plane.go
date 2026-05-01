package domain

import "time"

type AgentMode string

const (
	AgentModeEphemeral AgentMode = "ephemeral"
	AgentModeService   AgentMode = "service"
)

type AgentDesiredState string

const (
	AgentDesiredStopped  AgentDesiredState = "stopped"
	AgentDesiredIdle     AgentDesiredState = "idle"
	AgentDesiredRunning  AgentDesiredState = "running"
	AgentDesiredDraining AgentDesiredState = "draining"
)

type RuntimeProvider string

const (
	RuntimeProviderLocal      RuntimeProvider = "local"
	RuntimeProviderE2B        RuntimeProvider = "e2b"
	RuntimeProviderKubernetes RuntimeProvider = "kubernetes"
	RuntimeProviderCI         RuntimeProvider = "ci"
	RuntimeProviderOther      RuntimeProvider = "other"
)

type NodeDrainState string

const (
	NodeDrainActive   NodeDrainState = "active"
	NodeDrainDraining NodeDrainState = "draining"
	NodeDrainDrained  NodeDrainState = "drained"
)

type Node struct {
	WorkspaceKey    string          `json:"workspace_key"`
	NodeID          string          `json:"node_id"`
	OwnerActor      string          `json:"owner_actor,omitempty"`
	RuntimeProvider RuntimeProvider `json:"runtime_provider"`
	Labels          []string        `json:"labels,omitempty"`
	Capabilities    []string        `json:"capabilities,omitempty"`
	ToolInventory   []string        `json:"tool_inventory,omitempty"`
	Version         string          `json:"version,omitempty"`
	Capacity        int             `json:"capacity,omitempty"`
	DrainState      NodeDrainState  `json:"drain_state"`
	LastHeartbeat   time.Time       `json:"last_heartbeat"`
	ExpiresAt       time.Time       `json:"expires_at"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

type AgentSessionKind string

const (
	AgentSessionKindTask          AgentSessionKind = "task"
	AgentSessionKindOrchestration AgentSessionKind = "orchestration"
	AgentSessionKindTerminal      AgentSessionKind = "terminal"
	AgentSessionKindMaintenance   AgentSessionKind = "maintenance"
	AgentSessionKindAdHoc         AgentSessionKind = "ad_hoc"
)

type AgentSessionStatus string

const (
	AgentSessionQueued    AgentSessionStatus = "queued"
	AgentSessionLeased    AgentSessionStatus = "leased"
	AgentSessionStarting  AgentSessionStatus = "starting"
	AgentSessionRunning   AgentSessionStatus = "running"
	AgentSessionIdle      AgentSessionStatus = "idle"
	AgentSessionYielded   AgentSessionStatus = "yielded"
	AgentSessionCompleted AgentSessionStatus = "completed"
	AgentSessionFailed    AgentSessionStatus = "failed"
	AgentSessionCancelled AgentSessionStatus = "cancelled"
	AgentSessionExpired   AgentSessionStatus = "expired"
)

type AgentSession struct {
	WorkspaceKey    string             `json:"workspace_key"`
	SessionID       string             `json:"session_id"`
	AgentID         string             `json:"agent_id"`
	NodeID          string             `json:"node_id,omitempty"`
	Kind            AgentSessionKind   `json:"kind"`
	TaskID          string             `json:"task_id,omitempty"`
	TerminalID      string             `json:"terminal_id,omitempty"`
	ParentSessionID string             `json:"parent_session_id,omitempty"`
	Status          AgentSessionStatus `json:"status"`
	Phase           string             `json:"phase,omitempty"`
	Attempt         int                `json:"attempt,omitempty"`
	StartedAt       time.Time          `json:"started_at,omitempty"`
	LastHeartbeat   time.Time          `json:"last_heartbeat,omitempty"`
	FinishedAt      *time.Time         `json:"finished_at,omitempty"`
	Summary         string             `json:"summary,omitempty"`
	ErrorClass      string             `json:"error_class,omitempty"`
	ExitCode        *int               `json:"exit_code,omitempty"`
	Metadata        map[string]string  `json:"metadata,omitempty"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
}

type TerminalSessionStatus string

const (
	TerminalSessionOpen   TerminalSessionStatus = "open"
	TerminalSessionClosed TerminalSessionStatus = "closed"
	TerminalSessionLost   TerminalSessionStatus = "lost"
)

type TerminalSession struct {
	WorkspaceKey    string                `json:"workspace_key"`
	TerminalID      string                `json:"terminal_id"`
	AgentID         string                `json:"agent_id,omitempty"`
	SessionID       string                `json:"session_id,omitempty"`
	NodeID          string                `json:"node_id,omitempty"`
	TaskID          string                `json:"task_id,omitempty"`
	Title           string                `json:"title,omitempty"`
	Kind            string                `json:"kind,omitempty"`
	Status          TerminalSessionStatus `json:"status"`
	PTYProvider     string                `json:"pty_provider,omitempty"`
	StreamRef       string                `json:"stream_ref,omitempty"`
	TranscriptRef   string                `json:"transcript_ref,omitempty"`
	AttachedClients int                   `json:"attached_clients,omitempty"`
	Metadata        map[string]string     `json:"metadata,omitempty"`
	StartedAt       time.Time             `json:"started_at"`
	LastSeenAt      time.Time             `json:"last_seen_at,omitempty"`
	EndedAt         *time.Time            `json:"ended_at,omitempty"`
	CreatedAt       time.Time             `json:"created_at"`
	UpdatedAt       time.Time             `json:"updated_at"`
}

type Artifact struct {
	WorkspaceKey string            `json:"workspace_key"`
	ArtifactID   string            `json:"artifact_id"`
	AgentID      string            `json:"agent_id,omitempty"`
	SessionID    string            `json:"session_id,omitempty"`
	TerminalID   string            `json:"terminal_id,omitempty"`
	TaskID       string            `json:"task_id,omitempty"`
	Type         string            `json:"type"`
	URI          string            `json:"uri"`
	Summary      string            `json:"summary,omitempty"`
	MIMEType     string            `json:"mime_type,omitempty"`
	SizeBytes    int64             `json:"size_bytes,omitempty"`
	Checksum     string            `json:"checksum,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

type AgentLeaseStatus string

const (
	AgentLeaseActive   AgentLeaseStatus = "active"
	AgentLeaseReleased AgentLeaseStatus = "released"
	AgentLeaseExpired  AgentLeaseStatus = "expired"
)

type AgentLease struct {
	WorkspaceKey  string           `json:"workspace_key"`
	LeaseID       string           `json:"lease_id"`
	SessionID     string           `json:"session_id"`
	AgentID       string           `json:"agent_id,omitempty"`
	NodeID        string           `json:"node_id,omitempty"`
	Token         string           `json:"token,omitempty"`
	FencingToken  int64            `json:"fencing_token"`
	Status        AgentLeaseStatus `json:"status"`
	ExpiresAt     time.Time        `json:"expires_at"`
	LastHeartbeat time.Time        `json:"last_heartbeat"`
	CreatedAt     time.Time        `json:"created_at"`
	UpdatedAt     time.Time        `json:"updated_at"`
}

type AgentCommandStatus string

const (
	AgentCommandQueued    AgentCommandStatus = "queued"
	AgentCommandAcked     AgentCommandStatus = "acked"
	AgentCommandRunning   AgentCommandStatus = "running"
	AgentCommandSucceeded AgentCommandStatus = "succeeded"
	AgentCommandFailed    AgentCommandStatus = "failed"
	AgentCommandCancelled AgentCommandStatus = "cancelled"
)

type AgentCommand struct {
	WorkspaceKey  string             `json:"workspace_key"`
	CommandID     string             `json:"command_id"`
	Cursor        int64              `json:"cursor"`
	TargetAgentID string             `json:"target_agent_id,omitempty"`
	TargetNodeID  string             `json:"target_node_id,omitempty"`
	SessionID     string             `json:"session_id,omitempty"`
	Type          string             `json:"type"`
	Payload       map[string]string  `json:"payload,omitempty"`
	Status        AgentCommandStatus `json:"status"`
	AckedBy       string             `json:"acked_by,omitempty"`
	AckedAt       *time.Time         `json:"acked_at,omitempty"`
	Result        string             `json:"result,omitempty"`
	ErrorClass    string             `json:"error_class,omitempty"`
	CreatedAt     time.Time          `json:"created_at"`
	UpdatedAt     time.Time          `json:"updated_at"`
}
