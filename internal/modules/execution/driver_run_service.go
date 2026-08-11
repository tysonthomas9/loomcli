package execution

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	defaultDriverAwaitMaxTimeout      = 14 * 24 * time.Hour
	defaultDriverAwaitMaxPerRun       = 100
	defaultDriverAwaitTotalSuspendCap = 30 * 24 * time.Hour

	// Fleet versions review-claim fingerprints so the retained review restore
	// status is cryptographically bound into the immutable claim receipt.
	driverRunWorkItemReviewClaimFingerprintPrefix = "driver-run-work-item-claim-v2:"
)

func (service *Service) GetDriverRun(ctx context.Context, workspace, runID string) (*DriverRun, error) {
	if strings.TrimSpace(workspace) == "" || strings.TrimSpace(runID) == "" {
		return nil, ErrInvalid
	}
	port := service.dependencies.DriverRuns.Queries
	if port == nil {
		return nil, ErrUnavailable
	}
	run, err := port.GetDriverRun(ctx, workspace, runID)
	if err != nil {
		return nil, err
	}
	if run == nil || run.WorkspaceKey != workspace || run.RunID != runID {
		return nil, ErrConflict
	}
	return cloneDriverRun(run), nil
}

func (service *Service) ListDriverRuns(ctx context.Context, query DriverRunQuery) ([]*DriverRun, error) {
	if strings.TrimSpace(query.WorkspaceKey) == "" || query.Limit < 0 {
		return nil, ErrInvalid
	}
	port := service.dependencies.DriverRuns.Queries
	if port == nil {
		return nil, ErrUnavailable
	}
	runs, err := port.ListDriverRuns(ctx, query)
	if err != nil {
		return nil, err
	}
	result := make([]*DriverRun, 0, len(runs))
	for _, run := range runs {
		if run == nil || run.WorkspaceKey != query.WorkspaceKey ||
			(query.DriverID != "" && run.DriverID != query.DriverID) ||
			(query.EpicID != "" && run.EpicID != query.EpicID) ||
			(query.ParentRunID != "" && run.ParentRunID != query.ParentRunID) ||
			(query.AgentServiceID != "" && run.AgentServiceID != query.AgentServiceID) ||
			(query.Status != "" && run.Status != query.Status) {
			return nil, ErrConflict
		}
		result = append(result, cloneDriverRun(run))
	}
	if query.Limit > 0 && len(result) > query.Limit {
		return nil, ErrConflict
	}
	return result, nil
}

