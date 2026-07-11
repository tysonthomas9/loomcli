package driver

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
)

const NoopTaskProviderEnvVar = "LOOM_DRIVER_ENABLE_TEST_NOOP_PROVIDER"

func testNoopProviderEnabled() bool {
	return strings.TrimSpace(os.Getenv(NoopTaskProviderEnvVar)) == "1"
}

type TaskRunRequestOptions struct {
	WorkspaceKey       string
	DriverRunID        string
	DriverStepID       string
	TaskRunID          string
	TaskID             string
	WorkerProfileID    string
	Runner             string
	RunnerRef          string
	RunnerKind         string
	RunnerEntrypoint   string
	RunnerVersionID    string
	RunnerTrustLevel   domain.DriverTrustLevel
	ProviderProfile    string
	ParentSessionID    string
	ParentNodeID       string
	ParentLeaseID      string
	ParentFence        int64
	NodeID             string
	RunnerID           string
	LeaseID            string
	LeaseToken         string
	SupportedProviders []string
	Capabilities       []string
	WorkerProfileIDs   []string
	RunnerPlacement    domain.TaskRunPlacement
	SandboxPlacement   domain.TaskRunPlacement
	HeartbeatInterval  time.Duration
	DeferCompletion    bool
	CloseTaskOnSuccess *bool
	Input              json.RawMessage
}

const TaskRunCloseOnSuccessMetaKey = "close_task_on_success"

func resolveCloseTaskOnSuccess(fallback bool, metadata map[string]string) bool {
	if metadata == nil {
		return fallback
	}
	raw, ok := metadata[TaskRunCloseOnSuccessMetaKey]
	if !ok {
		return fallback
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return parsed
}

type TaskRunWorkerOptions struct {
	WorkspaceKey       string
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
	HeartbeatInterval  time.Duration
	DeferCompletion    bool
	CloseTaskOnSuccess bool
	MaxAttempts        int
	Now                func() time.Time
}

type TaskExecRequest struct {
	WorkspaceKey     string                  `json:"workspace_key"`
	DriverRunID      string                  `json:"driver_run_id"`
	DriverStepID     string                  `json:"driver_step_id,omitempty"`
	TaskRunID        string                  `json:"task_run_id"`
	TaskID           string                  `json:"task_id"`
	WorkerProfileID  string                  `json:"worker_profile_id,omitempty"`
	Runner           string                  `json:"runner,omitempty"`
	RunnerRef        string                  `json:"runner_ref,omitempty"`
	RunnerKind       string                  `json:"runner_kind,omitempty"`
	RunnerEntrypoint string                  `json:"runner_entrypoint,omitempty"`
	RunnerVersionID  string                  `json:"runner_driver_version_id,omitempty"`
	RunnerTrustLevel domain.DriverTrustLevel `json:"runner_trust_level,omitempty"`
	ProviderProfile  string                  `json:"provider_profile,omitempty"`
	ParentSessionID  string                  `json:"parent_session_id,omitempty"`
	NodeID           string                  `json:"node_id,omitempty"`
	LeaseID          string                  `json:"lease_id,omitempty"`
	LeaseToken       string                  `json:"lease_token,omitempty"`
	FencingToken     int64                   `json:"fencing_token,omitempty"`
	RunnerPlacement  domain.TaskRunPlacement `json:"runner_placement,omitempty"`
	SandboxPlacement domain.TaskRunPlacement `json:"sandbox_placement,omitempty"`
	Input            json.RawMessage         `json:"input,omitempty"`
}

type TaskExecResult struct {
	Status           domain.TaskRunStatus
	ExitCode         int
	LogsRef          string
	ArtifactsRef     string
	ArtifactIDs      []string
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	EstimatedCostUSD float64
	RuntimeMetadata  map[string]string
	ErrorClass       string
	ErrorMessage     string
}

type TaskExecutor interface {
	ExecuteTask(ctx context.Context, req TaskExecRequest) (TaskExecResult, error)
}

type TaskProviderPreflighter interface {
	PreflightTaskProvider(ctx context.Context, opts TaskRunRequestOptions) (TaskRunRequestOptions, error)
}
