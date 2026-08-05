package driver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver/runnersettings"
)

const NoopTaskProviderEnvVar = "LOOM_DRIVER_ENABLE_TEST_NOOP_PROVIDER"

// ManagedAgentPolicyInputKey is the reserved TaskRun input field that carries
// Loom's server-derived, immutable policy for a UI-managed agent.
const ManagedAgentPolicyInputKey = "loomAgentPolicy"

// ManagedAgentPolicy is the versioned projection captured when a managed agent
// requests a TaskRun. The executor consumes this snapshot instead of joining
// live AgentService or Role records, so queued work and retries cannot drift.
type ManagedAgentPolicy struct {
	Version        int      `json:"version"`
	AgentServiceID string   `json:"agentServiceId"`
	RoleName       string   `json:"roleName"`
	RoleUpdatedAt  string   `json:"roleUpdatedAt,omitempty"`
	Backend        string   `json:"backend"`
	Model          string   `json:"model,omitempty"`
	Effort         string   `json:"effort,omitempty"`
	ReadOnly       bool     `json:"readOnly,omitempty"`
	AllowedTools   []string `json:"allowedTools,omitempty"`
	DeniedTools    []string `json:"deniedTools,omitempty"`
	MaxBudgetUSD   *float64 `json:"maxBudgetUsd,omitempty"`
}

func testNoopProviderEnabled() bool {
	return strings.TrimSpace(os.Getenv(NoopTaskProviderEnvVar)) == "1"
}

// resolveTaskRunnerBackend resolves the backend CLI for the local task runner.
// A server-stamped managed-agent policy wins so a caller-supplied legacy worker
// identifier cannot change a UI agent's credentialed backend. Unmanaged runs
// use the local-node setting and codex fallback; the retired supervised-agent
// projection is never consulted by the execution leaf.
func (e HostBridgeTaskExecutor) resolveTaskRunnerBackend(req TaskExecRequest, agentPolicy localTaskRunnerAgentPolicy) string {
	if backend := strings.TrimSpace(agentPolicy.Backend); backend != "" {
		return backend
	}
	if backend := runnersettings.RuntimeProvider(req.WorkspaceKey); backend != "" {
		return backend
	}
	return defaultTaskRunnerBackend
}

// RuntimeProviderForWorkspace exposes the driver's canonical local execution
// provider resolution to inbound adapters without leaking its settings store.
func RuntimeProviderForWorkspace(workspace string) string {
	return runnersettings.RuntimeProvider(workspace)
}

type localTaskRunnerAgentPolicy = ManagedAgentPolicy

// localTaskRunnerAgentPolicyFromInput reads only the immutable projection that
// loom serve inserts at the verified TaskRun request boundary. Execution never
// joins live AgentService or Role records, so a queued run cannot drift when an
// operator edits the agent before a retry or worker claim.
func localTaskRunnerAgentPolicyFromInput(input json.RawMessage) (localTaskRunnerAgentPolicy, bool, error) {
	if len(input) == 0 {
		return localTaskRunnerAgentPolicy{}, false, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(input, &object); err != nil || object == nil {
		return localTaskRunnerAgentPolicy{}, false, nil
	}
	rawPolicy, present := object[ManagedAgentPolicyInputKey]
	if !present {
		return localTaskRunnerAgentPolicy{}, false, nil
	}
	var policy localTaskRunnerAgentPolicy
	if err := json.Unmarshal(rawPolicy, &policy); err != nil {
		return localTaskRunnerAgentPolicy{}, true, fmt.Errorf("decode managed agent policy: %w", err)
	}
	policy.AgentServiceID = strings.TrimSpace(policy.AgentServiceID)
	policy.RoleName = strings.TrimSpace(policy.RoleName)
	policy.RoleUpdatedAt = strings.TrimSpace(policy.RoleUpdatedAt)
	policy.Backend = strings.TrimSpace(policy.Backend)
	policy.Model = strings.TrimSpace(policy.Model)
	policy.Effort = strings.TrimSpace(policy.Effort)
	if policy.Version != 1 || policy.AgentServiceID == "" || policy.RoleName == "" || policy.Backend == "" {
		return localTaskRunnerAgentPolicy{}, true, fmt.Errorf(
			"managed agent policy requires version 1, agentServiceId, roleName, and backend: %w",
			domain.ErrInvalid,
		)
	}
	return policy, true, nil
}

// localTaskRunnerRoleEnv mirrors the role constraints the legacy daemon
// supervisor exports. The local-task-runner consumes these values for prompt
// policy, read-only fail-closed verification, Claude effort, and budget.
func localTaskRunnerRoleEnv(policy localTaskRunnerAgentPolicy, backend string) []string {
	if policy.Version == 0 {
		return nil
	}
	env := make([]string, 0, 8)
	if len(policy.AllowedTools) > 0 {
		env = append(env, "LOOM_ALLOWED_TOOLS="+strings.Join(policy.AllowedTools, ","))
	}
	if len(policy.DeniedTools) > 0 {
		env = append(env, "LOOM_DENIED_TOOLS="+strings.Join(policy.DeniedTools, ","))
	}
	if policy.ReadOnly {
		env = append(env, "LOOM_READ_ONLY=1")
	}
	if policy.MaxBudgetUSD != nil {
		env = append(env, fmt.Sprintf("LOOM_MAX_BUDGET_USD=%.2f", *policy.MaxBudgetUSD))
	}
	if effort := strings.TrimSpace(policy.Effort); effort != "" {
		env = append(env, "LOOM_AGENT_EFFORT="+effort, "LOOM_CLAUDE_EFFORT="+effort)
	}
	if model := strings.TrimSpace(policy.Model); model != "" && strings.EqualFold(strings.TrimSpace(backend), "opencode") {
		env = append(env, "LOOM_OPENCODE_MODEL="+model)
	}
	return env
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
	// RetainWorkItemClaim keeps the parent DriverRun's exact Work Item claim
	// live after a successful non-closing child. Only the parent may retire it
	// through the fenced review-handoff command.
	RetainWorkItemClaim bool
	Input               json.RawMessage
}

const TaskRunCloseOnSuccessMetaKey = "close_task_on_success"
const TaskRunRetainWorkItemClaimMetaKey = "retain_work_item_claim_on_success"

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
	TaskRunAttempt   int                     `json:"task_run_attempt,omitempty"`
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