func (service *Service) SubmitDriverRun(ctx context.Context, auth authority.OperatorAuthority, command SubmitDriverRunCommand) (*DriverRun, error) {
	if err := service.requireOperator(ActionSubmitDriverRun, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if strings.TrimSpace(command.RequestID) == "" || strings.TrimSpace(command.RunID) == "" ||
		strings.TrimSpace(command.DriverID) == "" || strings.TrimSpace(command.DriverVersionID) == "" ||
		strings.TrimSpace(command.SourceKind) == "" || strings.TrimSpace(command.SourceRef) == "" ||
		len(command.Payload) == 0 || !json.Valid(command.Payload) {
		return nil, ErrInvalid
	}
	port := service.dependencies.DriverRuns.Submissions
	if port == nil {
		return nil, ErrUnavailable
	}
	run, err := port.SubmitDriverRun(ctx, cloneSubmitDriverRunCommand(command))
	if err != nil {
		return nil, err
	}
	if err := validateSubmittedDriverRun(command, run); err != nil {
		return nil, err
	}
	return cloneDriverRun(run), nil
}

func (service *Service) StartChildDriverRun(ctx context.Context, auth authority.ExecutionAuthority, command StartChildDriverRunCommand) (*DriverRun, error) {
	if err := service.requireOwner(ActionStartChildDriverRun, command.WorkspaceKey, command.Owner, auth); err != nil {
		return nil, err
	}
	childKey := strings.TrimSpace(command.ChildKey)
	childRunID := ChildDriverRunID(command.Owner.ResourceID, childKey)
	if !validStartChildDriverRunCommand(command, childKey, childRunID) {
		return nil, ErrInvalid
	}
	port := service.dependencies.DriverRuns.ChildStarts
	if port == nil {
		return nil, ErrUnavailable
	}
	command.ChildRunID = childRunID
	command.Payload = append(json.RawMessage(nil), command.Payload...)
	result, err := port.StartChildDriverRun(ctx, command)
	if err != nil {
		return nil, err
	}
	if err := validateChildStartParent(result, command); err != nil {
		return nil, err
	}
	submission := SubmitDriverRunCommand{
		WorkspaceKey: command.WorkspaceKey, RequestID: command.RequestID, RunID: childRunID,
		DriverID: command.DriverID, DriverVersionID: command.DriverVersionID,
		Entrypoint: "run", SourceKind: "workflow", SourceRef: command.Owner.ResourceID,
		ParentRunID: command.Owner.ResourceID, Payload: append(json.RawMessage(nil), command.Payload...),
	}
	if err := validateSubmittedDriverRun(submission, result.Child); err != nil {
		return nil, err
	}
	if !validChildStartResult(result, command) {
		return nil, fmt.Errorf("%w: child DriverRun escaped parent envelope", ErrConflict)
	}
	if result.ChildDepth > command.MaxDepth {
		return nil, ErrCompositionDepthExceeded
	}
	return cloneDriverRun(result.Child), nil
}

func validStartChildDriverRunCommand(command StartChildDriverRunCommand, childKey, childRunID string) bool {
	return command.Owner.ResourceKind == ResourceDriverRun && childKey != "" && childKey == command.ChildKey &&
		strings.TrimSpace(command.DriverID) != "" && strings.TrimSpace(command.DriverVersionID) != "" &&
		command.MaxDepth >= 1 && len(command.Payload) > 0 && json.Valid(command.Payload) &&
		command.RequestID == ChildDriverRunRequestID(command.Owner.ResourceID, childKey) &&
		(strings.TrimSpace(command.ChildRunID) == "" || command.ChildRunID == childRunID)
}

func validateChildStartParent(result StartChildDriverRunResult, command StartChildDriverRunCommand) error {
	if !result.Replay {
		return validateOwnedDriverRun(command.WorkspaceKey, command.Owner, result.Parent)
	}
	// Child-start replay returns the immutable original parent snapshot. A
	// successor owner may legitimately authorize the probe, so its current
	// tuple must not be compared with that historical receipt.
	parent := result.Parent
	if parent == nil || parent.WorkspaceKey != command.WorkspaceKey || parent.RunID != command.Owner.ResourceID ||
		parent.Status != DriverRunRunning || parent.Owner.ResourceKind != ResourceDriverRun ||
		parent.Owner.ResourceID != command.Owner.ResourceID || strings.TrimSpace(parent.Owner.NodeID) == "" ||
		strings.TrimSpace(parent.Owner.LeaseID) == "" || parent.Owner.FencingToken <= 0 {
		return fmt.Errorf("%w: child DriverRun replay has invalid original parent receipt", ErrConflict)
	}
	return nil
}

func validChildStartResult(result StartChildDriverRunResult, command StartChildDriverRunCommand) bool {
	return result.Child.ParentRunID == command.Owner.ResourceID && result.Child.SourceKind == "workflow" &&
		result.Child.SourceRef == command.Owner.ResourceID && result.ParentDepth >= 1 &&
		result.ChildDepth == result.ParentDepth+1 && result.Child.Entrypoint == "run" &&
		bytes.Equal(result.Child.Payload, command.Payload) && strings.TrimSpace(result.ActionID) != "" &&
		result.Child.Status == DriverRunQueued
}

func (service *Service) CascadeChildDriverRuns(ctx context.Context, auth authority.ExecutionAuthority, command CascadeChildDriverRunsCommand) (CascadeChildDriverRunsResult, error) {
	if err := service.requireOwner(ActionCascadeChildDriverRuns, command.WorkspaceKey, command.Owner, auth); err != nil {
		return CascadeChildDriverRunsResult{}, err
	}
	if strings.TrimSpace(command.ParentRunID) == "" {
		command.ParentRunID = command.Owner.ResourceID
	}
	if command.Owner.ResourceKind != ResourceDriverRun || !command.ParentStatus.IsTerminal() ||
		command.ParentRunID != command.Owner.ResourceID || command.SystemRecovery ||
		command.RequestID != CascadeChildDriverRunsRequestID(command.ParentRunID, command.ParentStatus) ||
		strings.TrimSpace(command.Reason) == "" || strings.TrimSpace(command.ErrorClass) == "" ||
		command.CascadedAt.IsZero() || command.MaxDepth < 1 {
		return CascadeChildDriverRunsResult{}, ErrInvalid
	}
	return service.applyChildDriverRunCascade(ctx, command)
}

func (service *Service) RecoverChildDriverRunCascade(ctx context.Context, auth authority.SystemAuthority, command RecoverChildDriverRunCascadeCommand) (CascadeChildDriverRunsResult, error) {
	if err := service.requireSystem(ActionRecoverChildDriverRunCascade, command.WorkspaceKey, auth); err != nil {
		return CascadeChildDriverRunsResult{}, err
	}
	command.ParentRunID = strings.TrimSpace(command.ParentRunID)
	if command.ParentRunID == "" || !command.ParentStatus.IsTerminal() ||
		command.RequestID != CascadeChildDriverRunsRequestID(command.ParentRunID, command.ParentStatus) ||
		strings.TrimSpace(command.Reason) == "" || strings.TrimSpace(command.ErrorClass) == "" ||
		command.CascadedAt.IsZero() || command.MaxDepth < 1 {
		return CascadeChildDriverRunsResult{}, ErrInvalid
	}
	return service.applyChildDriverRunCascade(ctx, CascadeChildDriverRunsCommand{
		WorkspaceKey: command.WorkspaceKey, RequestID: command.RequestID, ParentRunID: command.ParentRunID,
		ParentStatus: command.ParentStatus, Reason: command.Reason, ErrorClass: command.ErrorClass,
		CascadedAt: command.CascadedAt, MaxDepth: command.MaxDepth, SystemRecovery: true,
	})
}

func (service *Service) applyChildDriverRunCascade(ctx context.Context, command CascadeChildDriverRunsCommand) (CascadeChildDriverRunsResult, error) {
	port := service.dependencies.DriverRuns.Cascades
	if port == nil {
		return CascadeChildDriverRunsResult{}, ErrUnavailable
	}
	result, err := port.CascadeChildDriverRuns(ctx, cloneCascadeChildDriverRunsCommand(command))
	if err != nil {
		return CascadeChildDriverRunsResult{}, err
	}
	if err := validateCascadeChildDriverRunsResult(result, command); err != nil {
		return CascadeChildDriverRunsResult{}, fmt.Errorf("%w: child DriverRun cascade escaped parent envelope", ErrConflict)
	}
	return publicCascadeChildDriverRunsResult(result), nil
}

func validateCascadeChildDriverRunsResult(result CascadeChildDriverRunsResult, command CascadeChildDriverRunsCommand) error {
	commit := result.Committed
	if !validCascadeCommitEnvelope(commit, command, result.Replay) || strings.TrimSpace(result.ActionID) == "" {
		return ErrConflict
	}
	cancelledIDs := canonicalDriverRunIDs(commit.CancelledRunIDs)
	requestedIDs := canonicalDriverRunIDs(commit.CancelRequestedRunIDs)
	if !canonicalCascadeRunIDs(commit, cancelledIDs, requestedIDs) || !disjointDriverRunIDs(cancelledIDs, requestedIDs) {
		return ErrConflict
	}
	cancelledRuns := make(map[string]*DriverRun, len(result.CancelledRuns))
	for _, run := range result.CancelledRuns {
		if run != nil {
			cancelledRuns[run.RunID] = run
		}
	}
	if err := validateCascadeCurrentRuns(result.CancelledRuns, cancelledIDs, command, cancelledRuns, true, result.Replay); err != nil {
		return err
	}
	if err := validateCascadeCurrentRuns(result.CancelRequestedRuns, requestedIDs, command, cancelledRuns, false, result.Replay); err != nil {
		return err
	}
	return nil
}

func validCascadeCommitEnvelope(commit *CascadeChildDriverRunsCommit, command CascadeChildDriverRunsCommand, replay bool) bool {
	return commit != nil && commit.WorkspaceKey == command.WorkspaceKey && commit.ParentRunID == command.ParentRunID &&
		commit.ParentStatus == command.ParentStatus && commit.Reason == command.Reason && commit.ErrorClass == command.ErrorClass &&
		commit.MaxDepth == command.MaxDepth && ((!replay && commit.CascadedAt.Equal(command.CascadedAt)) || (replay && !commit.CascadedAt.IsZero()))
}

func canonicalCascadeRunIDs(commit *CascadeChildDriverRunsCommit, cancelledIDs, requestedIDs []string) bool {
	return slices.Equal(cancelledIDs, commit.CancelledRunIDs) && slices.Equal(requestedIDs, commit.CancelRequestedRunIDs)
}

func disjointDriverRunIDs(cancelledIDs, requestedIDs []string) bool {
	cancelled := make(map[string]struct{}, len(cancelledIDs))
	for _, runID := range cancelledIDs {
		cancelled[runID] = struct{}{}
	}
	for _, runID := range requestedIDs {
		if _, duplicate := cancelled[runID]; duplicate {
			return false
		}
	}
	return true
}

func validateCascadeCurrentRuns(runs []*DriverRun, committedIDs []string, command CascadeChildDriverRunsCommand, cancelledRuns map[string]*DriverRun, cancelled, replay bool) error {
	if len(runs) != len(committedIDs) {
		return ErrConflict
	}
	current := make(map[string]*DriverRun, len(runs))
	for _, run := range runs {
		if !validCascadeCurrentRun(run, command, cancelledRuns, cancelled, replay) {
			return ErrConflict
		}
		if _, exists := current[run.RunID]; exists {
			return ErrConflict
		}
		current[run.RunID] = run
	}
	for _, runID := range committedIDs {
		if current[runID] == nil {
			return ErrConflict
		}
	}
	return nil
}

func validCascadeCurrentRun(
	run *DriverRun,
	command CascadeChildDriverRunsCommand,
	cancelledRuns map[string]*DriverRun,
	cancelled, replay bool,
) bool {
	if run == nil || run.WorkspaceKey != command.WorkspaceKey || strings.TrimSpace(run.RunID) == "" ||
		!cascadeRunRootedAtParent(run, command.ParentRunID, cancelledRuns, command.MaxDepth) {
		return false
	}
	if cancelled {
		return run.Status == DriverRunCancelled && run.ErrorClass == command.ErrorClass && run.Summary == command.Reason
	}
	if !replay {
		return run.Status == DriverRunRunning && run.CancelRequestedAt != nil && run.CancelRequestedReason == command.Reason
	}
	return run.Status.IsTerminal() || (run.CancelRequestedAt != nil && run.CancelRequestedReason == command.Reason)
}

func cascadeRunRootedAtParent(run *DriverRun, rootRunID string, cancelledRuns map[string]*DriverRun, maxDepth int) bool {
	if run == nil || strings.TrimSpace(rootRunID) == "" || run.RunID == rootRunID || maxDepth < 1 {
		return false
	}
	seen := map[string]struct{}{run.RunID: {}}
	parentID := run.ParentRunID
	for depth := 1; depth <= maxDepth; depth++ {
		if parentID == rootRunID {
			return true
		}
		if strings.TrimSpace(parentID) == "" {
			return false
		}
		if _, cycle := seen[parentID]; cycle {
			return false
		}
		seen[parentID] = struct{}{}
		parent := cancelledRuns[parentID]
		if parent == nil {
			return false
		}
		parentID = parent.ParentRunID
	}
	return false
}

func canonicalDriverRunIDs(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		set[value] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	slices.Sort(out)
	return out
}

func (service *Service) ClaimDriverRun(ctx context.Context, auth authority.SystemAuthority, command ClaimDriverRunCommand) (*DriverRun, error) {
	if err := service.requireSystem(ActionClaimDriverRun, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if strings.TrimSpace(command.RequestID) == "" || strings.TrimSpace(command.RunID) == "" ||
		strings.TrimSpace(command.NodeID) == "" || strings.TrimSpace(command.LeaseID) == "" || strings.TrimSpace(command.LeaseToken) == "" {
		return nil, ErrInvalid
	}
	port := service.dependencies.DriverRuns.Claims
	if port == nil {
		return nil, ErrUnavailable
	}
	run, err := port.ClaimDriverRun(ctx, command)
	if err != nil {
		return nil, err
	}
	if run == nil || run.Status != DriverRunRunning || run.RunID != command.RunID || run.WorkspaceKey != command.WorkspaceKey ||
		run.Owner.ResourceKind != ResourceDriverRun || run.Owner.ResourceID != command.RunID ||
		run.Owner.NodeID != command.NodeID || run.Owner.LeaseID != command.LeaseID ||
		run.Owner.LeaseToken != command.LeaseToken || run.Owner.FencingToken <= 0 {
		return nil, fmt.Errorf("%w: claimed DriverRun escaped requested owner envelope", ErrConflict)
	}
	return cloneDriverRun(run), nil
}

func (service *Service) HeartbeatDriverRun(ctx context.Context, auth authority.ExecutionAuthority, command DriverRunHeartbeatCommand) (*DriverRun, error) {
	if err := service.requireOwner(ActionHeartbeatDriverRun, command.WorkspaceKey, command.Owner, auth); err != nil {
		return nil, err
	}
	if command.Owner.ResourceKind != ResourceDriverRun || command.At.IsZero() {
		return nil, ErrInvalid
	}
	port := service.dependencies.DriverRuns.Heartbeats
	if port == nil {
		return nil, ErrUnavailable
	}
	run, err := port.HeartbeatDriverRun(ctx, command)
	if err != nil {
		return nil, err
	}
	if err := validateOwnedDriverRun(command.WorkspaceKey, command.Owner, run); err != nil {
		return nil, err
	}
	return cloneDriverRun(run), nil
}

func (service *Service) ClaimDriverRunWorkItem(
	ctx context.Context,
	auth authority.ExecutionAuthority,
	command ClaimDriverRunWorkItemCommand,
) (DriverRunWorkItemMutationResult, error) {
	if err := service.requireOwner(ActionClaimDriverRunWorkItem, command.WorkspaceKey, command.Owner, auth); err != nil {
		return DriverRunWorkItemMutationResult{}, err
	}
	command.RequiredStatus = strings.TrimSpace(command.RequiredStatus)
	if command.Owner.ResourceKind != ResourceDriverRun || strings.TrimSpace(command.WorkItemID) == "" ||
		command.RequestID != ClaimDriverRunWorkItemRequestID(command.Owner.ResourceID, command.WorkItemID) ||
		(command.RequiredStatus != "" && command.RequiredStatus != DriverRunWorkItemRestoreReview) ||
		command.ClaimTTL < 0 || (command.ClaimTTL > 0 && command.ClaimTTL < time.Second) || command.ClaimedAt.IsZero() {
		return DriverRunWorkItemMutationResult{}, ErrInvalid
	}
	port := service.dependencies.DriverRuns.WorkItems
	if port == nil {
		return DriverRunWorkItemMutationResult{}, ErrUnavailable
	}
	result, err := port.ClaimDriverRunWorkItem(ctx, command)
	if err != nil {
		return DriverRunWorkItemMutationResult{}, err
	}
	if err := validateDriverRunWorkItemMutationResult(result, command.WorkspaceKey, command.Owner.ResourceID,
		command.WorkItemID, command.RequestID, "claim_work_item", "claimed", command.ClaimedAt,
		command.RequiredStatus, true); err != nil {
		return DriverRunWorkItemMutationResult{}, fmt.Errorf("%w: claimed Work Item escaped DriverRun owner envelope", ErrConflict)
	}
	return cloneDriverRunWorkItemMutationResult(result), nil
}

func (service *Service) ReleaseDriverRunWorkItem(
	ctx context.Context,
	auth authority.ExecutionAuthority,
	command ReleaseDriverRunWorkItemCommand,
) (DriverRunWorkItemMutationResult, error) {
	if err := service.requireOwner(ActionReleaseDriverRunWorkItem, command.WorkspaceKey, command.Owner, auth); err != nil {
		return DriverRunWorkItemMutationResult{}, err
	}
	command.RestoreStatus = strings.TrimSpace(command.RestoreStatus)
	if command.RestoreStatus == "" {
		command.RestoreStatus = DriverRunWorkItemRestoreOpen
	}
	claimRequestID := ClaimDriverRunWorkItemRequestID(command.Owner.ResourceID, command.WorkItemID)
	if command.Owner.ResourceKind != ResourceDriverRun || strings.TrimSpace(command.WorkItemID) == "" ||
		command.RequestID != ReleaseDriverRunWorkItemRequestID(command.Owner.ResourceID, command.WorkItemID) ||
		command.ClaimActionID != DriverRunWorkItemClaimActionID(claimRequestID) ||
		(command.RestoreStatus != DriverRunWorkItemRestoreOpen && command.RestoreStatus != DriverRunWorkItemRestoreReview) ||
		command.ReleasedAt.IsZero() {
		return DriverRunWorkItemMutationResult{}, ErrInvalid
	}
	port := service.dependencies.DriverRuns.WorkItems
	if port == nil {
		return DriverRunWorkItemMutationResult{}, ErrUnavailable
	}
	result, err := port.ReleaseDriverRunWorkItem(ctx, command)
	if err != nil {
		return DriverRunWorkItemMutationResult{}, err
	}
	if err := validateDriverRunWorkItemMutationResult(result, command.WorkspaceKey, command.Owner.ResourceID,
		command.WorkItemID, command.RequestID, "release_work_item", "released", command.ReleasedAt,
		"", false); err != nil {
		return DriverRunWorkItemMutationResult{}, fmt.Errorf("%w: released Work Item escaped DriverRun owner envelope", ErrConflict)
	}
	return cloneDriverRunWorkItemMutationResult(result), nil
}

func validateDriverRunWorkItemMutationResult(
	result DriverRunWorkItemMutationResult,
	workspace, driverRunID, workItemID, requestID, actionType, responseState string,
	requestedAt time.Time,
	requiredStatus string,
	claim bool,
) error {
	workItem, action := result.WorkItem, result.Action
	actor := "driver-run:" + driverRunID
	if !validDriverRunWorkItemMutation(workItem, action, workspace, workItemID, actor, claim) {
		return ErrConflict
	}
	actionPrefix := "driver-run-work-item-" + strings.TrimSuffix(actionType, "_work_item") + ":"
	wantActionID := actionPrefix + requestID
	wantResponseRef := "issue://" + workItemID + "#" + responseState
	fingerprintPrefix := ""
	if claim && requiredStatus == DriverRunWorkItemRestoreReview {
		fingerprintPrefix = driverRunWorkItemReviewClaimFingerprintPrefix
	}
	if !validDriverRunWorkItemActionReceipt(
		action, workspace, workItemID, actor, actionType, wantActionID, wantResponseRef,
		requestedAt, fingerprintPrefix, result.Replay,
	) {
		return ErrConflict
	}
	return nil
}

func validDriverRunWorkItemMutation(
	workItem *DriverRunWorkItem,
	action *DriverRunWorkItemAction,
	workspace, workItemID, actor string,
	claim bool,
) bool {
	if workItem == nil || action == nil || workItem.WorkspaceKey != workspace || workItem.WorkItemID != workItemID ||
		workItem.UpdatedAt.IsZero() {
		return false
	}
	if claim {
		return workItem.Status == "in_progress" && workItem.Assignee == actor
	}
	return validReleasedDriverRunWorkItem(workItem.Status, workItem.Assignee, actor)
}

func validDriverRunWorkItemActionReceipt(
	action *DriverRunWorkItemAction,
	workspace, workItemID, actor, actionType, wantActionID, wantResponseRef string,
	requestedAt time.Time,
	fingerprintPrefix string,
	replay bool,
) bool {
	return action != nil && action.WorkspaceKey == workspace && action.ActionID == wantActionID &&
		action.IdempotencyKey == wantActionID && action.ActionType == actionType && action.TargetRef == workItemID &&
		action.RequestedBy == actor && action.Status == "applied" &&
		validDriverRunCommandFingerprintWithPrefix(action.RequestRef, fingerprintPrefix) &&
		action.ResponseRef == wantResponseRef && !action.CreatedAt.IsZero() && action.AppliedAt != nil &&
		!action.AppliedAt.IsZero() && action.AppliedAt.Equal(action.CreatedAt) &&
		(replay || persistedCommandTimeMatches(action.CreatedAt, requestedAt))
}

func validReleasedDriverRunWorkItem(status, assignee, actor string) bool {
	switch status {
	case "open", "review":
		return assignee == ""
	case "closed", "tombstone":
		return assignee == actor
	default:
		return false
	}
}

func validDriverRunCommandFingerprint(value string) bool {
	if len(value) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func validDriverRunCommandFingerprintWithPrefix(value, prefix string) bool {
	if prefix == "" {
		return validDriverRunCommandFingerprint(value)
	}
	return strings.HasPrefix(value, prefix) &&
		validDriverRunCommandFingerprint(strings.TrimPrefix(value, prefix))
}

func cloneDriverRunWorkItemMutationResult(result DriverRunWorkItemMutationResult) DriverRunWorkItemMutationResult {
	if result.WorkItem != nil {
		workItem := *result.WorkItem
		workItem.Labels = append([]string(nil), result.WorkItem.Labels...)
		result.WorkItem = &workItem
	}
	if result.Action != nil {
		action := *result.Action
		if result.Action.AppliedAt != nil {
			appliedAt := *result.Action.AppliedAt
			action.AppliedAt = &appliedAt
		}
		result.Action = &action
	}
	if result.Comment != nil {
		comment := *result.Comment
		result.Comment = &comment
	}
	return result
}

func (service *Service) FinalizeDriverRun(ctx context.Context, auth authority.ExecutionAuthority, command FinalizeDriverRunCommand) (*DriverRun, error) {
	if err := service.requireOwner(ActionFinalizeDriverRun, command.WorkspaceKey, command.Owner, auth); err != nil {
		return nil, err
	}
	if command.Owner.ResourceKind != ResourceDriverRun || strings.TrimSpace(command.RequestID) == "" ||
		!command.Status.IsTerminal() || strings.TrimSpace(command.Summary) == "" || command.FinishedAt.IsZero() {
		return nil, ErrInvalid
	}
	port := service.dependencies.DriverRuns.Finalizer
	if port == nil {
		return nil, ErrUnavailable
	}
	run, err := port.FinalizeDriverRun(ctx, cloneFinalizeDriverRunCommand(command))
	if err != nil {
		return nil, err
	}
	if run == nil || run.WorkspaceKey != command.WorkspaceKey || run.RunID != command.Owner.ResourceID || run.Status != command.Status || !run.Status.IsTerminal() {
		return nil, fmt.Errorf("%w: finalized DriverRun escaped requested terminal envelope", ErrConflict)
	}
	return cloneDriverRun(run), nil
}

func (service *Service) RecoverDriverRuns(ctx context.Context, auth authority.SystemAuthority, command RecoverDriverRunsCommand) (*DriverRunRecoveryResult, error) {
	if err := service.requireSystem(ActionRecoverDriverRuns, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	if strings.TrimSpace(command.RequestID) == "" || command.ObservedAt.IsZero() || command.MaxAge <= 0 || strings.TrimSpace(command.Summary) == "" {
		return nil, ErrInvalid
	}
	port := service.dependencies.DriverRuns.Recovery
	if port == nil {
		return nil, ErrUnavailable
	}
	result, err := port.RecoverDriverRuns(ctx, command)
	if err != nil {
		return nil, err
	}
	if result == nil || result.WorkspaceKey != command.WorkspaceKey || result.Recovered < 0 || result.SkippedFresh < 0 {
		return nil, fmt.Errorf("%w: invalid DriverRun recovery result", ErrConflict)
	}
	cloned := *result
	cloned.RecoveredRunIDs = append([]string(nil), result.RecoveredRunIDs...)
	cloned.SkippedFreshRunIDs = append([]string(nil), result.SkippedFreshRunIDs...)
	return &cloned, nil
}

func (service *Service) AwaitDriverRun(ctx context.Context, auth authority.ExecutionAuthority, command AwaitDriverRunCommand) (*DriverAwaitResult, error) {
	if err := service.requireOwner(ActionAwaitDriverRun, command.WorkspaceKey, command.Owner, auth); err != nil {
		return nil, err
	}
	if command.Owner.ResourceKind != ResourceDriverRun || strings.TrimSpace(command.RequestID) == "" {
		return nil, ErrInvalid
	}
	port := service.dependencies.DriverRuns.Awaits
	if port == nil {
		return nil, ErrUnavailable
	}
	registration, registered, err := service.registerDriverAwait(ctx, port, command)
	if err != nil {
		return nil, err
	}
	if registered.Satisfied {
		return &DriverAwaitResult{Status: string(registered.Instance.Status), Instance: cloneDriverAwait(registered.Instance), Replay: true}, nil
	}
	run, replayed, err := suspendDriverAwait(ctx, port, command, registration)
	if err != nil {
		return nil, err
	}
	if replayed != nil {
		return replayed, nil
	}
	// Backends without a pending-resume marker need this post-suspend close of
	// the accepted resolution window. A missing row means the await is still
	// pending; other failures remain recoverable by the deadline dispatcher.
	closeSatisfiedDriverAwait(ctx, port, command, registration.InstanceKey)
	return &DriverAwaitResult{Status: DriverAwaitOutcomeSuspended, Instance: cloneDriverAwait(registered.Instance), Run: cloneDriverRun(run)}, nil
}

func (service *Service) registerDriverAwait(
	ctx context.Context,
	port DriverAwaitPort,
	command AwaitDriverRunCommand,
) (DriverAwaitRegistration, *DriverAwaitRegistrationResult, error) {
	registration, err := service.driverAwaitRegistration(ctx, port, command)
	if err != nil {
		return DriverAwaitRegistration{}, nil, err
	}
	registered, err := port.RegisterAndCheckDriverAwait(ctx, command.WorkspaceKey, registration)
	if err != nil {
		return DriverAwaitRegistration{}, nil, err
	}
	if registered == nil || registered.Instance == nil || registered.Instance.InstanceKey != registration.InstanceKey ||
		registered.Instance.RunID != command.Owner.ResourceID {
		return DriverAwaitRegistration{}, nil, fmt.Errorf("%w: invalid persisted await registration", ErrConflict)
	}
	return registration, registered, nil
}

func suspendDriverAwait(
	ctx context.Context,
	port DriverAwaitPort,
	command AwaitDriverRunCommand,
	registration DriverAwaitRegistration,
) (*DriverRun, *DriverAwaitResult, error) {
	run, err := port.SuspendDriverRun(ctx, command.WorkspaceKey, command.Owner, registration.InstanceKey)
	if errors.Is(err, ErrAlreadyResumed) {
		instance, replayErr := port.GetSatisfiedDriverAwait(ctx, command.WorkspaceKey, registration.InstanceKey)
		if replayErr != nil {
			return nil, nil, fmt.Errorf("replay resolved await %s: %w", registration.InstanceKey, replayErr)
		}
		return nil, &DriverAwaitResult{Status: string(instance.Status), Instance: cloneDriverAwait(instance), Replay: true}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if run == nil || run.Status != DriverRunSuspendedAwait || run.RunID != command.Owner.ResourceID ||
		run.AwaitInstanceKey != registration.InstanceKey {
		return nil, nil, fmt.Errorf("%w: invalid suspended DriverRun result", ErrConflict)
	}
	return run, nil, nil
}

func closeSatisfiedDriverAwait(ctx context.Context, port DriverAwaitPort, command AwaitDriverRunCommand, instanceKey string) {
	satisfied, err := port.GetSatisfiedDriverAwait(ctx, command.WorkspaceKey, instanceKey)
	if err == nil && satisfied != nil {
		_, _ = port.ResumeAwaitingDriverRun(ctx, command.WorkspaceKey, command.Owner.ResourceID, instanceKey, satisfied.SatisfiedByEventID)
	}
}

func (service *Service) ResolveDriverAwait(ctx context.Context, auth authority.SystemAuthority, command ResolveDriverAwaitCommand) error {
	if err := service.requireSystem(ActionResolveDriverAwait, command.WorkspaceKey, auth); err != nil {
		return err
	}
	if strings.TrimSpace(command.RequestID) == "" || strings.TrimSpace(command.InstanceKey) == "" ||
		strings.TrimSpace(command.EventID) == "" || strings.TrimSpace(command.Actor) == "" ||
		(len(command.Payload) > 0 && !json.Valid(command.Payload)) {
		return ErrInvalid
	}
	port := service.dependencies.DriverRuns.Resolutions
	if port == nil {
		return ErrUnavailable
	}
	command.Payload = append(json.RawMessage(nil), command.Payload...)
	return port.ResolveAndResumeDriverAwait(ctx, command)
}

func (service *Service) driverAwaitRegistration(ctx context.Context, port DriverAwaitPort, command AwaitDriverRunCommand) (DriverAwaitRegistration, error) {
	if !validDriverAwaitCommand(ctx, command) || !validDriverAwaitActors(command.ActorAllow) {
		return DriverAwaitRegistration{}, ErrInvalid
	}
	maxTimeout, maxPerRun, totalCap := driverAwaitLimits(command)
	if command.Timeout > maxTimeout || command.AwaitIndex > maxPerRun {
		return DriverAwaitRegistration{}, ErrInvalid
	}
	prior, err := priorDriverAwaitDuration(ctx, port, command)
	if err != nil {
		return DriverAwaitRegistration{}, err
	}
	if prior+command.Timeout > totalCap {
		return DriverAwaitRegistration{}, ErrInvalid
	}
	registeredAt := command.RegisteredAt.UTC()
	if registeredAt.IsZero() {
		registeredAt = time.Now().UTC()
	}
	return DriverAwaitRegistration{
		InstanceKey: driverAwaitInstanceKey(command.Owner.ResourceID, command.AwaitIndex), RunID: command.Owner.ResourceID,
		Pattern: command.Pattern, ActorAllow: append([]string(nil), command.ActorAllow...),
		Deadline: registeredAt.Add(command.Timeout), RegisteredAt: registeredAt,
	}, nil
}

func validDriverAwaitCommand(ctx context.Context, command AwaitDriverRunCommand) bool {
	return ctx != nil && ctx.Err() == nil && command.AwaitIndex >= 1 && command.Timeout > 0 && validDriverAwaitPattern(command.Pattern)
}

func validDriverAwaitActors(actors []string) bool {
	for _, actor := range actors {
		if strings.TrimSpace(actor) == "" || actor != strings.TrimSpace(actor) ||
			strings.ContainsFunc(actor, func(r rune) bool { return r < 0x20 }) {
			return false
		}
	}
	return true
}

func driverAwaitLimits(command AwaitDriverRunCommand) (time.Duration, int, time.Duration) {
	maxTimeout := command.MaxTimeout
	if maxTimeout <= 0 {
		maxTimeout = defaultDriverAwaitMaxTimeout
	}
	maxPerRun := command.MaxPerRun
	if maxPerRun <= 0 {
		maxPerRun = defaultDriverAwaitMaxPerRun
	}
	totalCap := command.TotalSuspendCap
	if totalCap <= 0 {
		totalCap = defaultDriverAwaitTotalSuspendCap
	}
	return maxTimeout, maxPerRun, totalCap
}

func priorDriverAwaitDuration(ctx context.Context, port DriverAwaitPort, command AwaitDriverRunCommand) (time.Duration, error) {
	var prior time.Duration
	for index := 1; index < command.AwaitIndex; index++ {
		instance, err := port.GetSatisfiedDriverAwait(ctx, command.WorkspaceKey, driverAwaitInstanceKey(command.Owner.ResourceID, index))
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return 0, err
		}
		if instance != nil && instance.ResumedAt != nil && instance.ResumedAt.After(instance.RegisteredAt) {
			prior += instance.ResumedAt.Sub(instance.RegisteredAt)
		}
	}
	return prior, nil
}

func (service *Service) requireOperator(action authority.Action, workspace string, auth authority.OperatorAuthority) error {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return ErrInvalid
	}
	if service == nil || service.admission == nil {
		return ErrUnavailable
	}
	return service.admission.RequireOperator(action, workspace, auth)
}

func validateSubmittedDriverRun(command SubmitDriverRunCommand, run *DriverRun) error {
	if run == nil || run.WorkspaceKey != command.WorkspaceKey || run.RunID != command.RunID || run.DriverID != command.DriverID ||
		run.DriverVersionID != command.DriverVersionID || !validPersistedDriverRunStatus(run.Status) || run.IdempotencyKey != command.RequestID {
		if run == nil {
			return fmt.Errorf("%w: submitted DriverRun escaped requested envelope: no run returned", ErrConflict)
		}
		return fmt.Errorf(
			"%w: submitted DriverRun escaped requested envelope: got workspace=%q run=%q driver=%q version=%q status=%q request=%q",
			ErrConflict, run.WorkspaceKey, run.RunID, run.DriverID, run.DriverVersionID, run.Status, run.IdempotencyKey,
		)
	}
	return nil
}

func validPersistedDriverRunStatus(status DriverRunStatus) bool {
	switch status {
	case DriverRunQueued, DriverRunRunning, DriverRunCompleted, DriverRunFailed,
		DriverRunNeedsReview, DriverRunCancelled, DriverRunSuspendedAwait:
		return true
	default:
		return false
	}
}

func validateOwnedDriverRun(workspace string, owner Owner, run *DriverRun) error {
	if run == nil || run.WorkspaceKey != workspace || run.RunID != owner.ResourceID || run.Status != DriverRunRunning ||
		run.Owner.ResourceKind != ResourceDriverRun || run.Owner.ResourceID != owner.ResourceID ||
		run.Owner.NodeID != owner.NodeID || run.Owner.LeaseID != owner.LeaseID ||
		run.Owner.LeaseToken != owner.LeaseToken || run.Owner.FencingToken != owner.FencingToken {
		return fmt.Errorf("%w: persisted DriverRun owner changed", ErrFenceConflict)
	}
	return nil
}

func validDriverAwaitPattern(pattern string) bool {
	eventType, subject, ok := strings.Cut(pattern, ":")
	return ok && strings.TrimSpace(eventType) != "" && strings.TrimSpace(subject) != ""
}

func driverAwaitInstanceKey(runID string, index int) string {
	return fmt.Sprintf("%s#await-%d", runID, index)
}

func ChildDriverRunID(parentRunID, childKey string) string {
	digest := sha256.Sum256([]byte("loom-child:" + parentRunID + ":" + childKey))
	return "run-" + hex.EncodeToString(digest[:16])
}

func ChildDriverRunRequestID(parentRunID, childKey string) string {
	return "workflow-start:" + parentRunID + ":" + childKey
}

func CascadeChildDriverRunsRequestID(parentRunID string, status DriverRunStatus) string {
	digest := sha256.Sum256([]byte("driver-run-child-cascade:" + strings.TrimSpace(parentRunID) + ":" + string(status)))
	return "driver-run-cascade:" + hex.EncodeToString(digest[:16])
}

func cloneSubmitDriverRunCommand(command SubmitDriverRunCommand) SubmitDriverRunCommand {
	command.Payload = append(json.RawMessage(nil), command.Payload...)
	return command
}

func cloneFinalizeDriverRunCommand(command FinalizeDriverRunCommand) FinalizeDriverRunCommand {
	command.Output = cloneDriverRunStringMap(command.Output)
	return command
}

func cloneCascadeChildDriverRunsCommand(command CascadeChildDriverRunsCommand) CascadeChildDriverRunsCommand {
	return command
}

func publicCascadeChildDriverRunsResult(result CascadeChildDriverRunsResult) CascadeChildDriverRunsResult {
	result.CancelledRuns = clonePublicDriverRuns(result.CancelledRuns)
	result.CancelRequestedRuns = clonePublicDriverRuns(result.CancelRequestedRuns)
	if result.Committed != nil {
		commit := *result.Committed
		commit.CancelledRunIDs = append([]string(nil), result.Committed.CancelledRunIDs...)
		commit.CancelRequestedRunIDs = append([]string(nil), result.Committed.CancelRequestedRunIDs...)
		result.Committed = &commit
	}
	return result
}

func clonePublicDriverRuns(runs []*DriverRun) []*DriverRun {
	if len(runs) == 0 {
		return nil
	}
	cloned := make([]*DriverRun, 0, len(runs))
	for _, run := range runs {
		copy := cloneDriverRun(run)
		if copy != nil {
			copy.Owner = Owner{}
		}
		cloned = append(cloned, copy)
	}
	return cloned
}

func cloneDriverRun(run *DriverRun) *DriverRun {
	if run == nil {
		return nil
	}
	cloned := *run
	cloned.Owner = publicOwner(run.Owner)
	cloned.Payload = append(json.RawMessage(nil), run.Payload...)
	cloned.Output = cloneDriverRunStringMap(run.Output)
	return &cloned
}

func cloneDriverAwait(instance *DriverAwaitInstance) *DriverAwaitInstance {
	if instance == nil {
		return nil
	}
	cloned := *instance
	cloned.ActorAllow = append([]string(nil), instance.ActorAllow...)
	cloned.SatisfiedPayload = append(json.RawMessage(nil), instance.SatisfiedPayload...)
	return &cloned
}

func cloneDriverRunStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
