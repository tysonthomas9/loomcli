package agents

import (
	"regexp"
	"strings"
	"time"
)

const (
	RoleKindInteractive = "interactive"
	RoleKindWorker      = "worker"
)

var (
	legacyAgentIdentifier = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
	storedAgentIdentifier = regexp.MustCompile(`^[a-z0-9]([a-z0-9._-]{0,98}[a-z0-9])?$`)
)

// ValidAgentIdentifier reports whether value is either a legacy local Agent
// identifier or the canonical Fleet-stored form. The stored form permits dots
// but neither form permits separators or target/control punctuation.
func ValidAgentIdentifier(value string) bool {
	return legacyAgentIdentifier.MatchString(value) || ValidStoredAgentName(value)
}

// ValidStoredAgentName reports whether value matches Fleet's canonical Agent
// name contract: lowercase, bounded, and without leading/trailing punctuation.
func ValidStoredAgentName(value string) bool {
	return storedAgentIdentifier.MatchString(value)
}

// ResolveRoleKind returns the Agents-owned runtime kind for one Role. Empty
// legacy roles retain the lead/orchestrator convention until their durable
// records are rewritten by an explicit migration.
func ResolveRoleKind(role *Role, fallbackName string) string {
	if role != nil {
		if kind := strings.ToLower(strings.TrimSpace(role.Kind)); kind != "" {
			return kind
		}
		if strings.TrimSpace(role.Name) != "" {
			fallbackName = role.Name
		}
	}
	switch strings.ToLower(strings.TrimSpace(fallbackName)) {
	case "lead", "orchestrator":
		return RoleKindInteractive
	default:
		return RoleKindWorker
	}
}

// AgentKind is the durable AgentService kind. It is descriptive placement
// metadata, not a behavior discriminator: Behavior is authoritative.
type AgentKind string

const (
	AgentKindLead                 AgentKind = "lead"
	AgentKindSupport              AgentKind = "support"
	AgentKindTriage               AgentKind = "triage"
	AgentKindOnCall               AgentKind = "on_call"
	AgentKindScheduled            AgentKind = "scheduled"
	AgentKindMaintenance          AgentKind = "maintenance"
	AgentKindOrchestrator         AgentKind = "orchestrator"
	AgentKindAlwaysOn             AgentKind = "always_on"
	AgentKindCron                 AgentKind = "cron"
	AgentKindEvent                AgentKind = "event"
	AgentKindCampaignOrchestrator AgentKind = "campaign_orchestrator"
)

// DesiredState is Agents-owned durable intent. Runtime and session state are
// deliberately absent from this capability model.
type DesiredState string

const (
	DesiredRunning DesiredState = "running"
	DesiredStopped DesiredState = "stopped"
	DesiredPaused  DesiredState = "paused"
)

// BehaviorReference points to configuration owned by Role or Workflow
// Catalog. Exactly one representation is valid: RoleName, or the complete
// DriverID/DriverVersionID pair. Agent identity never embeds prompt or source.
type BehaviorReference struct {
	RoleName        string `json:"role_name,omitempty"`
	DriverID        string `json:"driver_id,omitempty"`
	DriverVersionID string `json:"driver_version_id,omitempty"`
}

// RoleReference is the immutable Role fact Agents needs to validate a
// role-backed Agent. Role policy remains behind its own Agents-owned port.
type RoleReference struct {
	WorkspaceKey string `json:"workspace_key"`
	RoleName     string `json:"role_name"`
}

