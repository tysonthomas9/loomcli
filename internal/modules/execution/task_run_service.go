package execution

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"math/big"
	"slices"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func (service *Service) RequestTaskRun(ctx context.Context, auth authority.ExecutionAuthority, command RequestTaskRunCommand) (*TaskRun, error) {
	if err := service.requireOwner(ActionRequestTaskRun, command.WorkspaceKey, command.ParentOwner, auth); err != nil {
		return nil, err
	}
	command = prepareRequestTaskRunCommand(command)
	if !validRequestTaskRunCommand(command) {
		return nil, ErrInvalid
	}
	port := service.dependencies.TaskRuns.Requests
	if port == nil {
		return nil, ErrUnavailable
	}
	// Probe the durable idempotency receipt before live scheduling. A request
	// may have committed and lost its response before worker availability
	// changed; that retry must observe the committed child instead of being
	// rejected as a new unschedulable request.
	if replayed, found, err := replayRequestedTaskRun(ctx, port, command); err != nil || found {
		return replayed, err
	}
	if err := ensureTaskRunSchedulable(ctx, service.dependencies.TaskRuns.Scheduling, command); err != nil {
		return nil, err
	}
	result, err := port.RequestTaskRun(ctx, cloneRequestTaskRunCommand(command))
	if err != nil {
		return nil, err
	}
	if err := validateRequestedTaskRunResult(result, command); err != nil {
		// The authoritative port may have committed before returning a malformed
		// response. Classify this as ambiguous so callers retain the parent claim
		// and resolve it through the durable replay receipt instead of issuing a
		// compensating release against a possibly-live child.
		return nil, fmt.Errorf("%w: TaskRun request returned an invalid post-commit receipt", ErrUnavailable)
	}
	return cloneTaskRun(result.Run), nil
}

func prepareRequestTaskRunCommand(command RequestTaskRunCommand) RequestTaskRunCommand {
	if strings.TrimSpace(command.DriverStepID) == "" && strings.TrimSpace(command.DriverRunID) != "" && strings.TrimSpace(command.TaskRunID) != "" {
		command.DriverStepID = RequestedDriverStepID(command.DriverRunID, command.TaskRunID)
	}
	return command
}

func validRequestTaskRunCommand(command RequestTaskRunCommand) bool {
	return command.ParentOwner.ResourceKind == ResourceDriverRun &&
		command.RequestID == RequestTaskRunRequestID(command.DriverRunID, command.TaskRunID) && strings.TrimSpace(command.TaskRunID) != "" &&
		strings.TrimSpace(command.DriverRunID) != "" && command.DriverRunID == command.ParentOwner.ResourceID &&
		strings.TrimSpace(command.DriverStepID) != "" && strings.TrimSpace(command.WorkItemID) != "" &&
		command.ClaimActionID == DriverRunWorkItemClaimActionID(ClaimDriverRunWorkItemRequestID(command.DriverRunID, command.WorkItemID)) &&
		!command.RequestedAt.IsZero() && (len(command.Input) == 0 || json.Valid(command.Input))
}

