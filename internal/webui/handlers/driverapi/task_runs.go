package driverapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
)

// execTaskParams is the exec-task request body.
type execTaskParams struct {
	TaskID             string   `json:"taskId"`
	TaskRunID          string   `json:"taskRunId"`
	DriverStepID       string   `json:"driverStepId"`
	WorkerProfileID    string   `json:"workerProfileId"`
	Runner             string   `json:"runner"`
	ProviderProfile    string   `json:"providerProfile"`
	ParentSessionID    string   `json:"parentSessionId"`
	NodeID             string   `json:"nodeId"`
	TargetNodeID       string   `json:"targetNodeId"`
	RunnerID           string   `json:"runnerId"`
	SupportedProviders []string `json:"supportedProviders"`
	Capabilities       []string `json:"capabilities"`
	RepoRef            string   `json:"repoRef"`
	SandboxPlacement   struct {
		Provider  string `json:"provider"`
		SandboxID string `json:"sandboxId"`
		CWD       string `json:"cwd"`
		RepoRef   string `json:"repoRef"`
	} `json:"sandboxPlacement"`
	DeferCompletion bool `json:"deferCompletion"`
	EnqueueOnly     bool `json:"enqueueOnly"`
	// RetainWorkItemClaim is converted into trusted runtime metadata at this
	// verified parent boundary. Workflow callers cannot provide arbitrary
	// TaskRun metadata.
	RetainWorkItemClaim bool `json:"retainWorkItemClaim"`
	// CloseTask optionally overrides whether the serve task worker closes the
	// underlying task issue on success. Pointer so an absent field preserves the
	// worker default (true) byte-for-byte; a planner run passes false to leave
	// the card in design+review. Precedent: taskrunapi completeParams.CloseTask.
	CloseTask *bool `json:"closeTask,omitempty"`
	// Input is the optional task-run payload (camelCase driver wire). It is
	// persisted on the run and delivered verbatim to the runner.
	Input json.RawMessage `json:"input,omitempty"`
}

const managedAgentPolicyInputKey = driverpkg.ManagedAgentPolicyInputKey

// Keep the handler-local alias so tests describe the request-boundary
// projection while the driver package owns the single wire contract.
type managedAgentPolicyInput = driverpkg.ManagedAgentPolicy

func (p execTaskParams) requestOptions(ws string, id driverIdentity, fencingToken int64) driverpkg.TaskRunRequestOptions {
	opts := driverpkg.TaskRunRequestOptions{
		WorkspaceKey:        ws,
		DriverRunID:         id.RunID,
		DriverStepID:        p.DriverStepID,
		TaskRunID:           p.TaskRunID,
		TaskID:              p.TaskID,
		WorkerProfileID:     p.WorkerProfileID,
		Runner:              p.Runner,
		ProviderProfile:     p.ProviderProfile,
		ParentSessionID:     p.ParentSessionID,
		ParentNodeID:        id.NodeID,
		ParentLeaseID:       id.LeaseID,
		ParentFence:         fencingToken,
		NodeID:              firstNonEmpty(p.NodeID, p.TargetNodeID),
		RunnerID:            p.RunnerID,
		SupportedProviders:  p.SupportedProviders,
		Capabilities:        p.Capabilities,
		DeferCompletion:     p.DeferCompletion,
		CloseTaskOnSuccess:  p.CloseTask,
		RetainWorkItemClaim: p.RetainWorkItemClaim,
		Input:               p.Input,
		SandboxPlacement: domain.TaskRunPlacement{
			Provider:  p.SandboxPlacement.Provider,
			SandboxID: p.SandboxPlacement.SandboxID,
			CWD:       p.SandboxPlacement.CWD,
			RepoRef:   firstNonEmpty(p.SandboxPlacement.RepoRef, p.RepoRef),
		},
	}
	if strings.TrimSpace(p.Runner) == "" {
		opts.ProviderProfile = p.ProviderProfile
		opts.SupportedProviders = p.SupportedProviders
	}
	return opts
}

