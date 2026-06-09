package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

type DriverCreate struct {
	WorkspaceKey    string
	DriverID        string
	Name            string
	OwnerType       domain.DriverOwnerType
	OwnerRef        string
	Description     string
	ActiveVersionID string
	Status          domain.DriverStatus
	Metadata        map[string]string
}

type DriverFilter struct {
	Name   string
	Status domain.DriverStatus
	Limit  int
}

type DriverUpdate struct {
	Name            *string
	OwnerType       *domain.DriverOwnerType
	OwnerRef        *string
	Description     *string
	ActiveVersionID *string
	Status          *domain.DriverStatus
	Metadata        *map[string]string
}

type DriverStore interface {
	Create(ctx context.Context, in DriverCreate) (*domain.Driver, error)
	Get(ctx context.Context, workspaceKey, driverID string) (*domain.Driver, error)
	List(ctx context.Context, workspaceKey string, filter DriverFilter) ([]*domain.Driver, error)
	Update(ctx context.Context, workspaceKey, driverID string, patch DriverUpdate) (*domain.Driver, error)
}

type DriverVersionCreate struct {
	WorkspaceKey     string
	VersionID        string
	DriverID         string
	Version          int
	SourceRef        string
	SourceDigest     string
	BundleRef        string
	BundleDigest     string
	Runtime          string
	Manifest         map[string]string
	BuildDiagnostics string
	ValidationStatus domain.DriverVersionValidationStatus
	CreatedBy        string
}

type DriverVersionFilter struct {
	DriverID         string
	ValidationStatus domain.DriverVersionValidationStatus
	Limit            int
}

type DriverVersionStore interface {
	Create(ctx context.Context, in DriverVersionCreate) (*domain.DriverVersion, error)
	Get(ctx context.Context, workspaceKey, versionID string) (*domain.DriverVersion, error)
	List(ctx context.Context, workspaceKey string, filter DriverVersionFilter) ([]*domain.DriverVersion, error)
}

type WorkerProfileCreate struct {
	WorkspaceKey  string
	ProfileID     string
	Name          string
	Role          string
	Backend       string
	RuntimePolicy map[string]string
	Repos         []string
	MaxPriority   *int
	MaxParallel   int
	ParentEpic    string
	Labels        []string
	Capabilities  []string
	Enabled       *bool
	Metadata      map[string]string
}

type WorkerProfileFilter struct {
	Role    string
	Backend string
	Enabled *bool
	Limit   int
}

type WorkerProfileUpdate struct {
	Name             *string
	Role             *string
	Backend          *string
	RuntimePolicy    *map[string]string
	Repos            *[]string
	MaxPriority      *int
	MaxParallel      *int
	ClearMaxPriority bool
	ParentEpic       *string
	Labels           *[]string
	Capabilities     *[]string
	Enabled          *bool
	Metadata         *map[string]string
}

type WorkerProfileStore interface {
	Create(ctx context.Context, in WorkerProfileCreate) (*domain.WorkerProfile, error)
	Get(ctx context.Context, workspaceKey, profileID string) (*domain.WorkerProfile, error)
	List(ctx context.Context, workspaceKey string, filter WorkerProfileFilter) ([]*domain.WorkerProfile, error)
	Update(ctx context.Context, workspaceKey, profileID string, patch WorkerProfileUpdate) (*domain.WorkerProfile, error)
	Delete(ctx context.Context, workspaceKey, profileID string) error
}

type AgentServiceCreate struct {
	WorkspaceKey    string
	ServiceID       string
	Name            string
	Kind            domain.AgentServiceKind
	DesiredState    domain.AgentServiceDesiredState
	RoleName        string
	ProfileName     string
	ScheduleID      string
	EventSources    []string
	TriggerRefs     []string
	PlacementPolicy string
	MaxInstances    int
	LeaseID         string
	RestartPolicy   string
	Permissions     []string
	BudgetPolicy    string
	StateRef        string
	Metadata        map[string]string
}

type AgentServiceFilter struct {
	Kind         domain.AgentServiceKind
	DesiredState domain.AgentServiceDesiredState
	RoleName     string
	ProfileName  string
	Limit        int
}

type AgentServiceUpdate struct {
	Name            *string
	Kind            *domain.AgentServiceKind
	DesiredState    *domain.AgentServiceDesiredState
	RoleName        *string
	ProfileName     *string
	ScheduleID      *string
	EventSources    *[]string
	TriggerRefs     *[]string
	PlacementPolicy *string
	MaxInstances    *int
	LeaseID         *string
	RestartPolicy   *string
	Permissions     *[]string
	BudgetPolicy    *string
	StateRef        *string
	Metadata        *map[string]string
}

