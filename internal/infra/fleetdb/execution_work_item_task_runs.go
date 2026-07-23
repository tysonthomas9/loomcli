package fleetdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const executionDriverRunReviewClaimFingerprintPrefix = "driver-run-work-item-claim-v2:"

type executionTaskRunRequestBody struct {
	CommandID            string                  `json:"command_id"`
	TaskRunID            string                  `json:"task_run_id"`
	DriverStepID         string                  `json:"driver_step_id"`
	TaskID               string                  `json:"task_id"`
	ClaimActionID        string                  `json:"claim_action_id"`
	NodeID               string                  `json:"node_id"`
	LeaseID              string                  `json:"lease_id"`
	FencingToken         int64                   `json:"fencing_token"`
	WorkerProfileID      string                  `json:"worker_profile_id,omitempty"`
	Runner               string                  `json:"runner,omitempty"`
	RunnerRef            string                  `json:"runner_ref,omitempty"`
	RunnerKind           string                  `json:"runner_kind,omitempty"`
	RunnerEntrypoint     string                  `json:"runner_entrypoint,omitempty"`
	RunnerVersionID      string                  `json:"runner_driver_version_id,omitempty"`
	ProviderProfile      string                  `json:"provider_profile,omitempty"`
	TargetNodeID         string                  `json:"target_node_id,omitempty"`
	RequiredCapabilities []string                `json:"required_capabilities,omitempty"`
	RunnerPlacement      domain.TaskRunPlacement `json:"runner_placement,omitempty"`
	SandboxPlacement     domain.TaskRunPlacement `json:"sandbox_placement,omitempty"`
	RuntimeMetadata      map[string]string       `json:"runtime_metadata,omitempty"`
	Input                json.RawMessage         `json:"input,omitempty"`
	RequestedAt          time.Time               `json:"requested_at"`
	ReplayOnly           bool                    `json:"replay_only,omitempty"`
}

func (s *executionStore) RequestTaskRun(ctx context.Context, command ExecutionTaskRunRequestCommand) (*ExecutionTaskRunRequestResult, error) {
	if !executionStringsPresent(
		command.WorkspaceKey, command.CommandID, command.TaskRunID, command.DriverRunID,
		command.DriverStepID, command.TaskID, command.ClaimActionID, command.NodeID, command.LeaseID, command.LeaseToken,
	) || command.FencingToken <= 0 || command.RequestedAt.IsZero() ||
		(len(command.Input) > 0 && !json.Valid(command.Input)) {
		return nil, fmt.Errorf("execution task-run request identity, owner, payload, and time are required: %w", ErrExecutionInvalid)
	}
	body := executionTaskRunRequestBody{
		CommandID: command.CommandID, TaskRunID: command.TaskRunID, DriverStepID: command.DriverStepID,
		TaskID: command.TaskID, ClaimActionID: command.ClaimActionID,
		NodeID: command.NodeID, LeaseID: command.LeaseID, FencingToken: command.FencingToken,
		WorkerProfileID: command.WorkerProfileID, Runner: command.Runner, RunnerRef: command.RunnerRef,
		RunnerKind: command.RunnerKind, RunnerEntrypoint: command.RunnerEntrypoint, RunnerVersionID: command.RunnerVersionID,
		ProviderProfile: command.ProviderProfile, TargetNodeID: command.TargetNodeID,
		RequiredCapabilities: append([]string(nil), command.RequiredCapabilities...), RunnerPlacement: command.RunnerPlacement,
		SandboxPlacement: command.SandboxPlacement, RuntimeMetadata: cloneExecutionTransportStringMap(command.RuntimeMetadata),
		Input: append(json.RawMessage(nil), command.Input...), RequestedAt: command.RequestedAt, ReplayOnly: command.ReplayOnly,
	}
	path := "/api/v1/" + pathEscape(command.WorkspaceKey) + "/driver-runs/" + pathEscape(command.DriverRunID) + "/task-runs/request"
	var result ExecutionTaskRunRequestResult
	if err := s.client.doWithHeaders(ctx, http.MethodPost, path, body, &result, map[string]string{"X-Lease-Token": command.LeaseToken}); err != nil {
		return nil, mapExecutionTransportError("request task run", err)
	}
	wantActionID := "task-run-request:" + command.CommandID
	if result.TaskRun == nil || result.DriverStep == nil || result.Action == nil {
		return nil, fmt.Errorf("task-run request returned divergent committed state: %w", ErrExecutionUnavailable)
	}
	action := result.Action
	if !executionChecksPass(
		result.TaskRun.WorkspaceKey == command.WorkspaceKey, result.TaskRun.TaskRunID == command.TaskRunID,
		result.TaskRun.DriverRunID == command.DriverRunID, result.TaskRun.DriverStepID == command.DriverStepID,
		result.TaskRun.TaskID == command.TaskID, result.TaskRun.Status == domain.TaskRunQueued,
		result.DriverStep.WorkspaceKey == command.WorkspaceKey,
		result.DriverStep.StepID == command.DriverStepID, result.DriverStep.DriverRunID == command.DriverRunID,
		result.DriverStep.TaskRunID == command.TaskRunID, result.DriverStep.Status == domain.DriverStepQueued,
		result.DriverStep.ActionLedgerID == wantActionID, result.ClaimActionID == command.ClaimActionID,
		result.CommittedTaskRunStatus == domain.TaskRunQueued, result.CommittedDriverStepStatus == domain.DriverStepQueued,
		action.WorkspaceKey == command.WorkspaceKey, action.ActionID == wantActionID, action.IdempotencyKey == wantActionID,
		action.ActionType == "request_task_run", action.TargetRef == command.TaskRunID,
		action.RequestedBy == "driver-run:"+command.DriverRunID, action.Status == "applied",
		validExecutionCommandFingerprint(action.RequestRef), action.ResponseRef == "task-run://"+command.TaskRunID+"#queued",
		!action.CreatedAt.IsZero(), action.AppliedAt != nil && !action.AppliedAt.IsZero(),
		action.AppliedAt != nil && action.AppliedAt.Equal(action.CreatedAt),
		result.Replayed || executionPersistedCommandTimeMatches(action.CreatedAt, command.RequestedAt),
	) {
		return nil, fmt.Errorf("task-run request returned divergent committed state: %w", ErrExecutionUnavailable)
	}
	return &result, nil
}