func (m *Module) taskRequestExecutor() driverpkg.HostBridgeTaskExecutor {
	return driverpkg.HostBridgeTaskExecutor{
		Store:            m.store,
		Artifacts:        m.artifacts,
		WorktreePath:     m.worktreePath,
		APIBaseURL:       m.apiBaseURL,
		LocalSettingsDir: m.localSettingsDir,
		WorktreeResolver: driverpkg.LocalTaskWorktreeResolver{
			Store:            m.store,
			Lineage:          driverpkg.DefaultStackLineageLookup(),
			LocalSettingsDir: m.localSettingsDir,
		},
		StackStore: driverpkg.DefaultStackStore(),
	}
}

func taskRunRequestCommand(ws string, parent *domain.DriverRun, owner execution.Owner, opts driverpkg.TaskRunRequestOptions) execution.RequestTaskRunCommand {
	taskRunID := strings.TrimSpace(opts.TaskRunID)
	if taskRunID == "" {
		taskRunID = execution.RequestedTaskRunID(parent.RunID, opts.TaskID)
	}
	driverStepID := strings.TrimSpace(opts.DriverStepID)
	if driverStepID == "" {
		driverStepID = execution.RequestedDriverStepID(parent.RunID, taskRunID)
	}
	return execution.RequestTaskRunCommand{
		WorkspaceKey: ws, RequestID: execution.RequestTaskRunRequestID(parent.RunID, taskRunID), ParentOwner: owner,
		TaskRunID: taskRunID, DriverRunID: parent.RunID, DriverStepID: driverStepID, WorkItemID: opts.TaskID,
		ClaimActionID:   execution.DriverRunWorkItemClaimActionID(execution.ClaimDriverRunWorkItemRequestID(parent.RunID, opts.TaskID)),
		WorkerProfileID: opts.WorkerProfileID, Runner: opts.Runner, RunnerRef: opts.RunnerRef,
		RunnerKind: opts.RunnerKind, RunnerEntrypoint: opts.RunnerEntrypoint, RunnerVersionID: opts.RunnerVersionID,
		ProviderProfile: opts.ProviderProfile, TargetNodeID: opts.NodeID,
		RequiredCapabilities: append([]string(nil), opts.Capabilities...),
		RunnerPlacement:      executionPlacementFromDomain(opts.RunnerPlacement),
		SandboxPlacement:     executionPlacementFromDomain(opts.SandboxPlacement),
		RuntimeMetadata:      taskRunRequestMetadata(opts),
		Input:                append(json.RawMessage(nil), opts.Input...), RequestedAt: time.Now().UTC(),
	}
}

func (m *Module) execTask(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[execTaskParams](body)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.TaskID) == "" {
		return nil, fmt.Errorf("taskId required: %w", domain.ErrInvalid)
	}
	if params.RetainWorkItemClaim && (params.CloseTask == nil || *params.CloseTask) {
		return nil, fmt.Errorf("retainWorkItemClaim requires closeTask=false: %w", domain.ErrInvalid)
	}
	if m.taskRunRequests == nil || m.executionAuthorities == nil {
		return nil, fmt.Errorf("execution TaskRun request capability is unavailable: %w", execution.ErrUnavailable)
	}
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	params.Input, err = m.snapshotManagedAgentPolicy(ctx, ws, parent, params.Input)
	if err != nil {
		return nil, err
	}
	fencingToken, err := id.FencingToken()
	if err != nil {
		return nil, err
	}
	opts := params.requestOptions(ws, id, fencingToken)
	opts, err = driverpkg.PrepareTaskRunRequest(ctx, m.store, opts, parent, m.taskRequestExecutor())
	if err != nil {
		return nil, fmt.Errorf("prepare task request: %w", err)
	}
	owner := execution.Owner{
		ResourceKind: execution.ResourceDriverRun, ResourceID: parent.RunID,
		NodeID: id.NodeID, LeaseID: id.LeaseID, LeaseToken: id.LeaseToken, FencingToken: fencingToken,
	}
	auth, err := m.executionAuthorities.ResolveDriverRunAuthority(ctx, ws, execution.ActionRequestTaskRun, owner)
	if err != nil {
		return nil, fmt.Errorf("resolve TaskRun request authority: %w", err)
	}
	run, err := m.taskRunRequests.RequestTaskRun(ctx, auth, taskRunRequestCommand(ws, parent, owner, opts))
	if err != nil {
		return nil, fmt.Errorf("request TaskRun: %w", err)
	}
	return taskRunResultFromExecution(run), nil
}