func replayRequestedTaskRun(ctx context.Context, port TaskRunRequestPort, command RequestTaskRunCommand) (*TaskRun, bool, error) {
	replayed, err := port.ReplayTaskRunRequest(ctx, cloneRequestTaskRunCommand(command))
	if errors.Is(err, ErrTaskRunRequestReplayNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !replayed.Replay {
		return nil, false, fmt.Errorf("%w: TaskRun replay probe returned an uncertified result", ErrConflict)
	}
	if err := validateRequestedTaskRunResult(replayed, command); err != nil {
		return nil, false, fmt.Errorf("%w: requested TaskRun replay escaped parent envelope", ErrConflict)
	}
	return cloneTaskRun(replayed.Run), true, nil
}

func ensureTaskRunSchedulable(ctx context.Context, port TaskRunSchedulingQueryPort, command RequestTaskRunCommand) error {
	if port == nil {
		return ErrUnavailable
	}
	provider := ""
	if !requestTaskRunHasNamedRunner(command) {
		provider = strings.TrimSpace(command.SandboxPlacement.Provider)
		if provider == "" {
			provider = strings.TrimSpace(command.ProviderProfile)
		}
	}
	scheduling, err := port.CheckTaskRunScheduling(ctx, TaskRunSchedulingQuery{
		WorkspaceKey: command.WorkspaceKey, TargetNodeID: command.TargetNodeID,
		WorkerProfileID: command.WorkerProfileID, ProviderProfile: provider,
		RequiredFeatures: append([]string(nil), command.RequiredCapabilities...),
	})
	if err != nil {
		return err
	}
	if !scheduling.Schedulable {
		return fmt.Errorf("%w: %s", ErrUnschedulable, strings.TrimSpace(scheduling.ReasonCode))
	}
	return nil
}

func validateRequestedTaskRunResult(result RequestTaskRunResult, command RequestTaskRunCommand) error {
	run := result.Run
	if run == nil || run.WorkspaceKey != command.WorkspaceKey || run.TaskRunID != command.TaskRunID ||
		run.DriverRunID != command.DriverRunID || run.DriverStepID != command.DriverStepID ||
		run.WorkItemID != command.WorkItemID || strings.TrimSpace(result.ActionID) == "" ||
		result.ClaimActionID != command.ClaimActionID {
		return ErrConflict
	}
	if !result.Replay {
		if run.Status != StatusQueued || !requestedTaskRunPayloadMatches(run, command) ||
			validateTaskRunDriverStep(result.Step, command.WorkspaceKey, command.DriverStepID, command.DriverRunID, command.TaskRunID, "queued") != nil {
			return ErrConflict
		}
		return nil
	}
	// FleetDB replays the immutable queued TaskRun + DriverStep receipt even
	// when the live lifecycle has advanced. Validate that original snapshot,
	// never infer historical proof from today's mutable projections.
	if run.Status != StatusQueued || !requestedTaskRunPayloadMatches(run, command) ||
		validateTaskRunDriverStep(result.Step, command.WorkspaceKey, command.DriverStepID, command.DriverRunID, command.TaskRunID, "queued") != nil {
		return ErrConflict
	}
	return nil
}

func requestedTaskRunPayloadMatches(run *TaskRun, command RequestTaskRunCommand) bool {
	if run.WorkerProfileID != command.WorkerProfileID || run.Runner != command.Runner || run.RunnerRef != command.RunnerRef ||
		run.RunnerKind != command.RunnerKind || run.RunnerEntrypoint != command.RunnerEntrypoint ||
		run.RunnerVersionID != command.RunnerVersionID || run.ProviderProfile != command.ProviderProfile ||
		run.TargetNodeID != command.TargetNodeID || run.RunnerPlacement != command.RunnerPlacement ||
		run.SandboxPlacement != command.SandboxPlacement || !taskRunJSONInputEqual(run.Input, command.Input) {
		return false
	}
	wantMetadata := cloneExecutionStringMap(command.RuntimeMetadata)
	if wantMetadata == nil {
		wantMetadata = map[string]string{}
	}
	wantMetadata["execution_request_id"] = command.RequestID
	return maps.Equal(run.RuntimeMetadata, wantMetadata)
}

// taskRunJSONInputEqual compares the JSON value rather than its wire bytes.
// The TaskRun request crosses Loom -> FleetDB -> Loom before this receipt is
// validated. encoding/json normalizes insignificant whitespace and string
// escapes, while FleetDB's PostgreSQL immutable receipt is JSONB and may also
// reorder object keys. Semantic equality matches both certified backends;
// UseNumber keeps distinct large integer values from collapsing through
// float64 during comparison.
func taskRunJSONInputEqual(left, right json.RawMessage) bool {
	if len(left) == 0 || len(right) == 0 {
		return len(left) == len(right)
	}
	if !json.Valid(left) || !json.Valid(right) {
		return false
	}
	decode := func(raw json.RawMessage) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		return value, nil
	}
	leftValue, err := decode(left)
	if err != nil {
		return false
	}
	rightValue, err := decode(right)
	return err == nil && taskRunJSONValueEqual(leftValue, rightValue)
}

func taskRunJSONValueEqual(left, right any) bool {
	switch left := left.(type) {
	case nil:
		return right == nil
	case bool:
		right, ok := right.(bool)
		return ok && left == right
	case string:
		right, ok := right.(string)
		return ok && left == right
	case json.Number:
		return taskRunJSONNumberEqual(left, right)
	case []any:
		return taskRunJSONArrayEqual(left, right)
	case map[string]any:
		return taskRunJSONObjectEqual(left, right)
	default:
		return false
	}
}

