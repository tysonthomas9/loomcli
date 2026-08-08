package execution

import (
	"context"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

const (
	ActionSubmitDriverRun                authority.Action = "execution.submit-driver-run"
	ActionStartChildDriverRun            authority.Action = "execution.start-child-driver-run"
	ActionCascadeChildDriverRuns         authority.Action = "execution.cascade-child-driver-runs"
	ActionRecoverChildDriverRunCascade   authority.Action = "execution.recover-child-driver-run-cascade"
	ActionRecoverTerminalDriverRunWork   authority.Action = "execution.recover-terminal-driver-run-work"
	ActionClaimDriverRun                 authority.Action = "execution.claim-driver-run"
	ActionHeartbeatDriverRun             authority.Action = "execution.heartbeat-driver-run"
	ActionClaimDriverRunWorkItem         authority.Action = "execution.claim-driver-run-work-item"
	ActionReleaseDriverRunWorkItem       authority.Action = "execution.release-driver-run-work-item"
	ActionHandoffDriverRunReviewWorkItem authority.Action = "execution.handoff-driver-run-review-work-item"
	ActionFinalizeDriverRun              authority.Action = "execution.finalize-driver-run"
	ActionRecoverDriverRuns              authority.Action = "execution.recover-driver-runs"
	ActionAwaitDriverRun                 authority.Action = "execution.await-driver-run"
	ActionResolveDriverAwait             authority.Action = "execution.resolve-driver-await"
	ActionBindWorkerProfileParent        authority.Action = "execution.bind-worker-profile-parent"
	ActionEnqueueLeadAssignment          authority.Action = "execution.enqueue-lead-assignment"
)

// DriverRunOperationRules is the default-deny registry for the DriverRun and
// await slice. It is kept separate from OperationRules while Phase 4 callers
// migrate so one composition can register both TaskRun and DriverRun actions
// against the same issuer and admission seal.
func DriverRunOperationRules() []authority.OperationRule {
	return []authority.OperationRule{
		authority.OperatorOnly(ActionSubmitDriverRun),
		authority.Allow(ActionStartChildDriverRun, authority.ClassExecution),
		authority.Allow(ActionCascadeChildDriverRuns, authority.ClassExecution),
		authority.Allow(ActionRecoverChildDriverRunCascade, authority.ClassSystem),
		authority.Allow(ActionRecoverTerminalDriverRunWork, authority.ClassSystem),
		authority.Allow(ActionClaimDriverRun, authority.ClassSystem),
		authority.Allow(ActionHeartbeatDriverRun, authority.ClassExecution),
		authority.Allow(ActionClaimDriverRunWorkItem, authority.ClassExecution),
		authority.Allow(ActionReleaseDriverRunWorkItem, authority.ClassExecution),
		authority.Allow(ActionHandoffDriverRunReviewWorkItem, authority.ClassExecution),
		authority.Allow(ActionFinalizeDriverRun, authority.ClassExecution),
		authority.Allow(ActionRecoverDriverRuns, authority.ClassSystem),
		authority.Allow(ActionAwaitDriverRun, authority.ClassExecution),
		authority.Allow(ActionResolveDriverAwait, authority.ClassSystem),
		authority.Allow(ActionBindWorkerProfileParent, authority.ClassExecution),
		authority.Allow(ActionEnqueueLeadAssignment, authority.ClassExecution),
	}
}

// DriverRunAPI is the public intent surface used by workflow submission,
// the DriverRun executor, and the run-scoped driver-op transport. Queries may
// remain on read models while Phase 4 moves every live lifecycle mutation
// through this API.
type DriverRunAPI interface {
	SubmitDriverRun(context.Context, authority.OperatorAuthority, SubmitDriverRunCommand) (*DriverRun, error)
	StartChildDriverRun(context.Context, authority.ExecutionAuthority, StartChildDriverRunCommand) (*DriverRun, error)
	CascadeChildDriverRuns(context.Context, authority.ExecutionAuthority, CascadeChildDriverRunsCommand) (CascadeChildDriverRunsResult, error)
	RecoverChildDriverRunCascade(context.Context, authority.SystemAuthority, RecoverChildDriverRunCascadeCommand) (CascadeChildDriverRunsResult, error)
	RecoverTerminalDriverRunWork(context.Context, authority.SystemAuthority, RecoverTerminalDriverRunWorkCommand) (RecoverTerminalDriverRunWorkResult, error)
	ClaimDriverRun(context.Context, authority.SystemAuthority, ClaimDriverRunCommand) (*DriverRun, error)
	HeartbeatDriverRun(context.Context, authority.ExecutionAuthority, DriverRunHeartbeatCommand) (*DriverRun, error)
	ClaimDriverRunWorkItem(context.Context, authority.ExecutionAuthority, ClaimDriverRunWorkItemCommand) (DriverRunWorkItemMutationResult, error)
	ReleaseDriverRunWorkItem(context.Context, authority.ExecutionAuthority, ReleaseDriverRunWorkItemCommand) (DriverRunWorkItemMutationResult, error)
	HandoffDriverRunReviewWorkItem(context.Context, authority.ExecutionAuthority, HandoffDriverRunReviewWorkItemCommand) (DriverRunWorkItemMutationResult, error)
	FinalizeDriverRun(context.Context, authority.ExecutionAuthority, FinalizeDriverRunCommand) (*DriverRun, error)
	RecoverDriverRuns(context.Context, authority.SystemAuthority, RecoverDriverRunsCommand) (*DriverRunRecoveryResult, error)
	AwaitDriverRun(context.Context, authority.ExecutionAuthority, AwaitDriverRunCommand) (*DriverAwaitResult, error)
	ResolveDriverAwait(context.Context, authority.SystemAuthority, ResolveDriverAwaitCommand) error
	BindWorkerProfileParent(context.Context, authority.ExecutionAuthority, BindWorkerProfileParentCommand) (*WorkerProfile, error)
	EnqueueLeadAssignment(context.Context, authority.ExecutionAuthority, EnqueueLeadAssignmentCommand) (*OutboxDelivery, error)
}

// DriverRunAuthorityResolver derives one exact run-bound authority after the
// transport has verified its credential and selected the operation.
type DriverRunAuthorityResolver interface {
	ResolveDriverRunAuthority(context.Context, string, authority.Action, Owner) (authority.ExecutionAuthority, error)
}

// SystemAuthorityResolver is the runtime-host seam for registered Execution
// components. Implementations must reject unknown component/action pairs.
type SystemAuthorityResolver interface {
	ResolveExecutionSystemAuthority(context.Context, string, authority.Action, string) (authority.SystemAuthority, error)
}