func (m *Module) snapshotManagedAgentPolicy(
	ctx context.Context,
	ws string,
	parent *domain.DriverRun,
	input json.RawMessage,
) (json.RawMessage, error) {
	agentServiceID := strings.TrimSpace(parent.AgentServiceID)
	object, objectInput, err := taskRunInputObject(input)
	if err != nil {
		return nil, err
	}
	if agentServiceID == "" {
		return stripUnmanagedAgentPolicyInput(input, object, objectInput)
	}
	if !objectInput {
		return nil, fmt.Errorf("managed agent TaskRun input must be a JSON object: %w", domain.ErrInvalid)
	}
	policy, err := m.resolveManagedAgentPolicy(ctx, ws, agentServiceID)
	if err != nil {
		return nil, err
	}
	rawPolicy, err := json.Marshal(policy)
	if err != nil {
		return nil, fmt.Errorf("encode managed TaskRun policy: %w", err)
	}
	object[managedAgentPolicyInputKey] = rawPolicy
	return json.Marshal(object)
}

func stripUnmanagedAgentPolicyInput(
	input json.RawMessage,
	object map[string]json.RawMessage,
	objectInput bool,
) (json.RawMessage, error) {
	if !objectInput {
		return append(json.RawMessage(nil), input...), nil
	}
	if _, reservedPresent := object[managedAgentPolicyInputKey]; !reservedPresent {
		return append(json.RawMessage(nil), input...), nil
	}
	delete(object, managedAgentPolicyInputKey)
	return json.Marshal(object)
}