type AgentServiceStore interface {
	Create(ctx context.Context, in AgentServiceCreate) (*domain.AgentService, error)
	Get(ctx context.Context, workspaceKey, serviceID string) (*domain.AgentService, error)
	List(ctx context.Context, workspaceKey string, filter AgentServiceFilter) ([]*domain.AgentService, error)
	Update(ctx context.Context, workspaceKey, serviceID string, patch AgentServiceUpdate) (*domain.AgentService, error)
	Delete(ctx context.Context, workspaceKey, serviceID string) error
}

type TriggerBindingCreate struct {
	WorkspaceKey         string
	BindingID            string
	Name                 string
	SourceKind           string
	SourceRef            string
	SourceConfigRef      string
	RouteKey             string
	Method               string
	PathTemplate         string
	Topic                string
	EventTypePatterns    []string
	FilterRef            string
	DriverID             string
	DriverVersionID      string
	TargetEntrypoint     string
	TargetAgentServiceID string
	ConcurrencyPolicy    domain.TriggerBindingConcurrencyPolicy
	IdempotencyPolicy    string
	AuthPolicy           string
	Permissions          []string
	Enabled              bool
}

type TriggerBindingFilter struct {
	SourceKind           string
	RouteKey             string
	DriverID             string
	TargetAgentServiceID string
	Enabled              *bool
	Limit                int
}

type TriggerBindingUpdate struct {
	Name                 *string
	SourceKind           *string
	SourceRef            *string
	SourceConfigRef      *string
	RouteKey             *string
	Method               *string
	PathTemplate         *string
	Topic                *string
	EventTypePatterns    *[]string
	FilterRef            *string
	DriverID             *string
	DriverVersionID      *string
	TargetEntrypoint     *string
	TargetAgentServiceID *string
	ConcurrencyPolicy    *domain.TriggerBindingConcurrencyPolicy
	IdempotencyPolicy    *string
	AuthPolicy           *string
	Permissions          *[]string
	Enabled              *bool
}

type TriggerBindingStore interface {
	Create(ctx context.Context, in TriggerBindingCreate) (*domain.TriggerBinding, error)
	Get(ctx context.Context, workspaceKey, bindingID string) (*domain.TriggerBinding, error)
	GetByRouteKey(ctx context.Context, workspaceKey, routeKey string) (*domain.TriggerBinding, error)
	List(ctx context.Context, workspaceKey string, filter TriggerBindingFilter) ([]*domain.TriggerBinding, error)
	Update(ctx context.Context, workspaceKey, bindingID string, patch TriggerBindingUpdate) (*domain.TriggerBinding, error)
}

type EpicRunCreate struct {
	RunID          string
	IdempotencyKey string
	Payload        json.RawMessage
}

type DriverRunCreate struct {
	WorkspaceKey    string
	RunID           string
	DriverID        string
	DriverVersionID string
	Entrypoint      string
	SourceKind      string
	SourceRef       string
	EpicID          string
	IdempotencyKey  string
	Payload         json.RawMessage
}

type DriverRunFilter struct {
	DriverID        string
	DriverVersionID string
	EpicID          string
	NodeID          string
	Status          domain.DriverRunStatus
	Limit           int
}

type DriverRunFinish struct {
	NodeID       string
	LeaseID      string
	FencingToken int64
	Status       domain.DriverRunStatus
	Summary      string
	ErrorClass   string
	Output       map[string]string
}

type StaleDriverRunRecovery struct {
	StaleBefore   time.Time `json:"stale_before,omitempty"`
	MaxAgeSeconds int64     `json:"max_age_seconds,omitempty"`
	ErrorClass    string    `json:"error_class,omitempty"`
	Summary       string    `json:"summary,omitempty"`
	Limit         int       `json:"limit,omitempty"`
}

type StaleDriverRunRecoveryResult struct {
	WorkspaceKey       string    `json:"workspace_key"`
	StaleBefore        time.Time `json:"stale_before"`
	RecoveredAt        time.Time `json:"recovered_at"`
	Recovered          int       `json:"recovered"`
	SkippedFresh       int       `json:"skipped_fresh"`
	RecoveredRunIDs    []string  `json:"recovered_run_ids,omitempty"`
	SkippedFreshRunIDs []string  `json:"skipped_fresh_run_ids,omitempty"`
}

