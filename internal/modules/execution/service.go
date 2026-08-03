package execution

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type Dependencies struct {
	Preflight              PreflightPort
	Claims                 ClaimStartPort
	Launcher               Launcher
	Heartbeats             HeartbeatPort
	Logs                   LogPort
	Classifier             Classifier
	Finalizer              FinalizePort
	Recovery               RecoveryPort
	Awaits                 AwaitPort
	DriverRuns             DriverRunDependencies
	TaskRuns               TaskRunDependencies
	Workers                WorkerDependencies
	Convergence            TaskRunConvergenceDependencies
	TaskRunRecovery        TaskRunRecoveryDependencies
	AwaitEvents            AwaitEventNotificationQueuePort
	RunOutcomes            DriverRunOutcomeQueuePort
	TerminalWorkRecoveries TerminalDriverRunWorkRecoveryQueuePort
	OutboxDeliveries       OutboxDeliveryPort
}

type Service struct {
	dependencies Dependencies
	admission    *authority.Admission
}

var _ API = (*Service)(nil)

func New(dependencies Dependencies, admission *authority.Admission) (*Service, error) {
	if admission == nil {
		return nil, fmt.Errorf("%w: admission registry is required", ErrUnavailable)
	}
	return &Service{dependencies: dependencies, admission: admission}, nil
}

func (service *Service) Preflight(ctx context.Context, auth authority.SystemAuthority, command PreflightCommand) (PreflightResult, error) {
	if err := service.requireSystem(ActionPreflight, command.WorkspaceKey, auth); err != nil {
		return PreflightResult{}, err
	}
	if strings.TrimSpace(command.RequestID) == "" || strings.TrimSpace(command.RunnerRef) == "" {
		return PreflightResult{}, ErrInvalid
	}
	if service.dependencies.Preflight == nil {
		return PreflightResult{}, ErrUnavailable
	}
	return service.dependencies.Preflight.Preflight(ctx, command)
}

func (service *Service) ClaimAndLaunch(ctx context.Context, auth authority.SystemAuthority, command ClaimAndLaunchCommand) (ClaimAndLaunchResult, error) {
	if err := service.requireSystem(ActionClaimAndLaunch, command.WorkspaceKey, auth); err != nil {
		return ClaimAndLaunchResult{}, err
	}
	if err := validateClaimCommand(command); err != nil {
		return ClaimAndLaunchResult{}, err
	}
	if service.dependencies.Claims == nil || service.dependencies.Launcher == nil {
		return ClaimAndLaunchResult{}, ErrUnavailable
	}
	claim, err := service.dependencies.Claims.ClaimAndStart(ctx, command)
	if err != nil {
		return ClaimAndLaunchResult{}, err
	}
	if err := validateClaimResult(command, claim); err != nil {
		return ClaimAndLaunchResult{}, err
	}
	receipt, launchErr := service.dependencies.Launcher.Launch(ctx, claim, append([]byte(nil), command.Input...))
	if launchErr == nil {
		return ClaimAndLaunchResult{Claim: publicClaimStart(claim), Launch: receipt}, nil
	}
	classification := ExitClassification{Status: StatusFailed, ErrorClass: "launch_failed", Retryable: true, Summary: launchErr.Error()}
	if service.dependencies.Classifier != nil {
		if classified, classifyErr := service.dependencies.Classifier.Classify(ctx, ClassifyCommand{
			WorkspaceKey: command.WorkspaceKey, Owner: claim.Owner, ExitCode: -1, BackendError: launchErr.Error(),
		}); classifyErr == nil {
			classification = classified
		}
	}
	if err := service.dependencies.Claims.RecordLaunchFailure(ctx, claim, classification); err != nil {
		return ClaimAndLaunchResult{Claim: publicClaimStart(claim), Outcome: classification}, fmt.Errorf("%w: launch: %v; record failure: %v", ErrLaunchFailed, launchErr, err)
	}
	return ClaimAndLaunchResult{Claim: publicClaimStart(claim), Outcome: classification}, fmt.Errorf("%w: %v", ErrLaunchFailed, launchErr)
}

func (service *Service) Heartbeat(ctx context.Context, auth authority.ExecutionAuthority, command HeartbeatCommand) (HeartbeatResult, error) {
	if err := service.requireOwner(ActionHeartbeat, command.WorkspaceKey, command.Owner, auth); err != nil {
		return HeartbeatResult{}, err
	}
	if command.At.IsZero() {
		return HeartbeatResult{}, ErrInvalid
	}
	if service.dependencies.Heartbeats == nil {
		return HeartbeatResult{}, ErrUnavailable
	}
	result, err := service.dependencies.Heartbeats.Heartbeat(ctx, command)
	result.Owner = publicOwner(result.Owner)
	return result, err
}