func (m *Module) resolveManagedAgentPolicy(
	ctx context.Context,
	ws string,
	agentServiceID string,
) (managedAgentPolicyInput, error) {
	service, err := m.store.AgentServices().Get(ctx, ws, agentServiceID)
	if err != nil {
		return managedAgentPolicyInput{}, fmt.Errorf("resolve managed TaskRun agent service: %w", err)
	}
	roleName := strings.TrimSpace(service.RoleName)
	if roleName == "" {
		return managedAgentPolicyInput{}, fmt.Errorf("managed agent %q has no role: %w", agentServiceID, domain.ErrInvalid)
	}
	role, err := m.store.Roles().Get(ctx, ws, roleName)
	if err != nil {
		return managedAgentPolicyInput{}, fmt.Errorf("resolve managed TaskRun role %q: %w", roleName, err)
	}
	backend := m.resolveManagedAgentBackend(ctx, ws, service, role)
	policy := managedAgentPolicyInput{
		Version: 1, AgentServiceID: agentServiceID, RoleName: roleName, Backend: backend,
		Model: strings.TrimSpace(role.Model), Effort: strings.TrimSpace(role.Effort), ReadOnly: role.ReadOnly,
		AllowedTools: append([]string(nil), role.AllowedTools...), DeniedTools: append([]string(nil), role.DeniedTools...),
		MaxBudgetUSD: role.MaxBudgetUSD,
	}
	if !role.UpdatedAt.IsZero() {
		policy.RoleUpdatedAt = role.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return policy, nil
}

func (m *Module) resolveManagedAgentBackend(
	ctx context.Context,
	ws string,
	service *domain.AgentService,
	role *domain.Role,
) string {
	backend := firstNonEmpty(strings.TrimSpace(service.Metadata["backend"]), strings.TrimSpace(role.Backend))
	if backend == "" {
		if profile, profileErr := m.store.Daemon().Get(ctx, ws); profileErr == nil && profile != nil {
			backend = strings.TrimSpace(profile.AgentBackend)
		}
	}
	if backend == "" {
		backend = "codex"
	}
	return backend
}

func taskRunInputObject(input json.RawMessage) (map[string]json.RawMessage, bool, error) {
	if len(input) == 0 {
		return map[string]json.RawMessage{}, true, nil
	}
	if !json.Valid(input) {
		return nil, false, fmt.Errorf("TaskRun input must be valid JSON: %w", domain.ErrInvalid)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(input, &object); err != nil || object == nil {
		return nil, false, nil
	}
	return object, true, nil
}

func executionPlacementFromDomain(value domain.TaskRunPlacement) execution.Placement {
	return execution.Placement{
		Provider: value.Provider, NodeID: value.NodeID, RunnerID: value.RunnerID,
		SandboxID: value.SandboxID, CWD: value.CWD, RepoRef: value.RepoRef,
	}
}

func taskRunRequestMetadata(opts driverpkg.TaskRunRequestOptions) map[string]string {
	metadata := map[string]string{"driver_run_id": opts.DriverRunID, "requested_by": "driver"}
	for key, value := range map[string]string{
		"runner": opts.Runner, "runner_ref": opts.RunnerRef, "runner_kind": opts.RunnerKind,
		"runner_entrypoint": opts.RunnerEntrypoint, "runner_driver_version_id": opts.RunnerVersionID,
		"runner_trust_level": string(opts.RunnerTrustLevel), "parent_session_id": opts.ParentSessionID,
	} {
		if strings.TrimSpace(value) != "" {
			metadata[key] = value
		}
	}
	if opts.CloseTaskOnSuccess != nil {
		metadata[driverpkg.TaskRunCloseOnSuccessMetaKey] = strconv.FormatBool(*opts.CloseTaskOnSuccess)
	}
	if opts.RetainWorkItemClaim {
		metadata[driverpkg.TaskRunRetainWorkItemClaimMetaKey] = "true"
	}
	return metadata
}

func taskRunResultFromExecution(run *execution.TaskRun) driverpkg.TaskRunRequestResult {
	if run == nil {
		return driverpkg.TaskRunRequestResult{}
	}
	status := domain.TaskRunStatus(run.Status)
	if run.Status == execution.StatusSucceeded {
		status = domain.TaskRunCompleted
	}
	return driverpkg.TaskRunRequestResult{
		ID: run.TaskRunID, TaskRunID: run.TaskRunID, DriverStepID: run.DriverStepID, TaskID: run.WorkItemID,
		Status: status, ExitCode: run.ExitCode, LogsRef: run.LogsRef, ArtifactsRef: run.ArtifactsRef,
		InputTokens: run.InputTokens, OutputTokens: run.OutputTokens, CacheReadTokens: run.CacheReadTokens,
		CacheWriteTokens: run.CacheWriteTokens, EstimatedCostUSD: run.EstimatedCostUSD,
		ErrorClass: run.ErrorClass, ErrorMessage: run.ErrorMessage, FinishedAt: run.FinishedAt,
		Runner: run.Runner, RunnerRef: run.RunnerRef, RunnerKind: run.RunnerKind,
		RunnerEntrypoint: run.RunnerEntrypoint, RunnerVersionID: run.RunnerVersionID,
		ProviderProfile: run.ProviderProfile, RuntimeMetadata: cloneTaskRunResultMetadata(run.RuntimeMetadata),
	}
}

func cloneTaskRunResultMetadata(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func (m *Module) taskRunGet(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		TaskRunID string `json:"taskRunId"`
	}](body)
	if err != nil {
		return nil, err
	}
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	taskRunID := strings.TrimSpace(params.TaskRunID)
	if taskRunID == "" {
		return nil, fmt.Errorf("taskRunId required: %w", domain.ErrInvalid)
	}
	run, err := m.store.TaskRuns().Get(ctx, ws, taskRunID)
	if err != nil {
		return nil, fmt.Errorf("get task run: %w", err)
	}
	if run.DriverRunID != parent.RunID {
		return nil, fmt.Errorf("task run %q does not belong to driver run %q: %w", taskRunID, parent.RunID, domain.ErrNotFound)
	}
	return driverpkg.TaskRunResultFromDomain(run), nil
}

func (m *Module) activeTaskRuns(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[struct {
		EpicID string `json:"epicId"`
		Limit  int    `json:"limit"`
	}](body)
	if err != nil {
		return nil, err
	}
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	epicID := firstNonEmpty(params.EpicID, parent.EpicID, driverpkg.DriverRunPayloadEpicID(parent.Payload))
	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}
	active, err := driverpkg.ListActiveTaskRuns(ctx, m.store, driverpkg.ActiveTaskRunsOptions{
		WorkspaceKey: ws,
		DriverRunID:  parent.RunID,
		EpicID:       epicID,
		Limit:        limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list active task runs: %w", err)
	}
	return active, nil
}

type recoverStaleTasksParams struct {
	StaleBefore   string `json:"staleBefore"`
	MaxAgeSeconds int64  `json:"maxAgeSeconds"`
	ErrorClass    string `json:"errorClass"`
	ErrorMessage  string `json:"errorMessage"`
}

func staleTaskRecoveryWindow(params recoverStaleTasksParams) (time.Time, time.Time, error) {
	observedAt := time.Now().UTC()
	if raw := strings.TrimSpace(params.StaleBefore); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("parse staleBefore as RFC3339: %s: %w", err.Error(), domain.ErrInvalid)
		}
		return parsed.UTC(), observedAt, nil
	}
	maxAgeSeconds := params.MaxAgeSeconds
	if maxAgeSeconds <= 0 {
		maxAgeSeconds = 300
	}
	return observedAt.Add(-time.Duration(maxAgeSeconds) * time.Second), observedAt, nil
}