type StaleTaskRunRecovery struct {
	StaleBefore   time.Time `json:"stale_before,omitempty"`
	MaxAgeSeconds int64     `json:"max_age_seconds,omitempty"`
	ErrorClass    string    `json:"error_class,omitempty"`
	ErrorMessage  string    `json:"error_message,omitempty"`
}

type StaleTaskRunRecoveryResult struct {
	WorkspaceKey         string    `json:"workspace_key"`
	DriverRunID          string    `json:"driver_run_id"`
	StaleBefore          time.Time `json:"stale_before"`
	RecoveredAt          time.Time `json:"recovered_at"`
	Recovered            int       `json:"recovered"`
	Released             int       `json:"released"`
	SkippedFresh         int       `json:"skipped_fresh"`
	SkippedActorMismatch int       `json:"skipped_actor_mismatch"`
	SkippedIssueNotFound int       `json:"skipped_issue_not_found"`
	RecoveredTaskRunIDs  []string  `json:"recovered_task_run_ids,omitempty"`
	ReleasedTaskIDs      []string  `json:"released_task_ids,omitempty"`
	ActorMismatchTaskIDs []string  `json:"actor_mismatch_task_ids,omitempty"`
	IssueNotFoundTaskIDs []string  `json:"issue_not_found_task_ids,omitempty"`
}

type DriverRunStore interface {
	Create(ctx context.Context, in DriverRunCreate) (*domain.DriverRun, error)
	CreateEpic(ctx context.Context, workspaceKey, epicID string, in EpicRunCreate) (*domain.DriverRun, error)
	Get(ctx context.Context, workspaceKey, runID string) (*domain.DriverRun, error)
	List(ctx context.Context, workspaceKey string, filter DriverRunFilter) ([]*domain.DriverRun, error)
	Claim(ctx context.Context, workspaceKey, runID, nodeID, leaseID string) (*domain.DriverRun, error)
	Heartbeat(ctx context.Context, workspaceKey, runID, nodeID, leaseID string, fencingToken int64) (*domain.DriverRun, error)
	Finish(ctx context.Context, workspaceKey, runID string, finish DriverRunFinish) (*domain.DriverRun, error)
	RecoverStale(ctx context.Context, workspaceKey string, recover StaleDriverRunRecovery) (*StaleDriverRunRecoveryResult, error)
	RecoverStaleTaskRuns(ctx context.Context, workspaceKey, runID string, recover StaleTaskRunRecovery) (*StaleTaskRunRecoveryResult, error)
}

var ErrDriverRunEventsUnavailable = errors.New("driver run event reader unsupported")

type DriverRunEventsReader interface {
	Events(ctx context.Context, workspaceKey, runID, after string, limit int) (*domain.PlatformEventsPage, error)
}

type DriverStepCreate struct {
	WorkspaceKey   string
	StepID         string
	DriverRunID    string
	StepKind       string
	Status         domain.DriverStepStatus
	TaskRunID      string
	ActionLedgerID string
	ExternalRef    string
	InputRef       string
	OutputRef      string
	StartedAt      time.Time
	EndedAt        *time.Time
	NodeID         string
	LeaseID        string
	FencingToken   int64
}

type DriverStepFilter struct {
	DriverRunID    string
	TaskRunID      string
	ActionLedgerID string
	StepKind       string
	Status         domain.DriverStepStatus
	Limit          int
}

type DriverStepUpdate struct {
	Status         *domain.DriverStepStatus `json:"status,omitempty"`
	TaskRunID      *string                  `json:"task_run_id,omitempty"`
	ActionLedgerID *string                  `json:"action_ledger_id,omitempty"`
	ExternalRef    *string                  `json:"external_ref,omitempty"`
	InputRef       *string                  `json:"input_ref,omitempty"`
	OutputRef      *string                  `json:"output_ref,omitempty"`
	StartedAt      *time.Time               `json:"started_at,omitempty"`
	ClearStartedAt bool                     `json:"clear_started_at,omitempty"`
	EndedAt        *time.Time               `json:"ended_at,omitempty"`
	ClearEndedAt   bool                     `json:"clear_ended_at,omitempty"`
	NodeID         string                   `json:"node_id,omitempty"`
	LeaseID        string                   `json:"lease_id,omitempty"`
	FencingToken   int64                    `json:"fencing_token,omitempty"`
}

