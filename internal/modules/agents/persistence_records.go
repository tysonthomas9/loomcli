package agents

import "time"

// RuntimeMode and RuntimeDesiredState describe local agent-process behavior.
// They live with the Agents capability rather than in a horizontal domain
// bucket, but are not part of the durable Agent aggregate.
type RuntimeMode string

const (
	RuntimeModeEphemeral RuntimeMode = "ephemeral"
	RuntimeModeService   RuntimeMode = "service"
)

type RuntimeDesiredState string

const (
	RuntimeDesiredStopped  RuntimeDesiredState = "stopped"
	RuntimeDesiredIdle     RuntimeDesiredState = "idle"
	RuntimeDesiredRunning  RuntimeDesiredState = "running"
	RuntimeDesiredDraining RuntimeDesiredState = "draining"
)

// AgentServiceRecord preserves the backend record used while adapters map the
// historical AgentService wire to the canonical Agent aggregate.
type AgentServiceRecord struct {
	WorkspaceKey    string            `json:"workspace_key"`
	ServiceID       string            `json:"service_id"`
	GenerationID    string            `json:"generation_id"`
	Name            string            `json:"name"`
	Kind            AgentKind         `json:"kind"`
	DesiredState    DesiredState      `json:"desired_state"`
	RoleName        string            `json:"role_name"`
	DriverID        string            `json:"driver_id,omitempty"`
	DriverVersionID string            `json:"driver_version_id,omitempty"`
	ProfileName     string            `json:"profile_name,omitempty"`
	ScheduleID      string            `json:"schedule_id,omitempty"`
	EventSources    []string          `json:"event_sources,omitempty"`
	TriggerRefs     []string          `json:"trigger_refs,omitempty"`
	PlacementPolicy string            `json:"placement_policy,omitempty"`
	MaxInstances    int               `json:"max_instances"`
	LeaseID         string            `json:"lease_id,omitempty"`
	RestartPolicy   string            `json:"restart_policy,omitempty"`
	Permissions     []string          `json:"permissions,omitempty"`
	BudgetPolicy    string            `json:"budget_policy,omitempty"`
	StateRef        string            `json:"state_ref,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	CreatedBy       string            `json:"created_by,omitempty"`
	DeletedAt       *time.Time        `json:"deleted_at,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

// OwnershipRecord is the adapter-facing durable ownership generation. Token
// is never exposed by Agents APIs; public callers receive AgentOwnershipLease.
type OwnershipRecord struct {
	WorkspaceKey    string          `json:"workspace_key"`
	AgentID         string          `json:"agent_id"`
	LeaseID         string          `json:"lease_id"`
	OwnerID         string          `json:"owner_id,omitempty"`
	RuntimeProvider RuntimeProvider `json:"runtime_provider,omitempty"`
	NodeID          string          `json:"node_id,omitempty"`
	Token           string          `json:"token,omitempty"`
	FencingToken    int64           `json:"fencing_token"`
	Status          OwnershipStatus `json:"status"`
	ExpiresAt       time.Time       `json:"expires_at"`
	LastHeartbeat   time.Time       `json:"last_heartbeat"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}