func (m *Module) recoverStaleTasks(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[recoverStaleTasksParams](body)
	if err != nil {
		return nil, err
	}
	if m.taskRunRecovery == nil || m.executionAuthorities == nil {
		return nil, fmt.Errorf("execution TaskRun recovery capability is unavailable: %w", execution.ErrUnavailable)
	}
	// Unlike the CLI path (already inside an authenticated process), the HTTP
	// surface must prove run ownership before failing its task runs.
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	staleBefore, observedAt, err := staleTaskRecoveryWindow(params)
	if err != nil {
		return nil, err
	}
	fence, err := id.FencingToken()
	if err != nil {
		return nil, err
	}
	owner := execution.Owner{
		ResourceKind: execution.ResourceDriverRun, ResourceID: parent.RunID,
		NodeID: id.NodeID, LeaseID: id.LeaseID, LeaseToken: id.LeaseToken, FencingToken: fence,
	}
	auth, err := m.executionAuthorities.ResolveDriverRunAuthority(ctx, ws, execution.ActionRecoverStaleChildTaskRuns, owner)
	if err != nil {
		return nil, fmt.Errorf("resolve stale child TaskRun authority: %w", err)
	}
	result, err := m.taskRunRecovery.RecoverStaleChildTaskRuns(ctx, auth, execution.RecoverStaleChildTaskRunsCommand{
		WorkspaceKey: ws, RequestID: execution.RecoverStaleChildTaskRunsRequestID(parent.RunID, staleBefore),
		ParentOwner: owner, DriverRunID: parent.RunID, StaleBefore: staleBefore,
		ErrorClass:   firstNonEmpty(params.ErrorClass, "stale_task_run"),
		ErrorMessage: firstNonEmpty(params.ErrorMessage, "task run heartbeat is stale"), ObservedAt: observedAt,
	})
	if err != nil {
		return nil, fmt.Errorf("recover stale task runs: %w", err)
	}
	return result, nil
}