type executionClaimAndStartBody struct {
	CommandID          string                  `json:"command_id"`
	NodeID             string                  `json:"node_id"`
	RunnerID           string                  `json:"runner_id,omitempty"`
	LeaseID            string                  `json:"lease_id"`
	ClaimTTLSeconds    int64                   `json:"claim_ttl_seconds,omitempty"`
	SupportedProviders []string                `json:"supported_providers,omitempty"`
	Capabilities       []string                `json:"capabilities,omitempty"`
	WorkerProfileIDs   []string                `json:"worker_profile_ids,omitempty"`
	RunnerPlacement    domain.TaskRunPlacement `json:"runner_placement,omitempty"`
	SandboxPlacement   domain.TaskRunPlacement `json:"sandbox_placement,omitempty"`
}

func newExecutionClaimAndStartBody(command ExecutionClaimAndStartCommand) executionClaimAndStartBody {
	claimTTLSeconds := int64(0)
	if command.ClaimTTL > 0 {
		claimTTLSeconds = int64(command.ClaimTTL / time.Second)
	}
	return executionClaimAndStartBody{
		CommandID: command.CommandID, NodeID: command.NodeID, RunnerID: command.RunnerID,
		LeaseID: command.LeaseID, ClaimTTLSeconds: claimTTLSeconds,
		SupportedProviders: append([]string(nil), command.SupportedProviders...),
		Capabilities:       append([]string(nil), command.Capabilities...),
		WorkerProfileIDs:   append([]string(nil), command.WorkerProfileIDs...),
		RunnerPlacement:    command.RunnerPlacement, SandboxPlacement: command.SandboxPlacement,
	}
}

func validateExecutionClaimAndStartResult(command ExecutionClaimAndStartCommand, result ExecutionClaimAndStartResult) error {
	if result.TaskRun == nil || result.DriverStep == nil || result.Issue == nil || result.Action == nil {
		return fmt.Errorf("claim-and-start returned divergent committed state: %w", ErrExecutionUnavailable)
	}
	wantActionID := "task-run-start:" + command.CommandID
	wantIssueAssignee := "driver-run:" + result.TaskRun.DriverRunID
	wantResponseRef := "task-run://" + result.TaskRun.TaskRunID + "#running"
	if !executionChecksPass(
		result.TaskRun.WorkspaceKey == command.WorkspaceKey, strings.TrimSpace(result.TaskRun.TaskRunID) != "",
		strings.TrimSpace(command.TaskRunID) == "" || result.TaskRun.TaskRunID == command.TaskRunID,
		strings.TrimSpace(result.TaskRun.DriverRunID) != "", strings.TrimSpace(result.TaskRun.TaskID) != "",
		result.TaskRun.Status == domain.TaskRunRunning,
		result.TaskRun.TaskID == result.Issue.ID, result.Issue.Status == "in_progress",
		result.Issue.Assignee == wantIssueAssignee, result.TaskRun.NodeID == command.NodeID,
		result.TaskRun.LeaseID == command.LeaseID, result.TaskRun.FencingToken > 0,
		result.TaskRun.DriverStepID == result.DriverStep.StepID,
		result.DriverStep.WorkspaceKey == command.WorkspaceKey,
		result.DriverStep.TaskRunID == result.TaskRun.TaskRunID,
		result.DriverStep.DriverRunID == result.TaskRun.DriverRunID,
		result.DriverStep.Status == domain.DriverStepRunning,
		result.DriverStep.ActionLedgerID == wantActionID,
		result.Action.WorkspaceKey == command.WorkspaceKey, result.Action.ActionType == "start_task_run",
		result.Action.ActionID == wantActionID, result.Action.IdempotencyKey == wantActionID,
		result.Action.TargetRef == result.TaskRun.TaskRunID, result.Action.RequestedBy == "node:"+command.NodeID,
		result.Action.Status == "applied", strings.TrimSpace(result.Action.RequestRef) != "",
		result.Action.ResponseRef == wantResponseRef, !result.Action.CreatedAt.IsZero(),
		result.Action.AppliedAt != nil && !result.Action.AppliedAt.IsZero(),
	) {
		return fmt.Errorf("claim-and-start returned divergent committed state: %w", ErrExecutionUnavailable)
	}
	return nil
}