func taskRunJSONNumberEqual(left json.Number, right any) bool {
	rightNumberText, ok := right.(json.Number)
	if !ok {
		return false
	}
	leftNumber, leftOK := new(big.Rat).SetString(string(left))
	rightNumber, rightOK := new(big.Rat).SetString(string(rightNumberText))
	return leftOK && rightOK && leftNumber.Cmp(rightNumber) == 0
}

func taskRunJSONArrayEqual(left []any, right any) bool {
	rightArray, ok := right.([]any)
	if !ok || len(left) != len(rightArray) {
		return false
	}
	for i := range left {
		if !taskRunJSONValueEqual(left[i], rightArray[i]) {
			return false
		}
	}
	return true
}

func taskRunJSONObjectEqual(left map[string]any, right any) bool {
	rightObject, ok := right.(map[string]any)
	if !ok || len(left) != len(rightObject) {
		return false
	}
	for key, value := range left {
		rightValue, ok := rightObject[key]
		if !ok || !taskRunJSONValueEqual(value, rightValue) {
			return false
		}
	}
	return true
}

func taskRunStepMatchesLifecycle(run *TaskRun, step *TaskRunDriverStep, workspace, stepID, driverRunID, taskRunID string) bool {
	if run == nil {
		return false
	}
	if stepID == "" {
		return step == nil
	}
	if step == nil || step.WorkspaceKey != workspace || step.StepID != stepID ||
		step.DriverRunID != driverRunID || step.TaskRunID != taskRunID {
		return false
	}
	switch run.Status {
	case StatusQueued:
		return step.Status == "queued"
	case StatusRunning:
		return step.Status == "running"
	case StatusSucceeded:
		return step.Status == "running" || step.Status == "completed"
	case StatusFailed:
		return step.Status == "running" || step.Status == "failed"
	case StatusCancelled:
		return step.Status == "queued" || step.Status == "running" || step.Status == "skipped"
	default:
		return false
	}
}

func validateRequeueTaskRunResult(result RequeueTaskRunResult, command RequeueTaskRunCommand) error {
	run := result.Run
	if run == nil || run.WorkspaceKey != command.WorkspaceKey || run.TaskRunID != command.Owner.ResourceID ||
		strings.TrimSpace(result.ActionID) == "" || !requeueTaskRunCommitMatches(result.Committed, command, run, result.Replay) {
		return ErrConflict
	}
	if !result.Replay {
		if run.Status != StatusQueued ||
			(run.DriverStepID != "" && validateTaskRunDriverStep(result.Step, command.WorkspaceKey, run.DriverStepID, run.DriverRunID, run.TaskRunID, "queued") != nil) {
			return ErrConflict
		}
		return nil
	}
	if !taskRunStepMatchesLifecycle(run, result.Step, command.WorkspaceKey, run.DriverStepID, run.DriverRunID, run.TaskRunID) {
		return ErrConflict
	}
	return nil
}

func requeueTaskRunCommitMatches(commit *RequeueTaskRunCommit, command RequeueTaskRunCommand, run *TaskRun, replay bool) bool { //nolint:cyclop // This is one declarative durable-receipt equality contract.
	if commit == nil || run == nil || commit.WorkspaceKey != command.WorkspaceKey ||
		commit.TaskRunID != command.Owner.ResourceID || commit.TaskRunID != run.TaskRunID ||
		commit.DriverRunID != run.DriverRunID || commit.DriverStepID != run.DriverStepID ||
		commit.WorkItemID != run.WorkItemID || commit.TaskRunStatus != StatusQueued ||
		(commit.DriverStepID != "" && commit.DriverStepStatus != "queued") ||
		(commit.DriverStepID == "" && commit.DriverStepStatus != "") ||
		!maps.Equal(commit.RuntimeMetadata, command.RuntimeMetadata) || commit.LogsRef != command.LogsRef ||
		commit.ArtifactsRef != command.ArtifactsRef || commit.ErrorClass != command.ErrorClass ||
		commit.ErrorMessage != command.ErrorMessage || (!replay && !commit.RequeuedAt.Equal(command.RequeuedAt)) ||
		(replay && commit.RequeuedAt.IsZero()) ||
		!commit.NextEligibleAt.Equal(command.NextEligibleAt) {
		return false
	}
	return true
}