type completeTaskParams struct {
	TaskID       string   `json:"taskId"`
	TaskRunID    string   `json:"taskRunId"`
	CompletionID string   `json:"completionId"`
	LeaseToken   string   `json:"leaseToken"`
	ArtifactIDs  []string `json:"artifactIds"`
	LogsRef      string   `json:"logsRef"`
	ArtifactsRef string   `json:"artifactsRef"`
	Reason       string   `json:"reason"`
}

func taskCompletionDefaults(params completeTaskParams, taskRunID string) (string, string) {
	completionID := strings.TrimSpace(params.CompletionID)
	if completionID == "" {
		completionID = "complete-" + taskRunID
	}
	reason := strings.TrimSpace(params.Reason)
	if reason == "" {
		reason = "completed by driver"
	}
	return completionID, reason
}

func (m *Module) completeTask(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[completeTaskParams](body)
	if err != nil {
		return nil, err
	}
	if m.taskRuns == nil || m.taskRunAuthorities == nil {
		return nil, fmt.Errorf("execution TaskRun finalize capability is unavailable: %w", execution.ErrUnavailable)
	}
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	taskRunID := strings.TrimSpace(params.TaskRunID)
	if taskRunID == "" {
		return nil, fmt.Errorf("taskRunId is required for fenced driver completion: %w", domain.ErrInvalid)
	}
	run, err := m.store.TaskRuns().Get(ctx, ws, taskRunID)
	if err != nil {
		return nil, fmt.Errorf("get task run: %w", err)
	}
	if run.DriverRunID != parent.RunID {
		return nil, fmt.Errorf("task run %q does not belong to driver run %q: %w", taskRunID, parent.RunID, domain.ErrNotFound)
	}
	if taskID := strings.TrimSpace(params.TaskID); taskID != "" && taskID != run.TaskID {
		return nil, fmt.Errorf("task run %q belongs to task %q, not %q: %w", taskRunID, run.TaskID, taskID, domain.ErrInvalid)
	}
	owner := execution.Owner{
		ResourceKind: execution.ResourceTaskRun, ResourceID: run.TaskRunID,
		NodeID: run.NodeID, LeaseID: run.LeaseID, LeaseToken: strings.TrimSpace(params.LeaseToken),
		FencingToken: run.FencingToken,
	}
	auth, err := m.taskRunAuthorities.ResolveTaskRunAuthority(ctx, ws, execution.ActionFinalize, owner)
	if err != nil {
		return nil, fmt.Errorf("resolve TaskRun finalize authority: %w", err)
	}
	completionID, reason := taskCompletionDefaults(params, taskRunID)
	_, err = m.taskRuns.Finalize(ctx, auth, execution.FinalizeCommand{
		WorkspaceKey: ws, RequestID: completionID, Owner: owner,
		Classification: execution.ExitClassification{Status: execution.StatusSucceeded},
		LogsRef:        params.LogsRef, ArtifactsRef: params.ArtifactsRef,
		RequiredArtifactIDs: append([]string(nil), params.ArtifactIDs...), RequireArtifacts: len(params.ArtifactIDs) > 0,
		CloseWorkItem: true, CloseReason: reason, FinishedAt: time.Now().UTC(),
	})
	if err != nil {
		return nil, fmt.Errorf("complete task run: %w", err)
	}
	return &driverpkg.TaskMutationResult{ID: run.TaskID, Status: string(domain.TaskRunCompleted), Reason: reason}, nil
}

func (m *Module) releaseTask(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	return m.releaseTaskToStatus(ctx, ws, id, body, execution.DriverRunWorkItemRestoreOpen)
}

func (m *Module) releaseReview(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	return m.releaseTaskToStatus(ctx, ws, id, body, execution.DriverRunWorkItemRestoreReview)
}