// Role is the Agents-owned durable behavior policy for role-backed Agents.
// It intentionally contains no session, terminal, Git, worktree, or trigger
// state. Prompt and PromptFile are mutually compatible legacy representations
// during Phase 5; callers must preserve both exactly when ensuring a role.
type Role struct {
	WorkspaceKey   string    `json:"workspace_key"`
	Name           string    `json:"name"`
	Kind           string    `json:"kind,omitempty"`
	Description    string    `json:"description,omitempty"`
	Prompt         string    `json:"prompt,omitempty"`
	PromptFile     string    `json:"prompt_file,omitempty"`
	Model          string    `json:"model,omitempty"`
	TaskFilter     string    `json:"task_filter,omitempty"`
	Backend        string    `json:"backend,omitempty"`
	Effort         string    `json:"effort,omitempty"`
	PathPatterns   []string  `json:"path_patterns,omitempty"`
	Skills         []string  `json:"skills,omitempty"`
	MaxPriority    *int      `json:"max_priority,omitempty"`
	MaxConcurrency *int      `json:"max_concurrency,omitempty"`
	ReadOnly       bool      `json:"read_only,omitempty"`
	AllowedTools   []string  `json:"allowed_tools,omitempty"`
	DeniedTools    []string  `json:"denied_tools,omitempty"`
	MaxBudgetUSD   *float64  `json:"max_budget_usd,omitempty"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// RoleDefinition contains only caller-controlled Role fields.
type RoleDefinition struct {
	Name           string   `json:"name"`
	Kind           string   `json:"kind,omitempty"`
	Description    string   `json:"description,omitempty"`
	Prompt         string   `json:"prompt,omitempty"`
	PromptFile     string   `json:"prompt_file,omitempty"`
	Model          string   `json:"model,omitempty"`
	TaskFilter     string   `json:"task_filter,omitempty"`
	Backend        string   `json:"backend,omitempty"`
	Effort         string   `json:"effort,omitempty"`
	PathPatterns   []string `json:"path_patterns,omitempty"`
	Skills         []string `json:"skills,omitempty"`
	MaxPriority    *int     `json:"max_priority,omitempty"`
	MaxConcurrency *int     `json:"max_concurrency,omitempty"`
	ReadOnly       bool     `json:"read_only,omitempty"`
	AllowedTools   []string `json:"allowed_tools,omitempty"`
	DeniedTools    []string `json:"denied_tools,omitempty"`
	MaxBudgetUSD   *float64 `json:"max_budget_usd,omitempty"`
}

// Agent is the transport-neutral durable Agent identity. EventSources and
// TriggerRefs are declarative compatibility selectors; live trigger delivery,
// AgentSession, PTY, Git, worktree, and execution state stay outside Agents.
type Agent struct {
	WorkspaceKey    string            `json:"workspace_key"`
	AgentID         string            `json:"agent_id"`
	GenerationID    string            `json:"generation_id"`
	Name            string            `json:"name"`
	Kind            AgentKind         `json:"kind"`
	Behavior        BehaviorReference `json:"behavior"`
	DesiredState    DesiredState      `json:"desired_state"`
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

func ValidGenerationID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for index := range value {
		char := value[index]
		if char < '0' || char > '9' {
			if char < 'a' || char > 'f' {
				return false
			}
		}
	}
	return true
}

// RuntimeProvider identifies where an Agents-owned desired-state controller
// holds its ownership generation. It is an opaque placement fact here.
type RuntimeProvider string

const (
	RuntimeProviderLocal      RuntimeProvider = "local"
	RuntimeProviderE2B        RuntimeProvider = "e2b"
	RuntimeProviderKubernetes RuntimeProvider = "kubernetes"
	RuntimeProviderCI         RuntimeProvider = "ci"
	RuntimeProviderOther      RuntimeProvider = "other"
)

// OwnershipStatus is the durable lifecycle of an AgentOwnershipLease.
type OwnershipStatus string

const (
	OwnershipActive   OwnershipStatus = "active"
	OwnershipReleased OwnershipStatus = "released"
	OwnershipExpired  OwnershipStatus = "expired"
)

// AgentOwnershipLease is the public, non-secret projection of the exact
// controller generation that owns an Agent. The raw lease token is returned
// only in OwnershipGrant and accepted only through OwnershipProof.
type AgentOwnershipLease struct {
	WorkspaceKey    string          `json:"workspace_key"`
	AgentID         string          `json:"agent_id"`
	LeaseID         string          `json:"lease_id"`
	OwnerID         string          `json:"owner_id"`
	RuntimeProvider RuntimeProvider `json:"runtime_provider,omitempty"`
	NodeID          string          `json:"node_id,omitempty"`
	FencingToken    int64           `json:"fencing_token"`
	Status          OwnershipStatus `json:"status"`
	ExpiresAt       time.Time       `json:"expires_at"`
	LastHeartbeat   time.Time       `json:"last_heartbeat"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// OwnershipGrant carries the secret returned when ownership is acquired. The
// token is process-local credential material and cannot be JSON serialized.
type OwnershipGrant struct {
	Lease      *AgentOwnershipLease `json:"lease"`
	LeaseToken string               `json:"-"`
}

// OwnershipProof is the full tuple a durable adapter must validate atomically
// for renewal, release, and owner-fenced desired-state commands. LeaseToken
// must never appear in DTO responses, logs, metadata, or process arguments.
type OwnershipProof struct {
	WorkspaceKey    string          `json:"-"`
	AgentID         string          `json:"-"`
	LeaseID         string          `json:"-"`
	LeaseToken      string          `json:"-"`
	OwnerID         string          `json:"-"`
	RuntimeProvider RuntimeProvider `json:"-"`
	NodeID          string          `json:"-"`
	FencingToken    int64           `json:"-"`
}

func cloneAgent(in *Agent) *Agent {
	if in == nil {
		return nil
	}
	out := *in
	out.EventSources = append([]string(nil), in.EventSources...)
	out.TriggerRefs = append([]string(nil), in.TriggerRefs...)
	out.Permissions = append([]string(nil), in.Permissions...)
	out.Metadata = cloneStringMap(in.Metadata)
	if in.DeletedAt != nil {
		value := *in.DeletedAt
		out.DeletedAt = &value
	}
	return &out
}

func cloneRole(in *Role) *Role {
	if in == nil {
		return nil
	}
	out := *in
	out.PathPatterns = append([]string(nil), in.PathPatterns...)
	out.Skills = append([]string(nil), in.Skills...)
	out.AllowedTools = append([]string(nil), in.AllowedTools...)
	out.DeniedTools = append([]string(nil), in.DeniedTools...)
	out.MaxPriority = cloneInt(in.MaxPriority)
	out.MaxConcurrency = cloneInt(in.MaxConcurrency)
	out.MaxBudgetUSD = cloneFloat64(in.MaxBudgetUSD)
	return &out
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneInt(in *int) *int {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneFloat64(in *float64) *float64 {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneLease(in *AgentOwnershipLease) *AgentOwnershipLease {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneGrant(in *OwnershipGrant) *OwnershipGrant {
	if in == nil {
		return nil
	}
	return &OwnershipGrant{Lease: cloneLease(in.Lease), LeaseToken: in.LeaseToken}
}
