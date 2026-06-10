package domain

import (
	"encoding/json"
	"time"
)

type PlatformEvent struct {
	ID          string            `json:"id"`
	Timestamp   time.Time         `json:"timestamp"`
	Actor       string            `json:"actor"`
	Action      string            `json:"action"`
	EntityType  string            `json:"entity_type"`
	EntityID    string            `json:"entity_id"`
	WorkspaceID string            `json:"workspace_id"`
	Before      string            `json:"before,omitempty"`
	After       string            `json:"after,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
}

type PlatformEventsPage struct {
	Events []PlatformEvent `json:"events"`
	Cursor string          `json:"cursor"`
}

type DriverOwnerType string

const (
	DriverOwnerUser      DriverOwnerType = "user"
	DriverOwnerTeam      DriverOwnerType = "team"
	DriverOwnerLeadAgent DriverOwnerType = "lead_agent"
	DriverOwnerSystem    DriverOwnerType = "system"
)

type DriverStatus string

const (
	DriverStatusDraft    DriverStatus = "draft"
	DriverStatusActive   DriverStatus = "active"
	DriverStatusDisabled DriverStatus = "disabled"
	DriverStatusArchived DriverStatus = "archived"
)

type Driver struct {
	WorkspaceKey    string            `json:"workspace_key"`
	DriverID        string            `json:"driver_id"`
	Name            string            `json:"name"`
	OwnerType       DriverOwnerType   `json:"owner_type"`
	OwnerRef        string            `json:"owner_ref,omitempty"`
	Description     string            `json:"description,omitempty"`
	ActiveVersionID string            `json:"active_version_id,omitempty"`
	Status          DriverStatus      `json:"status"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type DriverVersionValidationStatus string

const (
	DriverVersionValidationPending DriverVersionValidationStatus = "pending"
	DriverVersionValidationPassed  DriverVersionValidationStatus = "passed"
	DriverVersionValidationFailed  DriverVersionValidationStatus = "failed"
)

type DriverVersion struct {
	WorkspaceKey     string                        `json:"workspace_key"`
	VersionID        string                        `json:"version_id"`
	DriverID         string                        `json:"driver_id"`
	Version          int                           `json:"version"`
	SourceRef        string                        `json:"source_ref"`
	SourceDigest     string                        `json:"source_digest"`
	BundleRef        string                        `json:"bundle_ref"`
	BundleDigest     string                        `json:"bundle_digest"`
	Runtime          string                        `json:"runtime,omitempty"`
	Manifest         map[string]string             `json:"manifest,omitempty"`
	BuildDiagnostics string                        `json:"build_diagnostics,omitempty"`
	ValidationStatus DriverVersionValidationStatus `json:"validation_status"`
	CreatedBy        string                        `json:"created_by,omitempty"`
	CreatedAt        time.Time                     `json:"created_at"`
}

type WorkerProfile struct {
	WorkspaceKey  string            `json:"workspace_key"`
	ProfileID     string            `json:"profile_id"`
	Name          string            `json:"name"`
	Role          string            `json:"role"`
	Backend       string            `json:"backend,omitempty"`
	RuntimePolicy map[string]string `json:"runtime_policy,omitempty"`
	Repos         []string          `json:"repos,omitempty"`
	MaxPriority   *int              `json:"max_priority,omitempty"`
	MaxParallel   int               `json:"max_parallel,omitempty"`
	ParentEpic    string            `json:"parent_epic,omitempty"`
	Labels        []string          `json:"labels,omitempty"`
	Capabilities  []string          `json:"capabilities,omitempty"`
	Enabled       bool              `json:"enabled"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	CreatedAt     time.Time         `json:"created_at"`
	UpdatedAt     time.Time         `json:"updated_at"`
}

type AgentServiceKind string

const (
	AgentServiceKindLead                 AgentServiceKind = "lead"
	AgentServiceKindSupport              AgentServiceKind = "support"
	AgentServiceKindTriage               AgentServiceKind = "triage"
	AgentServiceKindOnCall               AgentServiceKind = "on_call"
	AgentServiceKindScheduled            AgentServiceKind = "scheduled"
	AgentServiceKindMaintenance          AgentServiceKind = "maintenance"
	AgentServiceKindOrchestrator         AgentServiceKind = "orchestrator"
	AgentServiceKindAlwaysOn             AgentServiceKind = "always_on"
	AgentServiceKindCron                 AgentServiceKind = "cron"
	AgentServiceKindEvent                AgentServiceKind = "event"
	AgentServiceKindCampaignOrchestrator AgentServiceKind = "campaign_orchestrator"
)

type AgentServiceDesiredState string

const (
	AgentServiceDesiredRunning AgentServiceDesiredState = "running"
	AgentServiceDesiredStopped AgentServiceDesiredState = "stopped"
	AgentServiceDesiredPaused  AgentServiceDesiredState = "paused"
)

type AgentService struct {
	WorkspaceKey    string                   `json:"workspace_key"`
	ServiceID       string                   `json:"service_id"`
	Name            string                   `json:"name"`
	Kind            AgentServiceKind         `json:"kind"`
	DesiredState    AgentServiceDesiredState `json:"desired_state"`
	RoleName        string                   `json:"role_name"`
	ProfileName     string                   `json:"profile_name,omitempty"`
	ScheduleID      string                   `json:"schedule_id,omitempty"`
	EventSources    []string                 `json:"event_sources,omitempty"`
	TriggerRefs     []string                 `json:"trigger_refs,omitempty"`
	PlacementPolicy string                   `json:"placement_policy,omitempty"`
	MaxInstances    int                      `json:"max_instances"`
	LeaseID         string                   `json:"lease_id,omitempty"`
	RestartPolicy   string                   `json:"restart_policy,omitempty"`
	Permissions     []string                 `json:"permissions,omitempty"`
	BudgetPolicy    string                   `json:"budget_policy,omitempty"`
	StateRef        string                   `json:"state_ref,omitempty"`
	Metadata        map[string]string        `json:"metadata,omitempty"`
	CreatedAt       time.Time                `json:"created_at"`
	UpdatedAt       time.Time                `json:"updated_at"`
}

type TriggerBindingConcurrencyPolicy string

const (
	TriggerBindingConcurrencyAllow            TriggerBindingConcurrencyPolicy = "allow"
	TriggerBindingConcurrencyForbid           TriggerBindingConcurrencyPolicy = "forbid"
	TriggerBindingConcurrencyReplace          TriggerBindingConcurrencyPolicy = "replace"
	TriggerBindingConcurrencyQueue            TriggerBindingConcurrencyPolicy = "queue"
	TriggerBindingConcurrencyOneActivePerEpic TriggerBindingConcurrencyPolicy = "one_active_per_epic"
)

type TriggerBinding struct {
	WorkspaceKey         string                          `json:"workspace_key"`
	BindingID            string                          `json:"binding_id"`
	Name                 string                          `json:"name"`
	SourceKind           string                          `json:"source_kind"`
	SourceRef            string                          `json:"source_ref,omitempty"`
	SourceConfigRef      string                          `json:"source_config_ref,omitempty"`
	RouteKey             string                          `json:"route_key,omitempty"`
	Method               string                          `json:"method,omitempty"`
	PathTemplate         string                          `json:"path_template,omitempty"`
	Topic                string                          `json:"topic,omitempty"`
	EventTypePatterns    []string                        `json:"event_type_patterns,omitempty"`
	FilterRef            string                          `json:"filter_ref,omitempty"`
	DriverID             string                          `json:"driver_id"`
	DriverVersionID      string                          `json:"driver_version_id"`
	TargetEntrypoint     string                          `json:"target_entrypoint,omitempty"`
	TargetAgentServiceID string                          `json:"target_agent_service_id,omitempty"`
	ConcurrencyPolicy    TriggerBindingConcurrencyPolicy `json:"concurrency_policy"`
	IdempotencyPolicy    string                          `json:"idempotency_policy,omitempty"`
	AuthPolicy           string                          `json:"auth_policy,omitempty"`
	Permissions          []string                        `json:"permissions,omitempty"`
	Enabled              bool                            `json:"enabled"`
	CreatedAt            time.Time                       `json:"created_at"`
	UpdatedAt            time.Time                       `json:"updated_at"`
}

type DriverRunStatus string

const (
	DriverRunQueued      DriverRunStatus = "queued"
	DriverRunRunning     DriverRunStatus = "running"
	DriverRunCompleted   DriverRunStatus = "completed"
	DriverRunFailed      DriverRunStatus = "failed"
	DriverRunNeedsReview DriverRunStatus = "needs_review"
	DriverRunCancelled   DriverRunStatus = "cancelled"
)

func (s DriverRunStatus) IsTerminal() bool {
	switch s {
	case DriverRunCompleted, DriverRunFailed, DriverRunNeedsReview, DriverRunCancelled:
		return true
	default:
		return false
	}
}

type DriverRun struct {
	WorkspaceKey    string            `json:"workspace_key"`
	RunID           string            `json:"run_id"`
	DriverID        string            `json:"driver_id"`
	DriverVersionID string            `json:"driver_version_id"`
	Entrypoint      string            `json:"entrypoint,omitempty"`
	SourceKind      string            `json:"source_kind,omitempty"`
	SourceRef       string            `json:"source_ref,omitempty"`
	EpicID          string            `json:"epic_id,omitempty"`
	Status          DriverRunStatus   `json:"status"`
	NodeID          string            `json:"node_id,omitempty"`
	LeaseID         string            `json:"lease_id,omitempty"`
	FencingToken    int64             `json:"fencing_token,omitempty"`
	IdempotencyKey  string            `json:"idempotency_key,omitempty"`
	Payload         json.RawMessage   `json:"payload,omitempty"`
	Output          map[string]string `json:"output,omitempty"`
	Summary         string            `json:"summary,omitempty"`
	ErrorClass      string            `json:"error_class,omitempty"`
	StartedAt       time.Time         `json:"started_at,omitempty"`
	LastHeartbeat   time.Time         `json:"last_heartbeat,omitempty"`
	FinishedAt      *time.Time        `json:"finished_at,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type DriverStepStatus string

const (
	DriverStepQueued    DriverStepStatus = "queued"
	DriverStepRunning   DriverStepStatus = "running"
	DriverStepWaiting   DriverStepStatus = "waiting"
	DriverStepCompleted DriverStepStatus = "completed"
	DriverStepFailed    DriverStepStatus = "failed"
	DriverStepSkipped   DriverStepStatus = "skipped"
)

func (s DriverStepStatus) IsTerminal() bool {
	switch s {
	case DriverStepCompleted, DriverStepFailed, DriverStepSkipped:
		return true
	default:
		return false
	}
}

type DriverStep struct {
	WorkspaceKey   string           `json:"workspace_key"`
	StepID         string           `json:"step_id"`
	DriverRunID    string           `json:"driver_run_id"`
	StepKind       string           `json:"step_kind"`
	Status         DriverStepStatus `json:"status"`
	TaskRunID      string           `json:"task_run_id,omitempty"`
	ActionLedgerID string           `json:"action_ledger_id,omitempty"`
	ExternalRef    string           `json:"external_ref,omitempty"`
	InputRef       string           `json:"input_ref,omitempty"`
	OutputRef      string           `json:"output_ref,omitempty"`
	StartedAt      time.Time        `json:"started_at,omitempty"`
	EndedAt        *time.Time       `json:"ended_at,omitempty"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
}

type TaskRunStatus string

const (
	TaskRunQueued    TaskRunStatus = "queued"
	TaskRunRunning   TaskRunStatus = "running"
	TaskRunCompleted TaskRunStatus = "completed"
	TaskRunFailed    TaskRunStatus = "failed"
	TaskRunCancelled TaskRunStatus = "cancelled"
)

func (s TaskRunStatus) IsTerminal() bool {
	switch s {
	case TaskRunCompleted, TaskRunFailed, TaskRunCancelled:
		return true
	default:
		return false
	}
}

type TaskRun struct {
	WorkspaceKey     string            `json:"workspace_key"`
	TaskRunID        string            `json:"task_run_id"`
	DriverRunID      string            `json:"driver_run_id,omitempty"`
	DriverStepID     string            `json:"driver_step_id,omitempty"`
	TaskID           string            `json:"task_id"`
	WorkerProfileID  string            `json:"worker_profile_id,omitempty"`
	ProviderProfile  string            `json:"provider_profile,omitempty"`
	Status           TaskRunStatus     `json:"status"`
	NodeID           string            `json:"node_id,omitempty"`
	LeaseID          string            `json:"lease_id,omitempty"`
	FencingToken     int64             `json:"fencing_token,omitempty"`
	RunnerPlacement  TaskRunPlacement  `json:"runner_placement,omitempty"`
	SandboxPlacement TaskRunPlacement  `json:"sandbox_placement,omitempty"`
	ExitCode         *int              `json:"exit_code,omitempty"`
	LogsRef          string            `json:"logs_ref,omitempty"`
	ArtifactsRef     string            `json:"artifacts_ref,omitempty"`
	InputTokens      int64             `json:"input_tokens,omitempty"`
	OutputTokens     int64             `json:"output_tokens,omitempty"`
	CacheReadTokens  int64             `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int64             `json:"cache_write_tokens,omitempty"`
	EstimatedCostUSD float64           `json:"estimated_cost_usd,omitempty"`
	RuntimeMetadata  map[string]string `json:"runtime_metadata,omitempty"`
	StartedAt        time.Time         `json:"started_at,omitempty"`
	LastHeartbeat    time.Time         `json:"last_heartbeat,omitempty"`
	FinishedAt       *time.Time        `json:"finished_at,omitempty"`
	ErrorClass       string            `json:"error_class,omitempty"`
	ErrorMessage     string            `json:"error_message,omitempty"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
}

type TaskRunPlacement struct {
	Provider        string     `json:"provider,omitempty"`
	NodeID          string     `json:"node_id,omitempty"`
	RunnerID        string     `json:"runner_id,omitempty"`
	ProcessRef      string     `json:"process_ref,omitempty"`
	SandboxID       string     `json:"sandbox_id,omitempty"`
	ImageOrSnapshot string     `json:"image_or_snapshot,omitempty"`
	CWD             string     `json:"cwd,omitempty"`
	RepoRef         string     `json:"repo_ref,omitempty"`
	CleanupPolicy   string     `json:"cleanup_policy,omitempty"`
	StartedAt       time.Time  `json:"started_at,omitempty"`
	HeartbeatAt     time.Time  `json:"heartbeat_at,omitempty"`
	RetainedUntil   *time.Time `json:"retained_until,omitempty"`
}

func (p TaskRunPlacement) Empty() bool {
	return p.Provider == "" &&
		p.NodeID == "" &&
		p.RunnerID == "" &&
		p.ProcessRef == "" &&
		p.SandboxID == "" &&
		p.ImageOrSnapshot == "" &&
		p.CWD == "" &&
		p.RepoRef == "" &&
		p.CleanupPolicy == "" &&
		p.StartedAt.IsZero() &&
		p.HeartbeatAt.IsZero() &&
		p.RetainedUntil == nil
}

type TaskRunLogEntry struct {
	WorkspaceKey string    `json:"workspace_key"`
	TaskRunID    string    `json:"task_run_id"`
	Sequence     int64     `json:"sequence"`
	Stream       string    `json:"stream"`
	Text         string    `json:"text"`
	NodeID       string    `json:"node_id,omitempty"`
	LeaseID      string    `json:"lease_id,omitempty"`
	FencingToken int64     `json:"fencing_token,omitempty"`
	Timestamp    time.Time `json:"timestamp"`
	CreatedAt    time.Time `json:"created_at"`
}
