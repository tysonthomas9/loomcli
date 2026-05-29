package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

type DefinitionVersionApply struct {
	WorkspaceKey       string
	DefinitionType     domain.DefinitionType
	DefinitionName     string
	Version            string
	SourceHash         string
	BundleHash         string
	Manifest           json.RawMessage
	CapabilityManifest json.RawMessage
	CreatedBy          string
	Status             domain.DefinitionStatus
}

type DefinitionVersionFilter struct {
	DefinitionType domain.DefinitionType
	DefinitionName string
	Status         domain.DefinitionStatus
	Limit          int
}

type DefinitionVersionStore interface {
	Apply(ctx context.Context, in DefinitionVersionApply) (*domain.DefinitionVersion, error)
	Get(ctx context.Context, workspaceKey string, typ domain.DefinitionType, name, version string) (*domain.DefinitionVersion, error)
	List(ctx context.Context, workspaceKey string, filter DefinitionVersionFilter) ([]*domain.DefinitionVersion, error)
}

type WorkflowDefinitionUpsert struct {
	WorkspaceKey       string
	Name               string
	Version            string
	Description        string
	InputSchema        json.RawMessage
	ResultSchema       json.RawMessage
	SingletonPolicy    string
	RuntimeProfileName string
	SourceRef          string
	BundleHash         string
	Manifest           json.RawMessage
	CapabilityManifest json.RawMessage
	Status             domain.DefinitionStatus
}

type WorkflowDefinitionFilter struct {
	Status domain.DefinitionStatus
	Limit  int
}

type WorkflowDefinitionStore interface {
	Upsert(ctx context.Context, in WorkflowDefinitionUpsert) (*domain.WorkflowDefinition, error)
	Get(ctx context.Context, workspaceKey, name string) (*domain.WorkflowDefinition, error)
	List(ctx context.Context, workspaceKey string, filter WorkflowDefinitionFilter) ([]*domain.WorkflowDefinition, error)
}

type WorkflowRunCreate struct {
	WorkspaceKey    string
	RunID           string
	WorkflowName    string
	WorkflowVersion string
	BundleHash      string
	IdempotencyKey  string
	Input           json.RawMessage
	Status          domain.WorkflowRunStatus
	LeaseOwner      string
	LeaseToken      string
	StartedAt       time.Time
}

type WorkflowRunFilter struct {
	WorkflowName   string
	Status         domain.WorkflowRunStatus
	IdempotencyKey string
	Live           bool
	Limit          int
}

type WorkflowRunUpdate struct {
	Status        *domain.WorkflowRunStatus `json:"status,omitempty"`
	Result        *json.RawMessage          `json:"result,omitempty"`
	ErrorClass    *string                   `json:"error_class,omitempty"`
	ErrorMessage  *string                   `json:"error_message,omitempty"`
	WaitCondition *string                   `json:"wait_condition,omitempty"`
	LeaseOwner    *string                   `json:"lease_owner,omitempty"`
	LeaseToken    *string                   `json:"lease_token,omitempty"`
	FencingToken  *int64                    `json:"fencing_token,omitempty"`
	StartedAt     *time.Time                `json:"started_at,omitempty"`
	FinishedAt    **time.Time               `json:"finished_at,omitempty"`
}

type WorkflowRunStore interface {
	CreateOrResume(ctx context.Context, in WorkflowRunCreate) (*domain.WorkflowRun, error)
	Get(ctx context.Context, workspaceKey, runID string) (*domain.WorkflowRun, error)
	List(ctx context.Context, workspaceKey string, filter WorkflowRunFilter) ([]*domain.WorkflowRun, error)
	Update(ctx context.Context, workspaceKey, runID string, patch WorkflowRunUpdate) (*domain.WorkflowRun, error)
}

type TaskRunEnsure struct {
	WorkspaceKey    string
	TaskRunID       string
	IdempotencyKey  string
	WorkflowRunID   string
	WorkItemID      string
	RoleName        string
	ClaimActor      string
	ClaimEventID    string
	Status          domain.TaskRunStatus
	AgentID         string
	NodeID          string
	CommandID       string
	SessionID       string
	LeaseID         string
	ParentSessionID string
	Reason          string
	Metadata        map[string]string
}

type TaskRunFilter struct {
	WorkflowRunID string
	WorkItemID    string
	RoleName      string
	AgentID       string
	Status        domain.TaskRunStatus
	Live          bool
	Limit         int
}