func (s *executionStore) ClaimAndStartTaskRun(ctx context.Context, command ExecutionClaimAndStartCommand) (*ExecutionClaimAndStartResult, error) {
	if !executionStringsPresent(command.WorkspaceKey, command.CommandID, command.NodeID, command.LeaseID, command.LeaseToken) {
		return nil, fmt.Errorf("execution claim-and-start identity and lease are required: %w", ErrExecutionInvalid)
	}
	if command.ClaimTTL < 0 || (command.ClaimTTL > 0 && command.ClaimTTL < time.Second) {
		return nil, fmt.Errorf("execution claim TTL must be zero or at least one second: %w", ErrExecutionInvalid)
	}
	body := newExecutionClaimAndStartBody(command)
	path := "/api/v1/" + pathEscape(command.WorkspaceKey) + "/task-runs/claim-next-and-start"
	if strings.TrimSpace(command.TaskRunID) != "" {
		path = "/api/v1/" + pathEscape(command.WorkspaceKey) + "/task-runs/" + pathEscape(command.TaskRunID) + "/claim-and-start"
	}
	var result ExecutionClaimAndStartResult
	status, err := s.client.doWithHeadersStatus(ctx, http.MethodPost, path, body, &result, map[string]string{"X-Lease-Token": command.LeaseToken})
	if err != nil {
		return nil, mapExecutionTransportError("claim and start task run", err)
	}
	claimNext := strings.TrimSpace(command.TaskRunID) == ""
	if status == http.StatusNoContent {
		if claimNext {
			return nil, ErrExecutionNotFound
		}
		return nil, fmt.Errorf("claim-and-start returned HTTP %d for an ID-addressed command: %w", status, ErrExecutionUnavailable)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("claim-and-start returned unexpected HTTP status %d: %w", status, ErrExecutionUnavailable)
	}
	if err := validateExecutionClaimAndStartResult(command, result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *executionStore) ClaimDriverRunWorkItem(
	ctx context.Context,
	command ExecutionDriverRunWorkItemClaimCommand,
) (*ExecutionDriverRunWorkItemResult, error) {
	command.RequiredStatus = strings.TrimSpace(command.RequiredStatus)
	if !validExecutionDriverRunWorkItemOwner(command.WorkspaceKey, command.CommandID, command.RunID, command.TaskID,
		command.NodeID, command.LeaseID, command.LeaseToken, command.FencingToken) || command.ClaimedAt.IsZero() ||
		(command.RequiredStatus != "" && command.RequiredStatus != "review") ||
		command.ClaimTTL < 0 || (command.ClaimTTL > 0 && command.ClaimTTL < time.Second) {
		return nil, fmt.Errorf("execution DriverRun Work Item claim identity, owner, token, TTL, and time are required: %w", ErrExecutionInvalid)
	}
	claimTTLSeconds := int64(0)
	if command.ClaimTTL > 0 {
		claimTTLSeconds = int64(command.ClaimTTL / time.Second)
	}
	body := struct {
		CommandID       string    `json:"command_id"`
		NodeID          string    `json:"node_id"`
		LeaseID         string    `json:"lease_id"`
		FencingToken    int64     `json:"fencing_token"`
		ClaimTTLSeconds int64     `json:"claim_ttl_seconds,omitempty"`
		RequiredStatus  string    `json:"required_status,omitempty"`
		ClaimedAt       time.Time `json:"claimed_at"`
	}{
		command.CommandID, command.NodeID, command.LeaseID, command.FencingToken,
		claimTTLSeconds, command.RequiredStatus, command.ClaimedAt,
	}
	path := "/api/v1/" + pathEscape(command.WorkspaceKey) + "/driver-runs/" + pathEscape(command.RunID) +
		"/work-items/" + pathEscape(command.TaskID) + "/claim"
	var result ExecutionDriverRunWorkItemResult
	status, err := s.client.doWithHeadersStatus(ctx, http.MethodPost, path, body, &result, map[string]string{"X-Lease-Token": command.LeaseToken})
	if err != nil {
		return nil, mapExecutionTransportError("claim DriverRun Work Item", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("DriverRun Work Item claim returned unexpected HTTP status %d: %w", status, ErrExecutionUnavailable)
	}
	if !validExecutionDriverRunWorkItemResult(&result, command.WorkspaceKey, command.RunID, command.TaskID,
		command.CommandID, "claim_work_item", "claimed", command.ClaimedAt, command.RequiredStatus, true) {
		return nil, fmt.Errorf("DriverRun Work Item claim returned divergent receipt: %w", ErrExecutionUnavailable)
	}
	return &result, nil
}

func (s *executionStore) ReleaseDriverRunWorkItem(
	ctx context.Context,
	command ExecutionDriverRunWorkItemReleaseCommand,
) (*ExecutionDriverRunWorkItemResult, error) {
	command.RestoreStatus = strings.TrimSpace(command.RestoreStatus)
	if command.RestoreStatus == "" {
		command.RestoreStatus = "open"
	}
	if !validExecutionDriverRunWorkItemOwner(command.WorkspaceKey, command.CommandID, command.RunID, command.TaskID,
		command.NodeID, command.LeaseID, command.LeaseToken, command.FencingToken) ||
		strings.TrimSpace(command.ClaimActionID) == "" ||
		(command.RestoreStatus != "open" && command.RestoreStatus != "review") ||
		command.ReleasedAt.IsZero() {
		return nil, fmt.Errorf("execution DriverRun Work Item release identity, owner, token, claim action, and time are required: %w", ErrExecutionInvalid)
	}
	body := struct {
		CommandID     string    `json:"command_id"`
		NodeID        string    `json:"node_id"`
		LeaseID       string    `json:"lease_id"`
		FencingToken  int64     `json:"fencing_token"`
		ClaimActionID string    `json:"claim_action_id"`
		RestoreStatus string    `json:"restore_status"`
		ReleasedAt    time.Time `json:"released_at"`
	}{
		command.CommandID, command.NodeID, command.LeaseID, command.FencingToken,
		command.ClaimActionID, command.RestoreStatus, command.ReleasedAt,
	}
	path := "/api/v1/" + pathEscape(command.WorkspaceKey) + "/driver-runs/" + pathEscape(command.RunID) +
		"/work-items/" + pathEscape(command.TaskID) + "/release"
	var result ExecutionDriverRunWorkItemResult
	status, err := s.client.doWithHeadersStatus(ctx, http.MethodPost, path, body, &result, map[string]string{"X-Lease-Token": command.LeaseToken})
	if err != nil {
		return nil, mapExecutionTransportError("release DriverRun Work Item", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("DriverRun Work Item release returned unexpected HTTP status %d: %w", status, ErrExecutionUnavailable)
	}
	if !validExecutionDriverRunWorkItemResult(&result, command.WorkspaceKey, command.RunID, command.TaskID,
		command.CommandID, "release_work_item", "released", command.ReleasedAt, "", false) {
		return nil, fmt.Errorf("DriverRun Work Item release returned divergent receipt: %w", ErrExecutionUnavailable)
	}
	return &result, nil
}

func (s *executionStore) HandoffDriverRunReviewWorkItem(
	ctx context.Context,
	command ExecutionDriverRunReviewWorkItemHandoffCommand,
) (*ExecutionDriverRunWorkItemResult, error) {
	command.TaskRunID = strings.TrimSpace(command.TaskRunID)
	command.TargetStatus = strings.TrimSpace(command.TargetStatus)
	command.Reason = strings.TrimSpace(command.Reason)
	if !validExecutionDriverRunWorkItemOwner(command.WorkspaceKey, command.CommandID, command.RunID, command.TaskID,
		command.NodeID, command.LeaseID, command.LeaseToken, command.FencingToken) ||
		strings.TrimSpace(command.ClaimActionID) == "" || command.TaskRunID == "" ||
		(command.TargetStatus != "open" && command.TargetStatus != "closed") ||
		command.HandedOffAt.IsZero() {
		return nil, fmt.Errorf("execution DriverRun review handoff identity, owner, token, claim action, TaskRun, target, and time are required: %w", ErrExecutionInvalid)
	}
	body := struct {
		CommandID     string    `json:"command_id"`
		NodeID        string    `json:"node_id"`
		LeaseID       string    `json:"lease_id"`
		FencingToken  int64     `json:"fencing_token"`
		ClaimActionID string    `json:"claim_action_id"`
		TaskRunID     string    `json:"task_run_id"`
		TargetStatus  string    `json:"target_status"`
		Reason        string    `json:"reason"`
		HandedOffAt   time.Time `json:"handed_off_at"`
	}{
		command.CommandID, command.NodeID, command.LeaseID, command.FencingToken,
		command.ClaimActionID, command.TaskRunID, command.TargetStatus, command.Reason, command.HandedOffAt,
	}
	path := "/api/v1/" + pathEscape(command.WorkspaceKey) + "/driver-runs/" + pathEscape(command.RunID) +
		"/work-items/" + pathEscape(command.TaskID) + "/review-handoff"
	var result ExecutionDriverRunWorkItemResult
	status, err := s.client.doWithHeadersStatus(ctx, http.MethodPost, path, body, &result, map[string]string{"X-Lease-Token": command.LeaseToken})
	if err != nil {
		return nil, mapExecutionTransportError("handoff DriverRun review Work Item", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("DriverRun review Work Item handoff returned unexpected HTTP status %d: %w", status, ErrExecutionUnavailable)
	}
	if !validExecutionDriverRunReviewWorkItemHandoffResult(&result, command) {
		return nil, fmt.Errorf("DriverRun review Work Item handoff returned divergent receipt: %w", ErrExecutionUnavailable)
	}
	return &result, nil
}

func validExecutionDriverRunReviewWorkItemHandoffResult(
	result *ExecutionDriverRunWorkItemResult,
	command ExecutionDriverRunReviewWorkItemHandoffCommand,
) bool {
	if result == nil || result.Issue == nil || result.Action == nil ||
		result.Issue.Workspace != command.WorkspaceKey || result.Issue.ID != command.TaskID ||
		result.Issue.Status != command.TargetStatus || result.Issue.Assignee != "" ||
		result.Issue.UpdatedAt.IsZero() {
		return false
	}
	actor := "driver-run:" + command.RunID
	wantActionID := "driver-run-review-work-item-handoff:" + command.CommandID
	action := result.Action
	return executionChecksPass(
		action.WorkspaceKey == command.WorkspaceKey, action.ActionID == wantActionID,
		action.IdempotencyKey == wantActionID, action.ActionType == "handoff_review_work_item",
		action.TargetRef == command.TaskID, action.RequestedBy == actor, action.Status == "applied",
		validExecutionCommandFingerprint(action.RequestRef),
		action.ResponseRef == "issue://"+command.TaskID+"#handed-off", !action.CreatedAt.IsZero(),
		action.AppliedAt != nil && !action.AppliedAt.IsZero() && action.AppliedAt.Equal(action.CreatedAt),
		result.Replayed || executionPersistedCommandTimeMatches(action.CreatedAt, command.HandedOffAt),
	)
}

func validExecutionDriverRunWorkItemOwner(workspace, commandID, runID, taskID, nodeID, leaseID, leaseToken string, fencingToken int64) bool {
	return executionStringsPresent(workspace, commandID, runID, taskID, nodeID, leaseID, leaseToken) &&
		len(commandID) <= 128 && fencingToken > 0
}

func validExecutionDriverRunWorkItemResult(
	result *ExecutionDriverRunWorkItemResult,
	workspace, runID, taskID, commandID, actionType, responseState string,
	requestedAt time.Time,
	requiredStatus string,
	claim bool,
) bool {
	if result == nil || result.Issue == nil || result.Action == nil || result.Issue.Workspace != workspace ||
		result.Issue.ID != taskID || result.Issue.UpdatedAt.IsZero() {
		return false
	}
	actor := "driver-run:" + runID
	if claim {
		if result.Issue.Status != "in_progress" || result.Issue.Assignee != actor {
			return false
		}
	} else if !validExecutionReleasedIssue(result.Issue, actor) {
		return false
	}
	actionPrefix := "driver-run-work-item-" + strings.TrimSuffix(actionType, "_work_item") + ":"
	wantActionID := actionPrefix + commandID
	action := result.Action
	validRequestRef := validExecutionCommandFingerprint(action.RequestRef)
	if claim && requiredStatus == "review" {
		validRequestRef = validExecutionPrefixedCommandFingerprint(
			action.RequestRef,
			executionDriverRunReviewClaimFingerprintPrefix,
		)
	}
	return executionChecksPass(
		action.WorkspaceKey == workspace, action.ActionID == wantActionID, action.IdempotencyKey == wantActionID,
		action.ActionType == actionType, action.TargetRef == taskID, action.RequestedBy == actor,
		action.Status == "applied", validRequestRef,
		action.ResponseRef == "issue://"+taskID+"#"+responseState, !action.CreatedAt.IsZero(),
		action.AppliedAt != nil && !action.AppliedAt.IsZero() && action.AppliedAt.Equal(action.CreatedAt),
		result.Replayed || executionPersistedCommandTimeMatches(action.CreatedAt, requestedAt),
	)
}

func validExecutionReleasedIssue(issue *ExecutionIssue, actor string) bool {
	if issue == nil {
		return false
	}
	switch issue.Status {
	case "open", "review":
		return issue.Assignee == ""
	case "closed", "tombstone":
		return issue.Assignee == actor
	default:
		return false
	}
}

func validExecutionCommandFingerprint(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func validExecutionPrefixedCommandFingerprint(value, prefix string) bool {
	return strings.HasPrefix(value, prefix) &&
		validExecutionCommandFingerprint(strings.TrimPrefix(value, prefix))
}

func executionPersistedCommandTimeMatches(got, want time.Time) bool {
	return !got.IsZero() && !want.IsZero() &&
		got.Truncate(time.Microsecond).Equal(want.Truncate(time.Microsecond))
}

// ExecutionTaskRunWorkItemDesignCommand is FleetDB's exact TaskRun-owner
// command for updating the design of the Work Item bound by TaskRun.TaskID.
// LeaseToken is write-only and crosses the wire only in X-Lease-Token; callers
// cannot select a different Work Item.
type ExecutionTaskRunWorkItemDesignCommand struct {
	WorkspaceKey string
	CommandID    string
	TaskRunID    string
	NodeID       string
	LeaseID      string
	LeaseToken   string `json:"-"`
	FencingToken int64
	Design       string
	DesignFormat *string
}

type ExecutionTaskRunWorkItemDesignCommit struct {
	WorkspaceKey     string    `json:"workspace_key"`
	TaskRunID        string    `json:"task_run_id"`
	TaskID           string    `json:"task_id"`
	DesignFormat     string    `json:"design_format"`
	DesignArtifactID string    `json:"design_artifact_id"`
	DesignSHA256     string    `json:"design_sha256"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type ExecutionTaskRunWorkItemDesignResult struct {
	TaskRun   *domain.TaskRun                      `json:"task_run"`
	Issue     *ExecutionIssue                      `json:"issue"`
	Action    *ExecutionActionLedger               `json:"action"`
	Committed ExecutionTaskRunWorkItemDesignCommit `json:"committed"`
	Replayed  bool                                 `json:"replayed"`
}

func (s *executionStore) UpdateTaskRunWorkItemDesign(
	ctx context.Context,
	command ExecutionTaskRunWorkItemDesignCommand,
) (*ExecutionTaskRunWorkItemDesignResult, error) {
	command, format, err := prepareExecutionTaskRunWorkItemDesignCommand(command)
	if err != nil {
		return nil, err
	}
	body := struct {
		CommandID    string  `json:"command_id"`
		NodeID       string  `json:"node_id"`
		LeaseID      string  `json:"lease_id"`
		FencingToken int64   `json:"fencing_token"`
		Design       string  `json:"design"`
		DesignFormat *string `json:"design_format,omitempty"`
	}{
		CommandID: command.CommandID, NodeID: command.NodeID, LeaseID: command.LeaseID,
		FencingToken: command.FencingToken, Design: command.Design, DesignFormat: command.DesignFormat,
	}
	path := "/api/v1/" + pathEscape(command.WorkspaceKey) + "/task-runs/" + pathEscape(command.TaskRunID) + "/work-item/design"
	var result ExecutionTaskRunWorkItemDesignResult
	if err = s.client.doWithHeaders(ctx, http.MethodPost, path, body, &result, map[string]string{"X-Lease-Token": command.LeaseToken}); err != nil {
		return nil, mapExecutionTransportError("update TaskRun Work Item design", err)
	}
	if !executionTaskRunWorkItemDesignResultMatches(&result, command, format) {
		return nil, fmt.Errorf("TaskRun Work Item design update returned divergent committed state: %w", ErrExecutionUnavailable)
	}
	return &result, nil
}

func prepareExecutionTaskRunWorkItemDesignCommand(
	command ExecutionTaskRunWorkItemDesignCommand,
) (ExecutionTaskRunWorkItemDesignCommand, string, error) {
	if !executionStringsPresent(
		command.WorkspaceKey, command.CommandID, command.TaskRunID,
		command.NodeID, command.LeaseID, command.LeaseToken,
	) || command.FencingToken <= 0 || strings.TrimSpace(command.Design) == "" {
		return command, "", fmt.Errorf("execution task-run Work Item design identity and owner are required: %w", ErrExecutionInvalid)
	}
	format := "markdown"
	if command.DesignFormat != nil && strings.TrimSpace(*command.DesignFormat) != "" {
		format = strings.TrimSpace(*command.DesignFormat)
	}
	if format != "markdown" && format != "html" {
		return command, "", fmt.Errorf("execution task-run Work Item design format is invalid: %w", ErrExecutionInvalid)
	}
	command.DesignFormat = &format
	return command, format, nil
}

func executionTaskRunWorkItemDesignResultMatches(
	result *ExecutionTaskRunWorkItemDesignResult,
	command ExecutionTaskRunWorkItemDesignCommand,
	format string,
) bool {
	if result == nil || result.TaskRun == nil || result.Issue == nil || result.Action == nil {
		return false
	}
	designDigest, designSHA256 := executionTaskRunWorkItemDesignDigest(command.Design)
	wantResponseRef := executionTaskRunWorkItemDesignResponseRef(
		result.TaskRun.TaskID, format, designDigest, result.Committed.DesignArtifactID,
	)
	return executionTaskRunWorkItemDesignProjectionMatches(result, command, format, designSHA256) &&
		executionTaskRunWorkItemDesignActionMatches(result, command, wantResponseRef)
}

func executionTaskRunWorkItemDesignProjectionMatches(
	result *ExecutionTaskRunWorkItemDesignResult,
	command ExecutionTaskRunWorkItemDesignCommand,
	format, designSHA256 string,
) bool {
	return executionChecksPass(
		result.TaskRun.WorkspaceKey == command.WorkspaceKey, result.TaskRun.TaskRunID == command.TaskRunID,
		strings.TrimSpace(result.TaskRun.TaskID) != "",
		result.Replayed || (result.TaskRun.NodeID == command.NodeID &&
			result.TaskRun.LeaseID == command.LeaseID && result.TaskRun.FencingToken == command.FencingToken),
		result.Replayed || result.TaskRun.Status == domain.TaskRunRunning,
		result.Issue.Workspace == command.WorkspaceKey, result.Issue.ID == result.TaskRun.TaskID,
		!result.Issue.UpdatedAt.IsZero(), result.Committed.WorkspaceKey == command.WorkspaceKey,
		result.Committed.TaskRunID == command.TaskRunID, result.Committed.TaskID == result.TaskRun.TaskID,
		result.Committed.DesignFormat == format, result.Committed.DesignSHA256 == designSHA256,
		!result.Committed.UpdatedAt.IsZero(),
	)
}

func executionTaskRunWorkItemDesignActionMatches(
	result *ExecutionTaskRunWorkItemDesignResult,
	command ExecutionTaskRunWorkItemDesignCommand,
	wantResponseRef string,
) bool {
	wantActionID := "task-run-work-item-design-update:" + command.CommandID
	return executionChecksPass(
		result.Action.WorkspaceKey == command.WorkspaceKey, result.Action.ActionID == wantActionID,
		result.Action.IdempotencyKey == wantActionID, result.Action.ActionType == "task_run_work_item_design_update",
		result.Action.TargetRef == command.TaskRunID, result.Action.RequestedBy == "node:"+command.NodeID,
		result.Action.Status == "applied", strings.HasPrefix(result.Action.RequestRef, "sha256:"),
		result.Action.ResponseRef == wantResponseRef, !result.Action.CreatedAt.IsZero(),
		result.Committed.UpdatedAt.Equal(result.Action.CreatedAt),
		result.Action.AppliedAt != nil && result.Action.AppliedAt.Equal(result.Action.CreatedAt),
	)
}

func executionTaskRunWorkItemDesignDigest(design string) (string, string) {
	digest := sha256.Sum256([]byte(design))
	bare := hex.EncodeToString(digest[:])
	return bare, "sha256:" + bare
}

func executionTaskRunWorkItemDesignResponseRef(taskID, format, designDigest, artifactID string) string {
	responseRef := "issue://" + taskID + "/design?format=" + format + "&sha256=" + designDigest
	if artifactID != "" {
		responseRef += "&artifact_id=" + artifactID
	}
	return responseRef
}

func (s *executionStore) RecoverTerminalDriverRunWork(
	ctx context.Context,
	command ExecutionTerminalDriverRunWorkRecoveryCommand,
) (*ExecutionTerminalDriverRunWorkRecoveryResult, error) {
	if !executionStringsPresent(
		command.WorkspaceKey, command.RequestID, command.DriverRunID, command.Reason, command.ErrorClass,
	) || !command.ParentStatus.IsTerminal() || command.RecoveredAt.IsZero() {
		return nil, fmt.Errorf("execution terminal DriverRun work recovery identity and terminal intent are required: %w", ErrExecutionInvalid)
	}
	body := struct {
		RequestID    string                 `json:"request_id"`
		ParentStatus domain.DriverRunStatus `json:"parent_status"`
		Reason       string                 `json:"reason"`
		ErrorClass   string                 `json:"error_class"`
		RecoveredAt  time.Time              `json:"recovered_at"`
	}{
		RequestID: command.RequestID, ParentStatus: command.ParentStatus,
		Reason: command.Reason, ErrorClass: command.ErrorClass, RecoveredAt: command.RecoveredAt,
	}
	path := "/api/v1/" + pathEscape(command.WorkspaceKey) + "/driver-runs/" + pathEscape(command.DriverRunID) + "/commands/recover-terminal-work"
	var result ExecutionTerminalDriverRunWorkRecoveryResult
	if err := s.client.do(ctx, http.MethodPost, path, body, &result); err != nil {
		return nil, mapExecutionTransportError("recover terminal DriverRun work", err)
	}
	if result.WorkspaceKey != command.WorkspaceKey || result.DriverRunID != command.DriverRunID ||
		result.ParentStatus != command.ParentStatus || result.Reason != command.Reason || result.ErrorClass != command.ErrorClass ||
		result.RecoveredAt.IsZero() || result.Action == nil || result.ActionID == "" || result.Action.ActionID != result.ActionID ||
		result.Action.ActionType != "recover_terminal_driver_run_work" {
		return nil, fmt.Errorf("terminal DriverRun work recovery returned divergent receipt: %w", ErrExecutionUnavailable)
	}
	return &result, nil
}

// RepairTerminalDriverStep invokes FleetDB's system-only convergence command.
// It deliberately carries no DriverRun owner token: the committed terminal
// TaskRun and exact backlinks are the authority for this repair lane.
func (s *executionStore) RepairTerminalDriverStep(ctx context.Context, repair store.TerminalDriverStepRepair) (*domain.DriverStep, bool, error) {
	if !executionStringsPresent(
		repair.RequestID, repair.WorkspaceKey, repair.DriverRunID, repair.DriverStepID, repair.TaskRunID,
	) || !repair.Status.IsTerminal() {
		return nil, false, fmt.Errorf("terminal DriverStep repair identity and terminal projection are required: %w", ErrExecutionInvalid)
	}
	body := struct {
		CommandID   string                  `json:"command_id"`
		DriverRunID string                  `json:"driver_run_id"`
		TaskRunID   string                  `json:"task_run_id"`
		Status      domain.DriverStepStatus `json:"status"`
		OutputRef   string                  `json:"output_ref,omitempty"`
	}{repair.RequestID, repair.DriverRunID, repair.TaskRunID, repair.Status, repair.OutputRef}
	var result struct {
		DriverStep *domain.DriverStep `json:"driver_step"`
		Replayed   bool               `json:"replayed"`
	}
	path := "/api/v1/" + pathEscape(repair.WorkspaceKey) + "/driver-steps/" + pathEscape(repair.DriverStepID) + "/repair-terminal"
	if err := s.client.do(ctx, http.MethodPost, path, body, &result); err != nil {
		return nil, false, mapExecutionTransportError("repair terminal DriverStep", err)
	}
	if result.DriverStep == nil {
		return nil, false, fmt.Errorf("terminal DriverStep repair returned no projection: %w", ErrExecutionUnavailable)
	}
	if !executionChecksPass(
		result.DriverStep.WorkspaceKey == repair.WorkspaceKey,
		result.DriverStep.StepID == repair.DriverStepID,
		result.DriverStep.DriverRunID == repair.DriverRunID,
		result.DriverStep.TaskRunID == repair.TaskRunID,
		result.DriverStep.Status == repair.Status,
		result.DriverStep.OutputRef == repair.OutputRef,
	) {
		return nil, false, fmt.Errorf("terminal DriverStep repair returned divergent projection: %w", ErrExecutionUnavailable)
	}
	return result.DriverStep, result.Replayed, nil
}

var _ store.DriverRunOutcomeStore = (*driverRunStore)(nil)
var _ store.TerminalDriverRunWorkRecoveryQueueStore = (*driverRunStore)(nil)

func (s *driverRunStore) ClaimDriverRunOutcomes(ctx context.Context, claim store.DriverRunOutcomeClaim) ([]store.DriverRunOutcome, error) {
	body := map[string]any{
		"claim_id": claim.ClaimID, "before": claim.Before, "claim_until": claim.ClaimUntil, "limit": claim.Limit,
	}
	var response struct {
		Outcomes []store.DriverRunOutcome `json:"outcomes"`
	}
	path := "/api/v1/" + pathEscape(claim.WorkspaceKey) + "/driver-run-outcomes/claim"
	if err := s.client.do(ctx, "POST", path, body, &response); err != nil {
		return nil, err
	}
	if response.Outcomes == nil {
		response.Outcomes = []store.DriverRunOutcome{}
	}
	return response.Outcomes, nil
}

func (s *driverRunStore) CompleteDriverRunOutcome(ctx context.Context, completion store.DriverRunOutcomeCompletion) error {
	completedAt := completion.CompletedAt
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	body := map[string]any{"run_id": completion.RunID, "claim_id": completion.ClaimID, "completed_at": completedAt}
	path := "/api/v1/" + pathEscape(completion.WorkspaceKey) + "/driver-run-outcomes/complete"
	return s.client.do(ctx, "POST", path, body, nil)
}

func (s *driverRunStore) RetryDriverRunOutcome(ctx context.Context, retry store.DriverRunOutcomeRetry) error {
	body := map[string]any{"run_id": retry.RunID, "claim_id": retry.ClaimID, "available_at": retry.AvailableAt, "error": retry.Error}
	path := "/api/v1/" + pathEscape(retry.WorkspaceKey) + "/driver-run-outcomes/retry"
	return s.client.do(ctx, "POST", path, body, nil)
}

func (s *driverRunStore) ClaimTerminalDriverRunWorkRecoveries(
	ctx context.Context,
	claim store.TerminalDriverRunWorkRecoveryClaim,
) ([]store.DriverRunOutcome, error) {
	body := map[string]any{
		"claim_id": claim.ClaimID, "before": claim.Before, "claim_until": claim.ClaimUntil, "limit": claim.Limit,
	}
	var response struct {
		Outcomes []store.DriverRunOutcome `json:"outcomes"`
		Count    *int                     `json:"count"`
	}
	path := "/api/v1/" + pathEscape(claim.WorkspaceKey) + "/driver-run-outcomes/terminal-work/claim"
	if err := s.client.do(ctx, http.MethodPost, path, body, &response); err != nil {
		return nil, mapExecutionTransportError("claim terminal DriverRun work recoveries", err)
	}
	if response.Count == nil || *response.Count != len(response.Outcomes) {
		return nil, fmt.Errorf("terminal DriverRun work recovery claim returned divergent count: %w", ErrExecutionUnavailable)
	}
	seen := make(map[string]struct{}, len(response.Outcomes))
	for _, outcome := range response.Outcomes {
		if outcome.WorkspaceKey != claim.WorkspaceKey || strings.TrimSpace(outcome.RunID) == "" ||
			!outcome.Status.IsTerminal() || outcome.OccurredAt.IsZero() || outcome.Attempt < 1 {
			return nil, fmt.Errorf("terminal DriverRun work recovery claim returned invalid snapshot: %w", ErrExecutionUnavailable)
		}
		if _, duplicate := seen[outcome.RunID]; duplicate {
			return nil, fmt.Errorf("terminal DriverRun work recovery claim returned duplicate snapshot: %w", ErrExecutionUnavailable)
		}
		seen[outcome.RunID] = struct{}{}
	}
	if response.Outcomes == nil {
		return []store.DriverRunOutcome{}, nil
	}
	return response.Outcomes, nil
}

func (s *driverRunStore) CompleteTerminalDriverRunWorkRecovery(
	ctx context.Context,
	completion store.TerminalDriverRunWorkRecoveryCompletion,
) error {
	completedAt := completion.CompletedAt
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	body := map[string]any{"run_id": completion.RunID, "claim_id": completion.ClaimID, "completed_at": completedAt}
	var response struct {
		Completed bool `json:"completed"`
	}
	path := "/api/v1/" + pathEscape(completion.WorkspaceKey) + "/driver-run-outcomes/terminal-work/complete"
	if err := s.client.do(ctx, http.MethodPost, path, body, &response); err != nil {
		return mapExecutionTransportError("complete terminal DriverRun work recovery", err)
	}
	if !response.Completed {
		return fmt.Errorf("terminal DriverRun work recovery completion returned divergent receipt: %w", ErrExecutionUnavailable)
	}
	return nil
}

func (s *driverRunStore) RetryTerminalDriverRunWorkRecovery(
	ctx context.Context,
	retry store.TerminalDriverRunWorkRecoveryRetry,
) error {
	body := map[string]any{"run_id": retry.RunID, "claim_id": retry.ClaimID, "available_at": retry.AvailableAt, "error": retry.Error}
	var response struct {
		Retried bool `json:"retried"`
	}
	path := "/api/v1/" + pathEscape(retry.WorkspaceKey) + "/driver-run-outcomes/terminal-work/retry"
	if err := s.client.do(ctx, http.MethodPost, path, body, &response); err != nil {
		return mapExecutionTransportError("retry terminal DriverRun work recovery", err)
	}
	if !response.Retried {
		return fmt.Errorf("terminal DriverRun work recovery retry returned divergent receipt: %w", ErrExecutionUnavailable)
	}
	return nil
}

func mapExecutionTransportError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var sentinel error
	switch {
	case errors.Is(err, ErrExecutionNotFound), errors.Is(err, ErrExecutionInvalid), errors.Is(err, ErrExecutionConflict), errors.Is(err, ErrExecutionNotOwner), errors.Is(err, ErrExecutionInvalidTransition), errors.Is(err, ErrExecutionAlreadyResumed), errors.Is(err, ErrExecutionUnavailable):
		return err
	case errors.Is(err, domain.ErrNotFound):
		sentinel = ErrExecutionNotFound
	case errors.Is(err, domain.ErrNotOwner), errors.Is(err, domain.ErrGone):
		sentinel = ErrExecutionNotOwner
	case errors.Is(err, domain.ErrInvalidTransition):
		sentinel = ErrExecutionInvalidTransition
	case errors.Is(err, domain.ErrDriverRunAlreadyResumed):
		sentinel = ErrExecutionAlreadyResumed
	case errors.Is(err, domain.ErrAlreadyExists), errors.Is(err, domain.ErrAlreadyClaimed), errors.Is(err, domain.ErrConflict):
		sentinel = ErrExecutionConflict
	case errors.Is(err, domain.ErrInvalid):
		sentinel = ErrExecutionInvalid
	default:
		sentinel = ErrExecutionUnavailable
	}
	return fmt.Errorf("%s: %w", operation, errors.Join(sentinel, err))
}

func cloneExecutionTransportStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