func validateExhaustTaskRunRetriesResult(result ExhaustTaskRunRetriesResult, command ExhaustTaskRunRetriesCommand) error {
	if result.Run == nil || result.Run.WorkspaceKey != command.WorkspaceKey ||
		result.Run.TaskRunID != command.Owner.ResourceID || result.Run.Status != StatusFailed ||
		strings.TrimSpace(result.WorkItemID) == "" || result.Run.WorkItemID != result.WorkItemID ||
		strings.TrimSpace(result.ActionID) == "" || !exhaustTaskRunRetriesCommitMatches(result.Committed, command, result.Run, result.Replay) {
		return ErrConflict
	}
	// When this command reports that it applied the exact-generation block, the
	// first response must already observe that projection. A preserved-successor
	// outcome may legitimately be non-blocked (or absent), and replay always
	// relies on the immutable commit because the current Issue can later move.
	if !result.Replay && result.Committed.WorkItemBlocked && !result.WorkItemBlocked {
		return ErrConflict
	}
	return nil
}

func exhaustTaskRunRetriesCommitMatches(commit *ExhaustTaskRunRetriesCommit, command ExhaustTaskRunRetriesCommand, run *TaskRun, replay bool) bool { //nolint:cyclop // This is one declarative durable-receipt equality contract.
	if commit == nil || run == nil || commit.WorkspaceKey != command.WorkspaceKey ||
		commit.TaskRunID != command.Owner.ResourceID || commit.TaskRunID != run.TaskRunID ||
		commit.WorkItemID != run.WorkItemID || commit.TaskRunStatus != StatusFailed ||
		commit.Attempt != command.Attempt || commit.MaxAttempts != command.MaxAttempts ||
		!optionalIntEqual(commit.ExitCode, command.ExitCode) || commit.LogsRef != command.LogsRef ||
		commit.ArtifactsRef != command.ArtifactsRef || !slices.Equal(commit.RequiredArtifactIDs, command.RequiredArtifactIDs) ||
		commit.RequireArtifacts != command.RequireArtifacts || commit.InputTokens != command.InputTokens ||
		commit.OutputTokens != command.OutputTokens || commit.CacheReadTokens != command.CacheReadTokens ||
		commit.CacheWriteTokens != command.CacheWriteTokens || commit.EstimatedCostUSD != command.EstimatedCostUSD ||
		!maps.Equal(commit.RuntimeMetadata, command.RuntimeMetadata) || commit.ErrorClass != command.ErrorClass ||
		commit.ErrorMessage != command.ErrorMessage || (!replay && !commit.FinishedAt.Equal(command.FinishedAt)) ||
		(replay && commit.FinishedAt.IsZero()) {
		return false
	}
	return true
}

func optionalIntEqual(left, right *int) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func canonicalArtifactIDs(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	slices.Sort(out)
	return out
}

func validTaskRunUsageValues(inputTokens, outputTokens, cacheReadTokens, cacheWriteTokens int64, estimatedCostUSD float64) bool {
	return inputTokens >= 0 && outputTokens >= 0 && cacheReadTokens >= 0 && cacheWriteTokens >= 0 &&
		estimatedCostUSD >= 0 && !math.IsNaN(estimatedCostUSD) && !math.IsInf(estimatedCostUSD, 0)
}

