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
	RuntimeProviderDaytona    RuntimeProvider = "daytona"
	RuntimeProviderCI         RuntimeProvider = "ci"
	RuntimeProviderOther      RuntimeProvider = "other"
)

type NodeDrainState string

const (
	NodeDrainActive   NodeDrainState = "active"
	NodeDrainDraining NodeDrainState = "draining"
	NodeDrainDrained  NodeDrainState = "drained"
)

type PlacementState string

const (
	PlacementStateProvisioning PlacementState = "provisioning"
	PlacementStateActive       PlacementState = "active"
	PlacementStateReleasing    PlacementState = "releasing"
	PlacementStateReleased     PlacementState = "released"
	PlacementStateLost         PlacementState = "lost"
)

type PlacementReleaseReason string

const (
	PlacementReleaseReasonUnspecified         PlacementReleaseReason = ""
	PlacementReleaseReasonLostConfirmedAbsent PlacementReleaseReason = "lost_confirmed_absent"
	// PlacementReleaseReasonCreateConfirmedAbsent records that an
	// empty-sandbox-id provisioning row was released only after the two-pass
	// create-absence protocol confirmed no sandbox exists.
	PlacementReleaseReasonCreateConfirmedAbsent PlacementReleaseReason = "create_confirmed_absent"
)

