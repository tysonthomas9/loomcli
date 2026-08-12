package execution

import (
	"context"
	"strings"

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
	GetDriverRun(context.Context, string, string) (*DriverRun, error)
	ListDriverRuns(context.Context, DriverRunQuery) ([]*DriverRun, error)
	ListDriverRunAwaits(context.Context, string, string) ([]*DriverAwaitInstance, error)
	ListDriverRunSteps(context.Context, string, string) ([]DriverRunStep, error)
	ListDriverRunEvents(context.Context, DriverRunEventQuery) (*DriverRunEventPage, error)
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

func (service *Service) ListDriverRunAwaits(
	ctx context.Context,
	workspace,
	runID string,
) ([]*DriverAwaitInstance, error) {
	workspace = strings.TrimSpace(workspace)
	runID = strings.TrimSpace(runID)
	if workspace == "" || runID == "" {
		return nil, ErrInvalid
	}
	port := service.dependencies.DriverRuns.AwaitQueries
	if port == nil {
		return nil, ErrUnavailable
	}
	values, err := port.ListDriverRunAwaits(ctx, workspace, runID)
	if err != nil {
		return nil, err
	}
	out := make([]*DriverAwaitInstance, 0, len(values))
	for _, value := range values {
		if value == nil || value.WorkspaceKey != workspace || value.RunID != runID {
			return nil, ErrConflict
		}
		out = append(out, cloneDriverAwait(value))
	}
	return out, nil
}

func (service *Service) ListDriverRunSteps(ctx context.Context, workspace, runID string) ([]DriverRunStep, error) {
	workspace = strings.TrimSpace(workspace)
	runID = strings.TrimSpace(runID)
	if workspace == "" || runID == "" {
		return nil, ErrInvalid
	}
	port := service.dependencies.DriverRuns.Projections
	if port == nil {
		return nil, ErrUnavailable
	}
	values, err := port.ListDriverRunSteps(ctx, workspace, runID)
	if err != nil {
		return nil, err
	}
	out := make([]DriverRunStep, len(values))
	copy(out, values)
	for index := range out {
		if out[index].WorkspaceKey != workspace || out[index].DriverRunID != runID || strings.TrimSpace(out[index].StepID) == "" {
			return nil, ErrConflict
		}
	}
	return out, nil
}

func (service *Service) ListDriverRunEvents(ctx context.Context, query DriverRunEventQuery) (*DriverRunEventPage, error) {
	query.WorkspaceKey = strings.TrimSpace(query.WorkspaceKey)
	query.RunID = strings.TrimSpace(query.RunID)
	query.After = strings.TrimSpace(query.After)
	if query.WorkspaceKey == "" || query.RunID == "" || query.Limit < 1 || query.Limit > 1000 {
		return nil, ErrInvalid
	}
	port := service.dependencies.DriverRuns.Projections
	if port == nil {
		return nil, ErrUnavailable
	}
	page, err := port.ListDriverRunEvents(ctx, query)
	if err != nil {
		return nil, err
	}
	if page == nil {
		return nil, ErrConflict
	}
	out := &DriverRunEventPage{Cursor: page.Cursor, Events: make([]DriverRunEvent, len(page.Events))}
	copy(out.Events, page.Events)
	for index := range out.Events {
		out.Events[index].Metadata = cloneDriverRunStringMap(out.Events[index].Metadata)
		if out.Events[index].WorkspaceID != query.WorkspaceKey {
			return nil, ErrConflict
		}
	}
	return out, nil
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