func (service *Service) ClaimTaskRun(ctx context.Context, auth authority.SystemAuthority, command ClaimTaskRunCommand) (ClaimTaskRunResult, error) {
	if err := service.requireSystem(ActionClaimTaskRun, command.WorkspaceKey, auth); err != nil {
		return ClaimTaskRunResult{}, err
	}
	if strings.TrimSpace(command.RequestID) == "" || strings.TrimSpace(command.NodeID) == "" ||
		strings.TrimSpace(command.LeaseID) == "" || strings.TrimSpace(command.LeaseToken) == "" ||
		command.LeaseTTL <= 0 || command.ClaimedAt.IsZero() {
		return ClaimTaskRunResult{}, ErrInvalid
	}
	port := service.dependencies.TaskRuns.Claims
	if port == nil {
		return ClaimTaskRunResult{}, ErrUnavailable
	}
	result, err := port.ClaimTaskRun(ctx, cloneClaimTaskRunCommand(command))
	if err != nil {
		return ClaimTaskRunResult{}, err
	}
	if err := validateClaimedTaskRun(command, result.Run); err != nil {
		return ClaimTaskRunResult{}, err
	}
	if strings.TrimSpace(result.ActionID) == "" ||
		(result.Run.DriverStepID != "" && validateTaskRunDriverStep(result.Step, command.WorkspaceKey, result.Run.DriverStepID, result.Run.DriverRunID, result.Run.TaskRunID, "running") != nil) {
		return ClaimTaskRunResult{}, fmt.Errorf("%w: claim did not atomically project its linked DriverStep", ErrConflict)
	}
	result.Run = cloneTaskRun(result.Run)
	result.Step = cloneTaskRunDriverStep(result.Step)
	result.Run.Owner = publicOwner(result.Run.Owner)
	return result, nil
}

// UpdateWorkItemDesign delegates the one Work Item mutation admitted to a
// running TaskRun. The authoritative port resolves the Work Item from the
// fenced TaskRun; neither this command nor its caller can select another ID.
func (service *Service) UpdateWorkItemDesign(
	ctx context.Context,
	auth authority.ExecutionAuthority,
	command UpdateTaskRunWorkItemDesignCommand,
) (UpdateTaskRunWorkItemDesignResult, error) {
	if err := service.requireOwner(ActionUpdateTaskRunWorkItemDesign, command.WorkspaceKey, command.Owner, auth); err != nil {
		return UpdateTaskRunWorkItemDesignResult{}, err
	}
	if command.Owner.ResourceKind != ResourceTaskRun || strings.TrimSpace(command.RequestID) == "" ||
		command.Design == nil || strings.TrimSpace(*command.Design) == "" {
		return UpdateTaskRunWorkItemDesignResult{}, ErrInvalid
	}
	format := "markdown"
	if command.DesignFormat != nil && strings.TrimSpace(*command.DesignFormat) != "" {
		format = strings.TrimSpace(*command.DesignFormat)
	}
	if format != "markdown" && format != "html" {
		return UpdateTaskRunWorkItemDesignResult{}, ErrInvalid
	}
	command.DesignFormat = &format
	port := service.dependencies.TaskRuns.WorkItemDesign
	if port == nil {
		return UpdateTaskRunWorkItemDesignResult{}, ErrUnavailable
	}
	result, err := port.UpdateTaskRunWorkItemDesign(ctx, cloneUpdateTaskRunWorkItemDesignCommand(command))
	if err != nil {
		return UpdateTaskRunWorkItemDesignResult{}, err
	}
	if strings.TrimSpace(result.WorkItemID) == "" || result.WorkItemID != strings.TrimSpace(result.WorkItemID) ||
		result.ActionID != TaskRunWorkItemDesignActionID(command.RequestID) {
		return UpdateTaskRunWorkItemDesignResult{}, fmt.Errorf("%w: Work Item design update returned an invalid receipt", ErrConflict)
	}
	return result, nil
}

func (service *Service) RequeueTaskRun(ctx context.Context, auth authority.ExecutionAuthority, command RequeueTaskRunCommand) (RequeueTaskRunResult, error) {
	if err := service.requireOwner(ActionRequeueTaskRun, command.WorkspaceKey, command.Owner, auth); err != nil {
		return RequeueTaskRunResult{}, err
	}
	if command.Owner.ResourceKind != ResourceTaskRun || strings.TrimSpace(command.RequestID) == "" || command.RequeuedAt.IsZero() {
		return RequeueTaskRunResult{}, ErrInvalid
	}
	port := service.dependencies.TaskRuns.Requeues
	if port == nil {
		return RequeueTaskRunResult{}, ErrUnavailable
	}
	result, err := port.RequeueTaskRun(ctx, cloneRequeueTaskRunCommand(command))
	if err != nil {
		return RequeueTaskRunResult{}, err
	}
	if err := validateRequeueTaskRunResult(result, command); err != nil {
		return RequeueTaskRunResult{}, fmt.Errorf("%w: requeued TaskRun escaped requested envelope", ErrConflict)
	}
	result.Run = cloneTaskRun(result.Run)
	result.Step = cloneTaskRunDriverStep(result.Step)
	result.Committed = cloneRequeueTaskRunCommit(result.Committed)
	result.Run.Owner = Owner{}
	return result, nil
}

