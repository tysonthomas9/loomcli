package domain

import (
	"encoding/json"
	"time"
)

type DefinitionType string

const (
	DefinitionTypeAgent    DefinitionType = "agent"
	DefinitionTypeLead     DefinitionType = "lead"
	DefinitionTypeWorkflow DefinitionType = "workflow"
	DefinitionTypeTool     DefinitionType = "tool"
	DefinitionTypeSkill    DefinitionType = "skill"
	DefinitionTypeRuntime  DefinitionType = "runtime"
)

type DefinitionStatus string

const (
	DefinitionStatusDraft      DefinitionStatus = "draft"
	DefinitionStatusActive     DefinitionStatus = "active"
	DefinitionStatusDeprecated DefinitionStatus = "deprecated"
	DefinitionStatusDisabled   DefinitionStatus = "disabled"
)

type DefinitionVersion struct {
	WorkspaceKey       string           `json:"workspace_key"`
	DefinitionType     DefinitionType   `json:"definition_type"`
	DefinitionName     string           `json:"definition_name"`
	Version            string           `json:"version"`
	SourceHash         string           `json:"source_hash,omitempty"`
	BundleHash         string           `json:"bundle_hash,omitempty"`
	Manifest           json.RawMessage  `json:"manifest,omitempty"`
	CapabilityManifest json.RawMessage  `json:"capability_manifest,omitempty"`
	CreatedBy          string           `json:"created_by,omitempty"`
	Status             DefinitionStatus `json:"status"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
}

type WorkflowDefinition struct {
	WorkspaceKey       string           `json:"workspace_key"`
	Name               string           `json:"name"`
	Version            string           `json:"version"`
	Description        string           `json:"description,omitempty"`
	InputSchema        json.RawMessage  `json:"input_schema,omitempty"`
	ResultSchema       json.RawMessage  `json:"result_schema,omitempty"`
	SingletonPolicy    string           `json:"singleton_policy,omitempty"`
	RuntimeProfileName string           `json:"runtime_profile_name,omitempty"`
	SourceRef          string           `json:"source_ref,omitempty"`
	BundleHash         string           `json:"bundle_hash,omitempty"`
	Manifest           json.RawMessage  `json:"manifest,omitempty"`
	CapabilityManifest json.RawMessage  `json:"capability_manifest,omitempty"`
	Status             DefinitionStatus `json:"status"`
	CreatedAt          time.Time        `json:"created_at"`
	UpdatedAt          time.Time        `json:"updated_at"`
}

type WorkflowRunStatus string

const (
	WorkflowRunQueued    WorkflowRunStatus = "queued"
	WorkflowRunRunning   WorkflowRunStatus = "running"
	WorkflowRunWaiting   WorkflowRunStatus = "waiting"
	WorkflowRunCompleted WorkflowRunStatus = "completed"
	WorkflowRunFailed    WorkflowRunStatus = "failed"
	WorkflowRunCancelled WorkflowRunStatus = "cancelled"
)

type WorkflowRun struct {
	WorkspaceKey    string            `json:"workspace_key"`
	RunID           string            `json:"run_id"`
	WorkflowName    string            `json:"workflow_name"`
	WorkflowVersion string            `json:"workflow_version"`
	BundleHash      string            `json:"bundle_hash,omitempty"`
	IdempotencyKey  string            `json:"idempotency_key,omitempty"`
	Input           json.RawMessage   `json:"input,omitempty"`
	Status          WorkflowRunStatus `json:"status"`
	Result          json.RawMessage   `json:"result,omitempty"`
	ErrorClass      string            `json:"error_class,omitempty"`
	ErrorMessage    string            `json:"error_message,omitempty"`
	WaitCondition   string            `json:"wait_condition,omitempty"`
	LeaseOwner      string            `json:"lease_owner,omitempty"`
	LeaseToken      string            `json:"lease_token,omitempty"`
	FencingToken    int64             `json:"fencing_token,omitempty"`
	StartedAt       time.Time         `json:"started_at,omitempty"`
	FinishedAt      *time.Time        `json:"finished_at,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type TaskRunStatus string

const (
	TaskRunQueued    TaskRunStatus = "queued"
	TaskRunClaimed   TaskRunStatus = "claimed"
	TaskRunStarting  TaskRunStatus = "starting"
	TaskRunRunning   TaskRunStatus = "running"
	TaskRunBlocked   TaskRunStatus = "blocked"
	TaskRunPassed    TaskRunStatus = "passed"
	TaskRunFailed    TaskRunStatus = "failed"
	TaskRunCancelled TaskRunStatus = "cancelled"
	TaskRunExpired   TaskRunStatus = "expired"
)

type TaskRun struct {
	WorkspaceKey    string            `json:"workspace_key"`
	TaskRunID       string            `json:"task_run_id"`
	IdempotencyKey  string            `json:"idempotency_key"`
	WorkflowRunID   string            `json:"workflow_run_id"`
	WorkItemID      string            `json:"work_item_id"`
	RoleName        string            `json:"role_name"`
	ClaimActor      string            `json:"claim_actor,omitempty"`
	ClaimEventID    string            `json:"claim_event_id,omitempty"`
	Status          TaskRunStatus     `json:"status"`
	Attempt         int               `json:"attempt"`
	AgentID         string            `json:"agent_id,omitempty"`
	NodeID          string            `json:"node_id,omitempty"`
	CommandID       string            `json:"command_id,omitempty"`
	SessionID       string            `json:"session_id,omitempty"`
	LeaseID         string            `json:"lease_id,omitempty"`
	ParentSessionID string            `json:"parent_session_id,omitempty"`
	Reason          string            `json:"reason,omitempty"`
	StartedAt       time.Time         `json:"started_at,omitempty"`
	FinishedAt      *time.Time        `json:"finished_at,omitempty"`
	ErrorClass      string            `json:"error_class,omitempty"`
	ErrorMessage    string            `json:"error_message,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	CreatedAt       time.Time         `json:"created_at"`
	UpdatedAt       time.Time         `json:"updated_at"`
}

type RunEvent struct {
	WorkspaceKey  string          `json:"workspace_key"`
	EventID       string          `json:"event_id"`
	WorkflowRunID string          `json:"workflow_run_id"`
	TaskRunID     string          `json:"task_run_id,omitempty"`
	EventIndex    int64           `json:"event_index"`
	Type          string          `json:"type"`
	Message       string          `json:"message,omitempty"`
	Data          json.RawMessage `json:"data,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

type RuntimeProfile struct {
	WorkspaceKey string           `json:"workspace_key"`
	Name         string           `json:"name"`
	Version      string           `json:"version"`
	Provider     RuntimeProvider  `json:"provider"`
	Image        string           `json:"image,omitempty"`
	Repos        []string         `json:"repos,omitempty"`
	Env          []string         `json:"env,omitempty"`
	CPU          string           `json:"cpu,omitempty"`
	Memory       string           `json:"memory,omitempty"`
	Manifest     json.RawMessage  `json:"manifest,omitempty"`
	Status       DefinitionStatus `json:"status"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
}

type RouteBinding struct {
	WorkspaceKey   string           `json:"workspace_key"`
	BindingID      string           `json:"binding_id"`
	DefinitionName string           `json:"definition_name"`
	DefinitionType DefinitionType   `json:"definition_type"`
	Path           string           `json:"path"`
	Method         string           `json:"method,omitempty"`
	AuthPolicy     string           `json:"auth_policy,omitempty"`
	Status         DefinitionStatus `json:"status"`
	CreatedAt      time.Time        `json:"created_at"`
	UpdatedAt      time.Time        `json:"updated_at"`
}

type TriggerBinding struct {
	WorkspaceKey string           `json:"workspace_key"`
	BindingID    string           `json:"binding_id"`
	WorkflowName string           `json:"workflow_name"`
	EventType    string           `json:"event_type"`
	Filter       json.RawMessage  `json:"filter,omitempty"`
	Status       DefinitionStatus `json:"status"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
}

func WorkflowRunStatusLive(status WorkflowRunStatus) bool {
	switch status {
	case "", WorkflowRunQueued, WorkflowRunRunning, WorkflowRunWaiting:
		return true
	default:
		return false
	}
}

func TaskRunStatusLive(status TaskRunStatus) bool {
	switch status {
	case "", TaskRunQueued, TaskRunClaimed, TaskRunStarting, TaskRunRunning, TaskRunBlocked:
		return true
	default:
		return false
	}
}