func (service *Service) AppendLog(ctx context.Context, auth authority.ExecutionAuthority, command AppendLogCommand) (LogEntry, error) {
	if err := service.requireOwner(ActionAppendLog, command.WorkspaceKey, command.Owner, auth); err != nil {
		return LogEntry{}, err
	}
	command.RequestID = strings.TrimSpace(command.RequestID)
	if command.RequestID == "" || command.Timestamp.IsZero() {
		return LogEntry{}, ErrInvalid
	}
	if service.dependencies.Logs == nil {
		return LogEntry{}, ErrUnavailable
	}
	return service.dependencies.Logs.AppendLog(ctx, command)
}

func (service *Service) Classify(ctx context.Context, auth authority.ExecutionAuthority, command ClassifyCommand) (ExitClassification, error) {
	if err := service.requireOwner(ActionClassify, command.WorkspaceKey, command.Owner, auth); err != nil {
		return ExitClassification{}, err
	}
	if service.dependencies.Classifier == nil {
		return ExitClassification{}, ErrUnavailable
	}
	return service.dependencies.Classifier.Classify(ctx, command)
}

func (service *Service) Finalize(ctx context.Context, auth authority.ExecutionAuthority, command FinalizeCommand) (FinalizeResult, error) {
	if err := service.requireOwner(ActionFinalize, command.WorkspaceKey, command.Owner, auth); err != nil {
		return FinalizeResult{}, err
	}
	command.RequiredArtifactIDs = canonicalArtifactIDs(command.RequiredArtifactIDs)
	if strings.TrimSpace(command.RequestID) == "" || !terminal(command.Classification.Status) || command.FinishedAt.IsZero() ||
		!validTaskRunUsageValues(command.InputTokens, command.OutputTokens, command.CacheReadTokens,
			command.CacheWriteTokens, command.EstimatedCostUSD) {
		return FinalizeResult{}, ErrInvalid
	}
	if service.dependencies.Finalizer == nil {
		return FinalizeResult{}, ErrUnavailable
	}
	result, err := service.dependencies.Finalizer.Finalize(ctx, command)
	result.Owner = publicOwner(result.Owner)
	return result, err
}

func (service *Service) Recover(ctx context.Context, auth authority.SystemAuthority, command RecoverCommand) (RecoverResult, error) {
	if err := service.requireSystem(ActionRecover, command.WorkspaceKey, auth); err != nil {
		return RecoverResult{}, err
	}
	if strings.TrimSpace(command.RequestID) == "" || !validResource(command.ResourceKind) || strings.TrimSpace(command.ResourceID) == "" || command.ObservedAt.IsZero() || command.MaxAge <= 0 {
		return RecoverResult{}, ErrInvalid
	}
	if service.dependencies.Recovery == nil {
		return RecoverResult{}, ErrUnavailable
	}
	return service.dependencies.Recovery.Recover(ctx, command)
}

func (service *Service) Await(ctx context.Context, auth authority.ExecutionAuthority, command AwaitCommand) (AwaitResult, error) {
	if err := service.requireOwner(ActionAwait, command.WorkspaceKey, command.Owner, auth); err != nil {
		return AwaitResult{}, err
	}
	if strings.TrimSpace(command.RequestID) == "" || strings.TrimSpace(command.InstanceKey) == "" || strings.TrimSpace(command.Pattern) == "" || command.Deadline.IsZero() || !command.Deadline.After(time.Now().Add(-24*time.Hour)) {
		return AwaitResult{}, ErrInvalid
	}
	if service.dependencies.Awaits == nil {
		return AwaitResult{}, ErrUnavailable
	}
	return service.dependencies.Awaits.Register(ctx, command)
}