type TaskRunUpdate struct {
	ClaimActor      *string               `json:"claim_actor,omitempty"`
	ClaimEventID    *string               `json:"claim_event_id,omitempty"`
	Status          *domain.TaskRunStatus `json:"status,omitempty"`
	AgentID         *string               `json:"agent_id,omitempty"`
	NodeID          *string               `json:"node_id,omitempty"`
	CommandID       *string               `json:"command_id,omitempty"`
	SessionID       *string               `json:"session_id,omitempty"`
	LeaseID         *string               `json:"lease_id,omitempty"`
	ParentSessionID *string               `json:"parent_session_id,omitempty"`
	Reason          *string               `json:"reason,omitempty"`
	StartedAt       *time.Time            `json:"started_at,omitempty"`
	FinishedAt      **time.Time           `json:"finished_at,omitempty"`
	ErrorClass      *string               `json:"error_class,omitempty"`
	ErrorMessage    *string               `json:"error_message,omitempty"`
	Metadata        *map[string]string    `json:"metadata,omitempty"`
}

type TaskRunStore interface {
	Ensure(ctx context.Context, in TaskRunEnsure) (*domain.TaskRun, error)
	Get(ctx context.Context, workspaceKey, taskRunID string) (*domain.TaskRun, error)
	List(ctx context.Context, workspaceKey string, filter TaskRunFilter) ([]*domain.TaskRun, error)
	Update(ctx context.Context, workspaceKey, taskRunID string, patch TaskRunUpdate) (*domain.TaskRun, error)
}

type RunEventAppend struct {
	WorkspaceKey  string
	EventID       string
	WorkflowRunID string
	TaskRunID     string
	Type          string
	Message       string
	Data          json.RawMessage
}

type RunEventFilter struct {
	WorkflowRunID string
	TaskRunID     string
	AfterIndex    int64
	Limit         int
}

type RunEventStore interface {
	Append(ctx context.Context, in RunEventAppend) (*domain.RunEvent, error)
	List(ctx context.Context, workspaceKey string, filter RunEventFilter) ([]*domain.RunEvent, error)
}

type RuntimeProfileUpsert struct {
	WorkspaceKey string
	Name         string
	Version      string
	Provider     domain.RuntimeProvider
	Image        string
	Repos        []string
	Env          []string
	CPU          string
	Memory       string
	Manifest     json.RawMessage
	Status       domain.DefinitionStatus
}

type RuntimeProfileFilter struct {
	Status domain.DefinitionStatus
	Limit  int
}

type RuntimeProfileStore interface {
	Upsert(ctx context.Context, in RuntimeProfileUpsert) (*domain.RuntimeProfile, error)
	Get(ctx context.Context, workspaceKey, name string) (*domain.RuntimeProfile, error)
	List(ctx context.Context, workspaceKey string, filter RuntimeProfileFilter) ([]*domain.RuntimeProfile, error)
}

type RouteBindingUpsert struct {
	WorkspaceKey   string
	BindingID      string
	DefinitionName string
	DefinitionType domain.DefinitionType
	Path           string
	Method         string
	AuthPolicy     string
	Status         domain.DefinitionStatus
}

type RouteBindingFilter struct {
	DefinitionName string
	Status         domain.DefinitionStatus
	Limit          int
}

type RouteBindingStore interface {
	Upsert(ctx context.Context, in RouteBindingUpsert) (*domain.RouteBinding, error)
	Get(ctx context.Context, workspaceKey, bindingID string) (*domain.RouteBinding, error)
	List(ctx context.Context, workspaceKey string, filter RouteBindingFilter) ([]*domain.RouteBinding, error)
}

type TriggerBindingUpsert struct {
	WorkspaceKey string
	BindingID    string
	WorkflowName string
	EventType    string
	Filter       json.RawMessage
	Status       domain.DefinitionStatus
}

type TriggerBindingFilter struct {
	WorkflowName string
	EventType    string
	Status       domain.DefinitionStatus
	Limit        int
}

type TriggerBindingStore interface {
	Upsert(ctx context.Context, in TriggerBindingUpsert) (*domain.TriggerBinding, error)
	Get(ctx context.Context, workspaceKey, bindingID string) (*domain.TriggerBinding, error)
	List(ctx context.Context, workspaceKey string, filter TriggerBindingFilter) ([]*domain.TriggerBinding, error)
}
