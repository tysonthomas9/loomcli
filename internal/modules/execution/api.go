package execution

import (
	"context"
	"errors"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

var (
	ErrInvalid         = errors.New("execution: invalid command")
	ErrUnavailable     = errors.New("execution: dependency unavailable")
	ErrConflict        = errors.New("execution: command conflict")
	ErrNotFound        = errors.New("execution: record not found")
	ErrFenceConflict   = errors.New("execution: owner fence conflict")
	ErrAlreadyResumed  = errors.New("execution: driver run already resumed for await")
	ErrPreflightFailed = errors.New("execution: preflight failed")
	ErrUnschedulable   = errors.New("execution: task run unschedulable")
	// ErrTaskRunRequestReplayNotFound is an internal read-only replay miss.
	// Request ports return it without writes so the service can run live
	// scheduling only for a genuinely new TaskRun request.
	ErrTaskRunRequestReplayNotFound = errors.New("execution: task run request replay not found")
	ErrLaunchFailed                 = errors.New("execution: launch failed")
	ErrInvalidTransition            = errors.New("execution: invalid transition")
	ErrCompositionDepthExceeded     = errors.New("execution: composition depth exceeded")
)

const (
	ActionPreflight            authority.Action = "execution.preflight"
	ActionClaimAndLaunch       authority.Action = "execution.claim-and-launch"
	ActionHeartbeat            authority.Action = "execution.heartbeat"
	ActionAppendLog            authority.Action = "execution.append-log"
	ActionClassify             authority.Action = "execution.classify"
	ActionFinalize             authority.Action = "execution.finalize"
	ActionRecover              authority.Action = "execution.recover"
	ActionAwait                authority.Action = "execution.await"
	ActionResolveTrustedRunner authority.Action = "execution.resolve-trusted-runner"

	ActionClaimAwaitEventNotifications          authority.Action = "execution.claim-await-event-notifications"
	ActionCompleteAwaitEventNotification        authority.Action = "execution.complete-await-event-notification"
	ActionRetryAwaitEventNotification           authority.Action = "execution.retry-await-event-notification"
	ActionClaimDriverRunOutcomes                authority.Action = "execution.claim-driver-run-outcomes"
	ActionCompleteDriverRunOutcome              authority.Action = "execution.complete-driver-run-outcome"
	ActionRetryDriverRunOutcome                 authority.Action = "execution.retry-driver-run-outcome"
	ActionClaimTerminalDriverRunWorkRecoveries  authority.Action = "execution.claim-terminal-driver-run-work-recoveries"
	ActionCompleteTerminalDriverRunWorkRecovery authority.Action = "execution.complete-terminal-driver-run-work-recovery"
	ActionRetryTerminalDriverRunWorkRecovery    authority.Action = "execution.retry-terminal-driver-run-work-recovery"
)

const (
	DaytonaProviderSchemaV1 = "daytona-task-run-execution.v1"
	RunnerKindFlueWorkflow  = "flue-workflow"
	RunnerKindNodeModule    = "node-module"
	runnerManifestKey       = "runners"
)

func OperationRules() []authority.OperationRule {
	return []authority.OperationRule{
		authority.Allow(ActionPreflight, authority.ClassSystem),
		authority.Allow(ActionClaimAndLaunch, authority.ClassSystem),
		authority.Allow(ActionHeartbeat, authority.ClassExecution),
		authority.Allow(ActionAppendLog, authority.ClassExecution),
		authority.Allow(ActionClassify, authority.ClassExecution),
		authority.Allow(ActionFinalize, authority.ClassExecution),
		authority.Allow(ActionRecover, authority.ClassSystem),
		authority.Allow(ActionAwait, authority.ClassExecution),
		authority.Allow(ActionResolveTrustedRunner, authority.ClassSystem),
		authority.Allow(ActionRequestTaskRun, authority.ClassExecution),
		authority.Allow(ActionClaimTaskRun, authority.ClassSystem),
		authority.Allow(ActionUpdateTaskRunWorkItemDesign, authority.ClassExecution),
		authority.Allow(ActionRequeueTaskRun, authority.ClassExecution),
		authority.Allow(ActionExhaustTaskRunRetries, authority.ClassExecution),
		authority.Allow(ActionRegisterWorkerNode, authority.ClassSystem),
		authority.Allow(ActionHeartbeatWorkerNode, authority.ClassSystem),
		authority.Allow(ActionSetWorkerNodeDrain, authority.ClassSystem),
		authority.OperatorOnly(ActionCreateWorkerProfile),
		authority.OperatorOnly(ActionUpdateWorkerProfile),
		authority.OperatorOnly(ActionDeleteWorkerProfile),
		authority.Allow(ActionConvergeTaskRun, authority.ClassSystem),
		authority.Allow(ActionRepairTerminalDriverStep, authority.ClassSystem),
		authority.Allow(ActionRecoverStaleChildTaskRuns, authority.ClassExecution),
		authority.Allow(ActionClaimAwaitEventNotifications, authority.ClassSystem),
		authority.Allow(ActionCompleteAwaitEventNotification, authority.ClassSystem),
		authority.Allow(ActionRetryAwaitEventNotification, authority.ClassSystem),
		authority.Allow(ActionClaimDriverRunOutcomes, authority.ClassSystem),
		authority.Allow(ActionCompleteDriverRunOutcome, authority.ClassSystem),
		authority.Allow(ActionRetryDriverRunOutcome, authority.ClassSystem),
		authority.Allow(ActionClaimTerminalDriverRunWorkRecoveries, authority.ClassSystem),
		authority.Allow(ActionCompleteTerminalDriverRunWorkRecovery, authority.ClassSystem),
		authority.Allow(ActionRetryTerminalDriverRunWorkRecovery, authority.ClassSystem),
	}
}

type API interface {
	Preflight(context.Context, authority.SystemAuthority, PreflightCommand) (PreflightResult, error)
	ClaimAndLaunch(context.Context, authority.SystemAuthority, ClaimAndLaunchCommand) (ClaimAndLaunchResult, error)
	Heartbeat(context.Context, authority.ExecutionAuthority, HeartbeatCommand) (HeartbeatResult, error)
	AppendLog(context.Context, authority.ExecutionAuthority, AppendLogCommand) (LogEntry, error)
	Classify(context.Context, authority.ExecutionAuthority, ClassifyCommand) (ExitClassification, error)
	Finalize(context.Context, authority.ExecutionAuthority, FinalizeCommand) (FinalizeResult, error)
	Recover(context.Context, authority.SystemAuthority, RecoverCommand) (RecoverResult, error)
	Await(context.Context, authority.ExecutionAuthority, AwaitCommand) (AwaitResult, error)
}

// TaskRunAPI is the narrow mutation surface used by a running TaskRun. It
// intentionally excludes system claim/recovery operations and DriverRun
// lifecycle commands.
type TaskRunAPI interface {
	Heartbeat(context.Context, authority.ExecutionAuthority, HeartbeatCommand) (HeartbeatResult, error)
	AppendLog(context.Context, authority.ExecutionAuthority, AppendLogCommand) (LogEntry, error)
	UpdateWorkItemDesign(context.Context, authority.ExecutionAuthority, UpdateTaskRunWorkItemDesignCommand) (UpdateTaskRunWorkItemDesignResult, error)
	Finalize(context.Context, authority.ExecutionAuthority, FinalizeCommand) (FinalizeResult, error)
}

// TaskRunAuthorityResolver is an inbound composition seam. The transport
// supplies the request-derived owner tuple and chooses one exact action; the
// resolver returns an opaque authority bound to that tuple. The outbound
// TaskRun port remains authoritative for validating LeaseToken.
type TaskRunAuthorityResolver interface {
	ResolveTaskRunAuthority(context.Context, string, authority.Action, Owner) (authority.ExecutionAuthority, error)
}