func (service *Service) ExhaustTaskRunRetries(ctx context.Context, auth authority.ExecutionAuthority, command ExhaustTaskRunRetriesCommand) (ExhaustTaskRunRetriesResult, error) {
	if err := service.requireOwner(ActionExhaustTaskRunRetries, command.WorkspaceKey, command.Owner, auth); err != nil {
		return ExhaustTaskRunRetriesResult{}, err
	}
	command.RequiredArtifactIDs = canonicalArtifactIDs(command.RequiredArtifactIDs)
	if command.Owner.ResourceKind != ResourceTaskRun || strings.TrimSpace(command.RequestID) == "" ||
		command.MaxAttempts <= 0 || command.Attempt < command.MaxAttempts || command.FinishedAt.IsZero() ||
		strings.TrimSpace(command.ErrorClass) == "" || strings.TrimSpace(command.ErrorMessage) == "" ||
		!validTaskRunUsageValues(command.InputTokens, command.OutputTokens, command.CacheReadTokens,
			command.CacheWriteTokens, command.EstimatedCostUSD) {
		return ExhaustTaskRunRetriesResult{}, ErrInvalid
	}
	port := service.dependencies.TaskRuns.RetryExhaustion
	if port == nil {
		return ExhaustTaskRunRetriesResult{}, ErrUnavailable
	}
	result, err := port.ExhaustTaskRunRetries(ctx, cloneExhaustTaskRunRetriesCommand(command))
	if err != nil {
		return ExhaustTaskRunRetriesResult{}, err
	}
	if err := validateExhaustTaskRunRetriesResult(result, command); err != nil {
		return ExhaustTaskRunRetriesResult{}, fmt.Errorf("%w: retry exhaustion did not prove TaskRun failure and its exact-generation Work Item outcome", ErrConflict)
	}
	result.Run = cloneTaskRun(result.Run)
	result.Committed = cloneExhaustTaskRunRetriesCommit(result.Committed)
	result.Run.Owner = Owner{}
	return result, nil
}