type Node struct {
	WorkspaceKey    string          `json:"workspace_key"`
	NodeID          string          `json:"node_id"`
	OwnerActor      string          `json:"owner_actor,omitempty"`
	RuntimeProvider RuntimeProvider `json:"runtime_provider"`
	Placement       *NodePlacement  `json:"placement,omitempty"`
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

type NodePlacement struct {
	SandboxID              string                 `json:"sandbox_id,omitempty"`
	Generation             int64                  `json:"generation"`
	ReservedVCPU           int                    `json:"reserved_vcpu,omitempty"`
	ReservedMemGiB         int                    `json:"reserved_mem_gib,omitempty"`
	State                  PlacementState         `json:"state"`
	LostAt                 *time.Time             `json:"lost_at,omitempty"`
	AbsenceConfirmedAt     *time.Time             `json:"absence_confirmed_at,omitempty"`
	ReleaseReason          PlacementReleaseReason `json:"release_reason,omitempty"`
	FirstAttachedAt        *time.Time             `json:"first_attached_at,omitempty"`
	LeadProcessStartedAt   *time.Time             `json:"lead_process_started_at,omitempty"`
	ProvisioningDeadlineAt *time.Time             `json:"provisioning_deadline_at,omitempty"`
	// ProvisionAmbiguousAt records that a sandbox create errored without
	// proving no sandbox exists. While set on a provisioning row with an empty
	// sandbox id, release requires the two-pass absence protocol.
	ProvisionAmbiguousAt *time.Time `json:"provision_ambiguous_at,omitempty"`
	// ProvisionAmbiguityDetail carries the (truncated) create error behind
	// ProvisionAmbiguousAt for operators and the attach surface.
	ProvisionAmbiguityDetail string `json:"provision_ambiguity_detail,omitempty"`
	// CreateAbsenceConfirmedAt is the first provider-confirmed zero-match
	// observation for an empty-sandbox-id provisioning row past its deadline.
	// Distinct from AbsenceConfirmedAt, which belongs to the lost protocol.
	CreateAbsenceConfirmedAt *time.Time `json:"create_absence_confirmed_at,omitempty"`
	// AttentionReason is a durable operator-facing block reason (for example
	// multiple provider sandboxes carrying this placement's label). Non-empty
	// marks the placement as needing attention.
	AttentionReason     string    `json:"attention_reason,omitempty"`
	SnapshotRef         string    `json:"snapshot_ref,omitempty"`
	AbandonedSandboxIDs []string  `json:"abandoned_sandbox_ids,omitempty"`
	DeleteAttempts      int       `json:"delete_attempts,omitempty"`
	LastDeleteError     string    `json:"last_delete_error,omitempty"`
	NextDeleteAt        time.Time `json:"next_delete_at,omitempty"`
}

func ResolveRuntimeProvider(agent *Agent, profile *DaemonProfile) RuntimeProvider {
	if agent != nil && agent.RuntimeProvider != "" {
		return agent.RuntimeProvider
	}
	if profile != nil && profile.RuntimeProvider != "" {
		return profile.RuntimeProvider
	}
	return RuntimeProviderLocal
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
	WorkspaceKey    string            `json:"workspace_key"`
	ArtifactID      string            `json:"artifact_id"`
	AgentID         string            `json:"agent_id,omitempty"`
	SessionID       string            `json:"session_id,omitempty"`
	TerminalID      string            `json:"terminal_id,omitempty"`
	TaskID          string            `json:"task_id,omitempty"`
	OwnerType       string            `json:"owner_type,omitempty"`
	OwnerID         string            `json:"owner_id,omitempty"`
	Type            string            `json:"type"`
	URI             string            `json:"uri,omitempty"`
	Summary         string            `json:"summary,omitempty"`
	MIMEType        string            `json:"mime_type,omitempty"`
	SizeBytes       int64             `json:"size_bytes,omitempty"`
	Checksum        string            `json:"checksum,omitempty"`
	ContentHash     string            `json:"content_hash,omitempty"`
	Visibility      string            `json:"visibility,omitempty"`
	RedactionStatus string            `json:"redaction_status,omitempty"`
	DurableStatus   string            `json:"durable_status,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	FinalizedAt     *time.Time        `json:"finalized_at,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
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

type AgentOwnershipLease struct {
	WorkspaceKey    string           `json:"workspace_key"`
	AgentID         string           `json:"agent_id"`
	LeaseID         string           `json:"lease_id"`
	OwnerID         string           `json:"owner_id,omitempty"`
	RuntimeProvider RuntimeProvider  `json:"runtime_provider,omitempty"`
	NodeID          string           `json:"node_id,omitempty"`
	Token           string           `json:"token,omitempty"`
	FencingToken    int64            `json:"fencing_token"`
	Status          AgentLeaseStatus `json:"status"`
	ExpiresAt       time.Time        `json:"expires_at"`
	LastHeartbeat   time.Time        `json:"last_heartbeat"`
	CreatedAt       time.Time        `json:"created_at"`
	UpdatedAt       time.Time        `json:"updated_at"`
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

type AgentInboxMessageStatus string

const (
	AgentInboxMessageQueued    AgentInboxMessageStatus = "queued"
	AgentInboxMessageDelivered AgentInboxMessageStatus = "delivered"
	AgentInboxMessageFailed    AgentInboxMessageStatus = "failed"
)

type AgentInboxMessage struct {
	WorkspaceKey      string                  `json:"workspace_key"`
	InboxMessageID    string                  `json:"inbox_message_id"`
	Cursor            int64                   `json:"cursor"`
	TargetAgentID     string                  `json:"target_agent_id"`
	SessionID         string                  `json:"session_id,omitempty"`
	Body              string                  `json:"body"`
	Status            AgentInboxMessageStatus `json:"status"`
	SourceKind        string                  `json:"source_kind,omitempty"`
	SourceRef         string                  `json:"source_ref,omitempty"`
	DriverRunID       string                  `json:"driver_run_id,omitempty"`
	TaskRunID         string                  `json:"task_run_id,omitempty"`
	TriggerEventID    string                  `json:"trigger_event_id,omitempty"`
	TriggerDeliveryID string                  `json:"trigger_delivery_id,omitempty"`
	DedupeKey         string                  `json:"dedupe_key,omitempty"`
	Attempt           int                     `json:"attempt,omitempty"`
	ClaimedBy         string                  `json:"claimed_by,omitempty"`
	ClaimExpiresAt    *time.Time              `json:"claim_expires_at,omitempty"`
	LastError         string                  `json:"last_error,omitempty"`
	ErrorClass        string                  `json:"error_class,omitempty"`
	DeliveredThreadID string                  `json:"delivered_thread_id,omitempty"`
	DeliveredAt       *time.Time              `json:"delivered_at,omitempty"`
	CreatedAt         time.Time               `json:"created_at"`
	UpdatedAt         time.Time               `json:"updated_at"`
}