type DriverStepStore interface {
	Create(ctx context.Context, in DriverStepCreate) (*domain.DriverStep, error)
	CreateForRun(ctx context.Context, workspaceKey, runID string, in DriverStepCreate) (*domain.DriverStep, error)
	Get(ctx context.Context, workspaceKey, stepID string) (*domain.DriverStep, error)
	List(ctx context.Context, workspaceKey string, filter DriverStepFilter) ([]*domain.DriverStep, error)
	ListForRun(ctx context.Context, workspaceKey, runID string, filter DriverStepFilter) ([]*domain.DriverStep, error)
	Update(ctx context.Context, workspaceKey, stepID string, update DriverStepUpdate) (*domain.DriverStep, error)
}

type TaskRunCreate struct {
	WorkspaceKey     string
	TaskRunID        string
	DriverRunID      string
	DriverStepID     string
	TaskID           string
	WorkerProfileID  string
	ProviderProfile  string
	Status           domain.TaskRunStatus
	NodeID           string
	LeaseID          string
	FencingToken     int64
	RunnerPlacement  domain.TaskRunPlacement
	SandboxPlacement domain.TaskRunPlacement
	RuntimeMetadata  map[string]string
}

type TaskRunFilter struct {
	DriverRunID     string
	DriverStepID    string
	TaskID          string
	WorkerProfileID string
	Status          domain.TaskRunStatus
	Limit           int
}

type TaskRunClaim struct {
	TaskRunID          string
	NodeID             string
	RunnerID           string
	LeaseID            string
	LeaseToken         string
	SupportedProviders []string
	Capabilities       []string
	WorkerProfileIDs   []string
	RunnerPlacement    domain.TaskRunPlacement
	SandboxPlacement   domain.TaskRunPlacement
	ClaimedAt          time.Time
}

type TaskRunFinish struct {
	NodeID           string
	LeaseID          string
	LeaseToken       string
	FencingToken     int64
	Status           domain.TaskRunStatus
	ExitCode         *int
	LogsRef          string
	ArtifactsRef     string
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	EstimatedCostUSD float64
	RuntimeMetadata  map[string]string
	ErrorClass       string
	ErrorMessage     string
	FinishedAt       time.Time
}

type TaskRunHeartbeat struct {
	NodeID          string
	LeaseID         string
	LeaseToken      string
	FencingToken    int64
	RuntimeMetadata map[string]string
	LogsRef         string
	ArtifactsRef    string
	HeartbeatAt     time.Time
}

type TaskRunComplete struct {
	CompletionID        string
	NodeID              string
	LeaseID             string
	LeaseToken          string
	FencingToken        int64
	Status              domain.TaskRunStatus
	ExitCode            *int
	LogsRef             string
	ArtifactsRef        string
	RequiredArtifactIDs []string
	RequireArtifacts    bool
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheWriteTokens    int64
	EstimatedCostUSD    float64
	RuntimeMetadata     map[string]string
	ErrorClass          string
	ErrorMessage        string
	CloseTask           bool
	CloseReason         string
	FinishedAt          time.Time
}

type TaskRunLogAppend struct {
	NodeID       string
	LeaseID      string
	LeaseToken   string
	FencingToken int64
	Stream       string
	Text         string
	Timestamp    time.Time
}

type TaskRunLogFilter struct {
	AfterSequence int64
	Limit         int
}

type TaskRunStore interface {
	Create(ctx context.Context, in TaskRunCreate) (*domain.TaskRun, error)
	ClaimQueued(ctx context.Context, workspaceKey string, claim TaskRunClaim) (*domain.TaskRun, error)
	Get(ctx context.Context, workspaceKey, taskRunID string) (*domain.TaskRun, error)
	List(ctx context.Context, workspaceKey string, filter TaskRunFilter) ([]*domain.TaskRun, error)
	Heartbeat(ctx context.Context, workspaceKey, taskRunID string, heartbeat TaskRunHeartbeat) (*domain.TaskRun, error)
	Finish(ctx context.Context, workspaceKey, taskRunID string, finish TaskRunFinish) (*domain.TaskRun, error)
	Complete(ctx context.Context, workspaceKey, taskRunID string, complete TaskRunComplete) (*domain.TaskRun, error)
	AppendLog(ctx context.Context, workspaceKey, taskRunID string, appendLog TaskRunLogAppend) (*domain.TaskRunLogEntry, error)
	ListLogs(ctx context.Context, workspaceKey, taskRunID string, filter TaskRunLogFilter) ([]*domain.TaskRunLogEntry, error)
}