func (service *Service) ListDueOutboxDeliveries(
	ctx context.Context,
	auth authority.SystemAuthority,
	command ListDueOutboxDeliveriesCommand,
) ([]OutboxDelivery, error) {
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	if err := service.requireSystem(ActionListDueOutboxDeliveries, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	query := OutboxDeliveryQuery(command)
	if !validOutboxDeliveryQuery(query) {
		return nil, ErrInvalid
	}
	if service.dependencies.OutboxDeliveries == nil {
		return nil, ErrUnavailable
	}
	values, err := service.dependencies.OutboxDeliveries.ListDueOutboxDeliveries(ctx, query)
	if err != nil {
		return nil, err
	}
	return append([]OutboxDelivery(nil), values...), nil
}

func (service *Service) RecordOutboxDeliveryResult(
	ctx context.Context,
	auth authority.SystemAuthority,
	command RecordOutboxDeliveryResultCommand,
) (*OutboxDelivery, error) {
	command.WorkspaceKey = strings.TrimSpace(command.WorkspaceKey)
	command.OutboxID = strings.TrimSpace(command.OutboxID)
	if err := service.requireSystem(ActionRecordOutboxDeliveryResult, command.WorkspaceKey, auth); err != nil {
		return nil, err
	}
	result := OutboxDeliveryResult(command)
	if !validOutboxDeliveryResult(result) {
		return nil, ErrInvalid
	}
	if service.dependencies.OutboxDeliveries == nil {
		return nil, ErrUnavailable
	}
	return service.dependencies.OutboxDeliveries.RecordOutboxDeliveryResult(ctx, result)
}

func (service *Service) requireSystem(action authority.Action, workspace string, auth authority.SystemAuthority) error {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return ErrInvalid
	}
	if service == nil || service.admission == nil {
		return ErrUnavailable
	}
	return service.admission.RequireSystem(action, workspace, auth)
}

func (service *Service) requireOwner(action authority.Action, workspace string, owner Owner, auth authority.ExecutionAuthority) error {
	workspace = strings.TrimSpace(workspace)
	if workspace == "" || !validOwner(owner) {
		return ErrInvalid
	}
	if service == nil || service.admission == nil {
		return ErrUnavailable
	}
	if err := service.admission.RequireExecution(action, workspace, auth); err != nil {
		return err
	}
	wantKind := authority.ExecutionResourceKind(owner.ResourceKind)
	if auth.ResourceKind() != wantKind || auth.ResourceID() != owner.ResourceID || auth.NodeID() != owner.NodeID || auth.LeaseID() != owner.LeaseID || auth.FencingToken() != owner.FencingToken {
		return ErrFenceConflict
	}
	return nil
}

func validateClaimCommand(command ClaimAndLaunchCommand) error {
	if strings.TrimSpace(command.WorkspaceKey) == "" || strings.TrimSpace(command.RequestID) == "" || strings.TrimSpace(command.WorkItemID) == "" || strings.TrimSpace(command.TaskRunID) == "" || strings.TrimSpace(command.RunnerRef) == "" || strings.TrimSpace(command.NodeID) == "" || strings.TrimSpace(command.LeaseID) == "" || strings.TrimSpace(command.LeaseToken) == "" || command.LeaseTTL <= 0 {
		return ErrInvalid
	}
	return nil
}

func validateClaimResult(command ClaimAndLaunchCommand, claim ClaimStart) error {
	if !validOwner(claim.Owner) || claim.Owner.ResourceKind != ResourceTaskRun || claim.Owner.ResourceID != command.TaskRunID || claim.Owner.NodeID != command.NodeID || claim.Owner.LeaseID != command.LeaseID || claim.Owner.LeaseToken != command.LeaseToken || claim.WorkItemID != command.WorkItemID || claim.TaskRunID != command.TaskRunID {
		return fmt.Errorf("%w: claim result escaped requested execution envelope", ErrConflict)
	}
	return nil
}

func publicOwner(owner Owner) Owner {
	owner.LeaseToken = ""
	return owner
}

func publicClaimStart(claim ClaimStart) ClaimStart {
	claim.Owner = publicOwner(claim.Owner)
	return claim
}

func validOwner(owner Owner) bool {
	if !validResource(owner.ResourceKind) || strings.TrimSpace(owner.ResourceID) == "" || strings.TrimSpace(owner.NodeID) == "" || strings.TrimSpace(owner.LeaseID) == "" || strings.TrimSpace(owner.LeaseToken) == "" || owner.FencingToken <= 0 {
		return false
	}
	return true
}

func validResource(kind ResourceKind) bool {
	return kind == ResourceDriverRun || kind == ResourceTaskRun
}

func terminal(status Status) bool {
	switch status {
	case StatusSucceeded, StatusFailed, StatusBlocked, StatusCancelled:
		return true
	default:
		return false
	}
}
