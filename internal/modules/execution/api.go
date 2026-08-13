package execution

import (
	"context"
	"errors"
	"strings"
	"time"

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
	ActionPreflight                  authority.Action = "execution.preflight"
	ActionClaimAndLaunch             authority.Action = "execution.claim-and-launch"
	ActionHeartbeat                  authority.Action = "execution.heartbeat"
	ActionAppendLog                  authority.Action = "execution.append-log"
	ActionClassify                   authority.Action = "execution.classify"
	ActionFinalize                   authority.Action = "execution.finalize"
	ActionRecover                    authority.Action = "execution.recover"
	ActionAwait                      authority.Action = "execution.await"
	ActionResolveTrustedRunner       authority.Action = "execution.resolve-trusted-runner"
	ActionListDueOutboxDeliveries    authority.Action = "execution.list-due-outbox-deliveries"
	ActionRecordOutboxDeliveryResult authority.Action = "execution.record-outbox-delivery-result"

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

type OutboxKind string

const (
	OutboxKindLeadAssignment  OutboxKind = "leadAssignment"
	OutboxKindLeadTaskMessage OutboxKind = "leadTaskMessage"
)

type OutboxDeliveryStatus string

const (
	OutboxDeliveryStatusPending     OutboxDeliveryStatus = "pending"
	OutboxDeliveryStatusDelivered   OutboxDeliveryStatus = "delivered"
	OutboxDeliveryStatusUnsupported OutboxDeliveryStatus = "unsupported"
	OutboxDeliveryStatusFailed      OutboxDeliveryStatus = "failed"
)

type ListDueOutboxDeliveriesCommand struct {
	WorkspaceKey string
	Now          time.Time
	Limit        int
}

type RecordOutboxDeliveryResultCommand struct {
	WorkspaceKey   string
	OutboxID       string
	Status         OutboxDeliveryStatus
	Attempt        int
	NextRetryAt    *time.Time
	LastError      string
	InboxMessageID string
}

// EnqueueLeadAssignmentCommand asks Execution to durably deliver one lead
// assignment on behalf of the exact live DriverRun owner. DriverRunID and the
// dedupe identity are derived from Owner rather than accepted from callers.
type EnqueueLeadAssignmentCommand struct {
	WorkspaceKey string
	EpicID       string
	TargetAgent  string
	Owner        Owner
}

type OutboxDeliveryQuery struct {
	WorkspaceKey string
	Now          time.Time
	Limit        int
}

type OutboxDeliveryResult struct {
	WorkspaceKey   string
	OutboxID       string
	Status         OutboxDeliveryStatus
	Attempt        int
	NextRetryAt    *time.Time
	LastError      string
	InboxMessageID string
}

type OutboxDeliveryEnqueue struct {
	WorkspaceKey string
	EpicID       string
	Kind         OutboxKind
	DriverRunID  string
	TargetAgent  string
	DedupeKey    string
}

// OutboxDelivery is the immutable runtime view needed to attempt one delivery.
type OutboxDelivery struct {
	WorkspaceKey   string
	OutboxID       string
	Kind           OutboxKind
	EpicID         string
	DriverRunID    string
	TaskRunID      string
	TargetAgent    string
	Body           string
	DedupeKey      string
	Status         OutboxDeliveryStatus
	Attempt        int
	NextRetryAt    *time.Time
	LastError      string
	InboxMessageID string
}

// OutboxDeliveryPort is Execution's owner-private persistence boundary.
type OutboxDeliveryPort interface {
	EnqueueOutboxDelivery(context.Context, OutboxDeliveryEnqueue) (*OutboxDelivery, error)
	ListDueOutboxDeliveries(context.Context, OutboxDeliveryQuery) ([]OutboxDelivery, error)
	RecordOutboxDeliveryResult(context.Context, OutboxDeliveryResult) (*OutboxDelivery, error)
}

func validOutboxDeliveryQuery(query OutboxDeliveryQuery) bool {
	return strings.TrimSpace(query.WorkspaceKey) != "" && !query.Now.IsZero() && query.Limit > 0
}

func validOutboxDeliveryResult(result OutboxDeliveryResult) bool {
	if strings.TrimSpace(result.WorkspaceKey) == "" || strings.TrimSpace(result.OutboxID) == "" || result.Attempt < 1 {
		return false
	}
	validStatus := result.Status == OutboxDeliveryStatusPending || result.Status == OutboxDeliveryStatusDelivered ||
		result.Status == OutboxDeliveryStatusUnsupported || result.Status == OutboxDeliveryStatusFailed
	return validStatus && (result.Status != OutboxDeliveryStatusPending || result.NextRetryAt != nil)
}

// OutboxDeliveryAPI is Execution's system-only runtime surface for draining
// durable agent-notification delivery work.
type OutboxDeliveryAPI interface {
	ListDueOutboxDeliveries(context.Context, authority.SystemAuthority, ListDueOutboxDeliveriesCommand) ([]OutboxDelivery, error)
	RecordOutboxDeliveryResult(context.Context, authority.SystemAuthority, RecordOutboxDeliveryResultCommand) (*OutboxDelivery, error)
}

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
		authority.Allow(ActionListDueOutboxDeliveries, authority.ClassSystem),
		authority.Allow(ActionRecordOutboxDeliveryResult, authority.ClassSystem),
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

// TaskRunQueries exposes the exact TaskRun snapshot needed by inbound runtime
// adapters after they have verified run-scoped authority.
type TaskRunQueries interface {
	GetTaskRun(context.Context, string, string) (*TaskRun, error)
	ListTaskRuns(context.Context, TaskRunArchiveQuery) ([]*TaskRun, error)
	ListActiveTaskRuns(context.Context, ActiveTaskRunQuery) ([]*TaskRun, error)
	ListTaskRunEvents(context.Context, TaskRunEventQuery) ([]*TaskRunEvent, error)
}

// TaskRunAuthorityResolver is an inbound composition seam. The transport
// supplies the request-derived owner tuple and chooses one exact action; the
// resolver returns an opaque authority bound to that tuple. The outbound
// TaskRun port remains authoritative for validating LeaseToken.
type TaskRunAuthorityResolver interface {
	ResolveTaskRunAuthority(context.Context, string, authority.Action, Owner) (authority.ExecutionAuthority, error)
}