type handoffReviewParams struct {
	TaskID      string    `json:"taskId"`
	TaskRunID   string    `json:"taskRunId"`
	Status      string    `json:"status"`
	Reason      string    `json:"reason"`
	Priority    *int      `json:"priority"`
	Labels      *[]string `json:"labels"`
	CommentBody *string   `json:"commentBody"`
}

type handoffReviewFieldPresence struct {
	priority    bool
	labels      bool
	commentBody bool
}

type handoffReviewRequest struct {
	taskID       string
	taskRunID    string
	targetStatus string
	reason       string
	priority     *int
	labels       []string
	commentBody  string
}

func decodeHandoffReviewParams(body []byte) (handoffReviewParams, handoffReviewFieldPresence, error) {
	params, err := decodeParams[handoffReviewParams](body)
	if err != nil {
		return handoffReviewParams{}, handoffReviewFieldPresence{}, err
	}
	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal(body, &rawFields); err != nil {
		return handoffReviewParams{}, handoffReviewFieldPresence{},
			fmt.Errorf("decode handoff review field presence: %w", domain.ErrInvalid)
	}
	_, priorityProvided := rawFields["priority"]
	_, labelsProvided := rawFields["labels"]
	_, commentBodyProvided := rawFields["commentBody"]
	return params, handoffReviewFieldPresence{
		priority: priorityProvided, labels: labelsProvided, commentBody: commentBodyProvided,
	}, nil
}

func normalizeHandoffReviewRequest(params handoffReviewParams) handoffReviewRequest {
	request := handoffReviewRequest{
		taskID: strings.TrimSpace(params.TaskID), taskRunID: strings.TrimSpace(params.TaskRunID),
		targetStatus: strings.TrimSpace(params.Status), reason: strings.TrimSpace(params.Reason),
		priority: params.Priority,
	}
	if params.Labels != nil {
		request.labels = append([]string{}, (*params.Labels)...)
	}
	if params.CommentBody != nil {
		request.commentBody = *params.CommentBody
	}
	return request
}

func validateHandoffReviewRequest(request handoffReviewRequest, fields handoffReviewFieldPresence) error {
	if request.taskID == "" || request.taskRunID == "" || !validHandoffReviewTargetStatus(request.targetStatus) {
		return fmt.Errorf("taskId, taskRunId, and status open, review, or closed are required: %w", domain.ErrInvalid)
	}
	if request.targetStatus == execution.DriverRunWorkItemRestoreReview {
		if !validHandoffReviewAnnotations(request, fields) {
			return fmt.Errorf("review status requires priority 0 through 4 and nonblank commentBody: %w", domain.ErrInvalid)
		}
		return nil
	}
	if fields.priority || fields.labels || fields.commentBody {
		return fmt.Errorf("priority, labels, and commentBody are only valid for review status: %w", domain.ErrInvalid)
	}
	return nil
}

func validHandoffReviewTargetStatus(status string) bool {
	return status == execution.DriverRunWorkItemRestoreOpen ||
		status == execution.DriverRunWorkItemRestoreReview ||
		status == "closed"
}

func validHandoffReviewAnnotations(request handoffReviewRequest, fields handoffReviewFieldPresence) bool {
	return fields.priority &&
		request.priority != nil &&
		*request.priority >= execution.DriverRunReviewWorkItemPriorityMin &&
		*request.priority <= execution.DriverRunReviewWorkItemPriorityMax &&
		fields.commentBody &&
		strings.TrimSpace(request.commentBody) != "" &&
		(!fields.labels || request.labels != nil)
}