func (service *Service) RegisterWorkerNode(ctx context.Context, auth authority.SystemAuthority, command RegisterWorkerNodeCommand) (*WorkerNode, error) {
	command = cloneRegisterWorkerNodeCommand(command)
	if err := service.requireSystem(ActionRegisterWorkerNode, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if strings.TrimSpace(command.RequestID) == "" || strings.TrimSpace(command.NodeID) == "" ||
		strings.TrimSpace(command.OwnerActor) == "" || strings.TrimSpace(command.RuntimeProvider) == "" ||
		command.TTL <= 0 || command.RegisteredAt.IsZero() {
		return nil, ErrInvalid
	}
	port := service.dependencies.Workers.Registration
	if port == nil {
		return nil, ErrUnavailable
	}
	node, err := port.RegisterWorkerNode(ctx, command)
	if err != nil {
		return nil, err
	}
	if err := validateWorkerNode(command.WorkspaceKey, command.NodeID, node); err != nil {
		return nil, err
	}
	return cloneWorkerNode(node), nil
}

func (service *Service) HeartbeatWorkerNode(ctx context.Context, auth authority.SystemAuthority, command HeartbeatWorkerNodeCommand) (*WorkerNode, error) {
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.RequestID = strings.TrimSpace(command.RequestID)
	command.NodeID = strings.TrimSpace(command.NodeID)
	if err := service.requireSystem(ActionHeartbeatWorkerNode, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if strings.TrimSpace(command.RequestID) == "" || strings.TrimSpace(command.NodeID) == "" || command.TTL <= 0 || command.HeartbeatAt.IsZero() {
		return nil, ErrInvalid
	}
	port := service.dependencies.Workers.Heartbeats
	if port == nil {
		return nil, ErrUnavailable
	}
	node, err := port.HeartbeatWorkerNode(ctx, command)
	if err != nil {
		return nil, err
	}
	if err := validateWorkerNode(command.WorkspaceKey, command.NodeID, node); err != nil {
		return nil, err
	}
	return cloneWorkerNode(node), nil
}

func (service *Service) SetWorkerNodeDrain(ctx context.Context, auth authority.SystemAuthority, command SetWorkerNodeDrainCommand) (*WorkerNode, error) {
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.RequestID = strings.TrimSpace(command.RequestID)
	command.NodeID = strings.TrimSpace(command.NodeID)
	if err := service.requireSystem(ActionSetWorkerNodeDrain, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if strings.TrimSpace(command.RequestID) == "" || strings.TrimSpace(command.NodeID) == "" ||
		!validWorkerNodeDrain(command.DrainState) || command.ChangedAt.IsZero() {
		return nil, ErrInvalid
	}
	port := service.dependencies.Workers.Drain
	if port == nil {
		return nil, ErrUnavailable
	}
	node, err := port.SetWorkerNodeDrain(ctx, command)
	if err != nil {
		return nil, err
	}
	if err := validateWorkerNode(command.WorkspaceKey, command.NodeID, node); err != nil {
		return nil, err
	}
	if node.DrainState != command.DrainState {
		return nil, fmt.Errorf("%w: worker node drain state did not converge", ErrConflict)
	}
	return cloneWorkerNode(node), nil
}

func (service *Service) CheckTaskRunScheduling(ctx context.Context, query TaskRunSchedulingQuery) (TaskRunSchedulingResult, error) {
	if strings.TrimSpace(query.WorkspaceKey) == "" {
		return TaskRunSchedulingResult{}, ErrInvalid
	}
	port := service.dependencies.TaskRuns.Scheduling
	if port == nil {
		return TaskRunSchedulingResult{}, ErrUnavailable
	}
	return port.CheckTaskRunScheduling(ctx, cloneSchedulingQuery(query))
}

func validateClaimedTaskRun(command ClaimTaskRunCommand, run *TaskRun) error {
	if run == nil || run.WorkspaceKey != command.WorkspaceKey || run.Status != StatusRunning ||
		run.Owner.ResourceKind != ResourceTaskRun || strings.TrimSpace(run.TaskRunID) == "" ||
		run.Owner.ResourceID != run.TaskRunID || run.Owner.NodeID != command.NodeID ||
		run.Owner.LeaseID != command.LeaseID || run.Owner.LeaseToken != command.LeaseToken || run.Owner.FencingToken <= 0 ||
		(strings.TrimSpace(command.TaskRunID) != "" && run.TaskRunID != command.TaskRunID) {
		return fmt.Errorf("%w: claimed TaskRun escaped requested owner envelope", ErrConflict)
	}
	return nil
}

func validateWorkerNode(workspace, nodeID string, node *WorkerNode) error {
	if node == nil || node.WorkspaceKey != workspace || node.NodeID != nodeID || !validWorkerNodeDrain(node.DrainState) {
		return fmt.Errorf("%w: worker node escaped requested envelope", ErrConflict)
	}
	return nil
}

func validateTaskRunDriverStep(step *TaskRunDriverStep, workspace, stepID, driverRunID, taskRunID, status string) error {
	if step == nil || step.WorkspaceKey != workspace || step.StepID != stepID || step.DriverRunID != driverRunID ||
		step.TaskRunID != taskRunID || step.Status != status {
		return ErrConflict
	}
	return nil
}

func validWorkerNodeDrain(state WorkerNodeDrainState) bool {
	switch state {
	case WorkerNodeActive, WorkerNodeDraining, WorkerNodeDrained:
		return true
	default:
		return false
	}
}

func cloneRequestTaskRunCommand(command RequestTaskRunCommand) RequestTaskRunCommand {
	command.RuntimeMetadata = cloneExecutionStringMap(command.RuntimeMetadata)
	command.RequiredCapabilities = append([]string(nil), command.RequiredCapabilities...)
	command.Input = append(json.RawMessage(nil), command.Input...)
	return command
}

func requestTaskRunHasNamedRunner(command RequestTaskRunCommand) bool {
	return strings.TrimSpace(command.Runner) != "" || strings.TrimSpace(command.RunnerRef) != "" ||
		strings.TrimSpace(command.RunnerKind) != "" || strings.TrimSpace(command.RunnerEntrypoint) != ""
}

func cloneClaimTaskRunCommand(command ClaimTaskRunCommand) ClaimTaskRunCommand {
	command.SupportedProviders = append([]string(nil), command.SupportedProviders...)
	command.Capabilities = append([]string(nil), command.Capabilities...)
	command.WorkerProfileIDs = append([]string(nil), command.WorkerProfileIDs...)
	return command
}

func cloneUpdateTaskRunWorkItemDesignCommand(command UpdateTaskRunWorkItemDesignCommand) UpdateTaskRunWorkItemDesignCommand {
	if command.Design != nil {
		design := *command.Design
		command.Design = &design
	}
	if command.DesignFormat != nil {
		format := *command.DesignFormat
		command.DesignFormat = &format
	}
	return command
}

func cloneRequeueTaskRunCommand(command RequeueTaskRunCommand) RequeueTaskRunCommand {
	command.RuntimeMetadata = cloneExecutionStringMap(command.RuntimeMetadata)
	return command
}

func cloneExhaustTaskRunRetriesCommand(command ExhaustTaskRunRetriesCommand) ExhaustTaskRunRetriesCommand {
	command.RequiredArtifactIDs = append([]string(nil), command.RequiredArtifactIDs...)
	command.RuntimeMetadata = cloneExecutionStringMap(command.RuntimeMetadata)
	return command
}

func cloneRegisterWorkerNodeCommand(command RegisterWorkerNodeCommand) RegisterWorkerNodeCommand {
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.RequestID = strings.TrimSpace(command.RequestID)
	command.NodeID = strings.TrimSpace(command.NodeID)
	command.OwnerActor = strings.TrimSpace(command.OwnerActor)
	command.RuntimeProvider = strings.TrimSpace(command.RuntimeProvider)
	command.Version = strings.TrimSpace(command.Version)
	command.Labels = append([]string(nil), command.Labels...)
	command.Capabilities = append([]string(nil), command.Capabilities...)
	command.ToolInventory = append([]string(nil), command.ToolInventory...)
	return command
}

func cloneSchedulingQuery(query TaskRunSchedulingQuery) TaskRunSchedulingQuery {
	query.RequiredFeatures = append([]string(nil), query.RequiredFeatures...)
	return query
}

func cloneTaskRun(run *TaskRun) *TaskRun {
	if run == nil {
		return nil
	}
	cloned := *run
	cloned.Owner = publicOwner(run.Owner)
	cloned.RuntimeMetadata = cloneExecutionStringMap(run.RuntimeMetadata)
	cloned.Input = append(json.RawMessage(nil), run.Input...)
	return &cloned
}

func cloneWorkerNode(node *WorkerNode) *WorkerNode {
	if node == nil {
		return nil
	}
	cloned := *node
	cloned.Labels = append([]string(nil), node.Labels...)
	cloned.Capabilities = append([]string(nil), node.Capabilities...)
	cloned.ToolInventory = append([]string(nil), node.ToolInventory...)
	return &cloned
}

func cloneTaskRunDriverStep(step *TaskRunDriverStep) *TaskRunDriverStep {
	if step == nil {
		return nil
	}
	cloned := *step
	return &cloned
}

func cloneRequeueTaskRunCommit(commit *RequeueTaskRunCommit) *RequeueTaskRunCommit {
	if commit == nil {
		return nil
	}
	cloned := *commit
	cloned.RuntimeMetadata = cloneExecutionStringMap(commit.RuntimeMetadata)
	return &cloned
}

func cloneExhaustTaskRunRetriesCommit(commit *ExhaustTaskRunRetriesCommit) *ExhaustTaskRunRetriesCommit {
	if commit == nil {
		return nil
	}
	cloned := *commit
	if commit.ExitCode != nil {
		exitCode := *commit.ExitCode
		cloned.ExitCode = &exitCode
	}
	cloned.RequiredArtifactIDs = append([]string(nil), commit.RequiredArtifactIDs...)
	cloned.RuntimeMetadata = cloneExecutionStringMap(commit.RuntimeMetadata)
	return &cloned
}

func cloneExecutionStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