func (m *Module) handoffReview(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, fields, err := decodeHandoffReviewParams(body)
	if err != nil {
		return nil, err
	}
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	request := normalizeHandoffReviewRequest(params)
	if err := validateHandoffReviewRequest(request, fields); err != nil {
		return nil, err
	}
	if m.execution == nil || m.executionAuthorities == nil {
		return nil, fmt.Errorf("execution DriverRun review handoff capability is unavailable: %w", execution.ErrUnavailable)
	}
	owner, err := driverRunExecutionOwner(id, parent.RunID)
	if err != nil {
		return nil, err
	}
	auth, err := m.executionAuthorities.ResolveDriverRunAuthority(
		ctx, ws, execution.ActionHandoffDriverRunReviewWorkItem, owner,
	)
	if err != nil {
		return nil, fmt.Errorf("resolve DriverRun review handoff authority: %w", err)
	}
	claimRequestID := execution.ClaimDriverRunWorkItemRequestID(parent.RunID, request.taskID)
	result, err := m.execution.HandoffDriverRunReviewWorkItem(ctx, auth, execution.HandoffDriverRunReviewWorkItemCommand{
		WorkspaceKey:  ws,
		RequestID:     execution.HandoffDriverRunReviewWorkItemRequestID(parent.RunID, request.taskID, request.taskRunID),
		Owner:         owner,
		WorkItemID:    request.taskID,
		ClaimActionID: execution.DriverRunWorkItemClaimActionID(claimRequestID),
		TaskRunID:     request.taskRunID,
		TargetStatus:  request.targetStatus,
		Reason:        request.reason,
		Priority:      request.priority,
		Labels:        request.labels,
		CommentBody:   request.commentBody,
		HandedOffAt:   time.Now().UTC(),
	})
	if err != nil {
		return nil, fmt.Errorf("handoff review task: %w", err)
	}
	if result.WorkItem == nil {
		return nil, fmt.Errorf("handoff review task returned no Work Item: %w", execution.ErrConflict)
	}
	return &driverpkg.TaskMutationResult{
		ID: request.taskID, Status: result.WorkItem.Status, Released: true, Reason: request.reason,
	}, nil
}

func (m *Module) releaseTaskToStatus(
	ctx context.Context,
	ws string,
	id driverIdentity,
	body []byte,
	restoreStatus string,
) (any, error) {
	params, err := decodeParams[struct {
		TaskID string `json:"taskId"`
		// Actor: accepted for wire-compat, IGNORED. SECURITY: the release
		// ownership check is keyed by the server-derived run actor below, never
		// by caller input — otherwise a run could present a victim's actor and
		// release a lock it never held (cross-agent task theft). The claim path
		// derives the SAME actor from the SAME run, so a run releases exactly
		// the leases it took; cross-run recovery relies on lock TTL, not on a
		// caller-supplied actor.
		Actor string `json:"actor"`
	}](body)
	if err != nil {
		return nil, err
	}
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(params.TaskID) == "" {
		return nil, fmt.Errorf("taskId required: %w", domain.ErrInvalid)
	}
	if m.execution == nil || m.executionAuthorities == nil {
		return nil, fmt.Errorf("execution DriverRun Work Item release capability is unavailable: %w", execution.ErrUnavailable)
	}
	owner, err := driverRunExecutionOwner(id, parent.RunID)
	if err != nil {
		return nil, err
	}
	auth, err := m.executionAuthorities.ResolveDriverRunAuthority(ctx, ws, execution.ActionReleaseDriverRunWorkItem, owner)
	if err != nil {
		return nil, fmt.Errorf("resolve DriverRun Work Item release authority: %w", err)
	}
	claimRequestID := execution.ClaimDriverRunWorkItemRequestID(parent.RunID, params.TaskID)
	_, err = m.execution.ReleaseDriverRunWorkItem(ctx, auth, execution.ReleaseDriverRunWorkItemCommand{
		WorkspaceKey: ws,
		RequestID:    execution.ReleaseDriverRunWorkItemRequestID(parent.RunID, params.TaskID),
		Owner:        owner, WorkItemID: params.TaskID,
		ClaimActionID: execution.DriverRunWorkItemClaimActionID(claimRequestID),
		RestoreStatus: restoreStatus, ReleasedAt: time.Now().UTC(),
	})
	if err != nil {
		return nil, fmt.Errorf("release task: %w", err)
	}
	return &driverpkg.TaskMutationResult{ID: params.TaskID, Released: true}, nil
}
