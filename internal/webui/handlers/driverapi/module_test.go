package driverapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	appserve "github.com/tysonthomas9/loomcli/internal/app/serve"
	"github.com/tysonthomas9/loomcli/internal/app/workfloweventing"
	"github.com/tysonthomas9/loomcli/internal/backend"
	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	trigger "github.com/tysonthomas9/loomcli/internal/infra/automationruntime"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/testutil"
)

// fakeIssueBackend embeds the interface so only the methods the driver ops
// touch need real implementations; anything else panics loudly.
type fakeIssueBackend struct {
	backend.IssueBackend
	ready                 []backend.IssueData
	readyOpts             []backend.ReadyOpts
	blocked               []backend.IssueData
	children              []backend.IssueData
	epic                  *backend.IssueDetailData
	actor                 string
	claimed               []string
	releases              []fakeRelease
	typedClaims           []execution.ClaimDriverRunWorkItemCommand
	typedReleases         []execution.ReleaseDriverRunWorkItemCommand
	typedHandoffs         []execution.HandoffDriverRunReviewWorkItemCommand
	typedClaimErrors      map[string]error
	typedClaimItems       map[string]*execution.DriverRunWorkItem
	repositoryBlocks      []string
	repositoryBlockResult *backend.RepositoryRequirementResult
	repositoryBlockErr    error
}

type fakeRelease struct {
	id    string
	actor string
}

// testWorkflowEventAuthorityProvider and testLegacyEventAdmission are
// deliberately test-only compatibility wiring for the pre-Phase-3 memstore
// route suites. Production driverapi has no InternalSource or TriggerRoutes
// fallback.
type testWorkflowEventAuthorityProvider struct{}

func (testWorkflowEventAuthorityProvider) AuthorityForVerifiedRun(context.Context, workfloweventing.VerifiedRun) (authority.ExecutionAuthority, error) {
	return authority.ExecutionAuthority{}, nil
}

type testLegacyEventAdmission struct{ st store.Store }

func (adapter testLegacyEventAdmission) AdmitEvent(ctx context.Context, _ automation.EventAuthority, command automation.AdmitEventCommand) (*automation.AdmissionResult, error) {
	parent, err := adapter.st.DriverRuns().Get(ctx, command.WorkspaceKey, "run-1")
	if err != nil {
		return nil, err
	}
	sourceResult, err := (&trigger.InternalSource{Store: adapter.st}).Emit(ctx, command.WorkspaceKey, trigger.InternalEvent{
		EventID: command.SourceEventID, EventType: command.EventType,
		Origin: automation.EventOriginWorkflow, ParentEventID: parent.SourceRef,
		EmittedByRunID: parent.RunID, SubjectRef: command.SubjectRef,
		ActorRef: driverpkg.DriverRunActor(parent.RunID),
		EpicID:   firstNonEmpty(parent.EpicID, driverpkg.DriverRunPayloadEpicID(parent.Payload)),
		Payload:  command.Payload, SubjectAttrs: command.SubjectAttrs,
	})
	if err != nil {
		return nil, err
	}
	result := &automation.AdmissionResult{
		Dropped: sourceResult.Dropped, DropReason: sourceResult.DropReason,
		EventType: sourceResult.EventType, RouteKey: sourceResult.RouteKey,
		Origin: sourceResult.Origin, HopDepth: sourceResult.HopDepth,
	}
	if sourceResult.Dropped {
		return result, nil
	}
	result.Event = &automation.Event{
		WorkspaceKey: command.WorkspaceKey, SourceKind: automation.SourceKindInternal,
		SourceEventID: command.SourceEventID, EventType: sourceResult.EventType,
		RouteKey: sourceResult.RouteKey, SubjectRef: command.SubjectRef,
		ActorRef: driverpkg.DriverRunActor(parent.RunID), EmittingRunID: parent.RunID,
		ParentEventID: parent.SourceRef, EpicID: firstNonEmpty(parent.EpicID, driverpkg.DriverRunPayloadEpicID(parent.Payload)),
		Origin: sourceResult.Origin, HopDepth: sourceResult.HopDepth,
	}
	if sourceResult.Dispatch != nil {
		result.Deliveries = make([]*automation.Delivery, 0, len(sourceResult.Dispatch.Deliveries))
		for _, delivery := range sourceResult.Dispatch.Deliveries {
			result.Deliveries = append(result.Deliveries, &automation.Delivery{
				DeliveryID: delivery.DeliveryID, TriggerBindingID: delivery.BindingID,
				DriverRunID: delivery.RunID, Status: delivery.Status,
				RejectionReason: delivery.RejectionReason,
			})
		}
	}
	return result, nil
}

func (f *fakeIssueBackend) Ready(_ context.Context, opts backend.ReadyOpts) ([]backend.IssueData, error) {
	f.readyOpts = append(f.readyOpts, opts)
	return f.ready, nil
}

func (f *fakeIssueBackend) ReleaseIssueAsActor(_ context.Context, id, actor string) error {
	f.releases = append(f.releases, fakeRelease{id: id, actor: actor})
	return nil
}

func (f *fakeIssueBackend) Blocked(_ context.Context, _ backend.BlockedOpts) ([]backend.IssueData, error) {
	return f.blocked, nil
}

func (f *fakeIssueBackend) List(_ context.Context, _ backend.ListOpts) ([]backend.IssueData, error) {
	return f.children, nil
}

func (f *fakeIssueBackend) ClaimIssue(_ context.Context, id string, _ time.Duration) error {
	f.claimed = append(f.claimed, id)
	return nil
}

func (f *fakeIssueBackend) ClaimIssueAsActor(_ context.Context, id string, _ time.Duration, actor string) error {
	f.claimed = append(f.claimed, id)
	f.actor = actor
	return nil
}

func (f *fakeIssueBackend) Get(_ context.Context, _ string) (*backend.IssueDetailData, error) {
	return f.epic, nil
}

func (f *fakeIssueBackend) BlockRepositoryRequired(_ context.Context, id string) (*backend.RepositoryRequirementResult, error) {
	f.repositoryBlocks = append(f.repositoryBlocks, id)
	if f.repositoryBlockErr != nil {
		return nil, f.repositoryBlockErr
	}
	if f.repositoryBlockResult != nil {
		return f.repositoryBlockResult, nil
	}
	return &backend.RepositoryRequirementResult{Changed: true}, nil
}

func (f *fakeIssueBackend) SetIssueRepository(_ context.Context, id, repo string) (*backend.IssueData, error) {
	return &backend.IssueData{ID: id, SourceRepo: repo}, nil
}

type testHarness struct {
	server      *httptest.Server
	store       *memstore.Store
	module      *Module
	backend     *fakeIssueBackend
	runID       string
	nodeID      string
	leaseID     string
	fence       int64
	runTokenKey []byte
	execution   *appserve.ExecutionCapability
}

type testStoreAgentIdentities struct {
	store store.AgentServiceStore
}

func (queries testStoreAgentIdentities) GetAgent(ctx context.Context, workspace, agentID string) (*agents.Agent, error) {
	value, err := queries.store.Get(ctx, workspace, agentID)
	if err != nil {
		return nil, err
	}
	return testCanonicalAgent(value), nil
}

func (queries testStoreAgentIdentities) ListAgents(ctx context.Context, workspace string, filter agents.AgentFilter) ([]*agents.Agent, error) {
	values, err := queries.store.List(ctx, workspace, store.AgentServiceFilter{
		RoleName:       filter.RoleName,
		IncludeDeleted: filter.IncludeDeleted,
		Limit:          filter.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]*agents.Agent, 0, len(values))
	for _, value := range values {
		out = append(out, testCanonicalAgent(value))
	}
	return out, nil
}

func testCanonicalAgent(value *domain.AgentService) *agents.Agent {
	if value == nil {
		return nil
	}
	return &agents.Agent{
		WorkspaceKey: value.WorkspaceKey,
		AgentID:      value.ServiceID,
		GenerationID: value.GenerationID,
		Name:         value.Name,
		Behavior:     agents.BehaviorReference{RoleName: value.RoleName},
		ProfileName:  value.ProfileName,
		CreatedAt:    value.CreatedAt,
		UpdatedAt:    value.UpdatedAt,
	}
}

type testDriverRunExecution struct {
	execution.DriverRunAPI
	store  store.Store
	issues *fakeIssueBackend
}

// testNoOpTerminalWorkRecoveryExecution supplies the atomic Fleet-only
// convergence command to executor tests backed by the in-memory store. The
// driver API tests exercise outcome/await delivery; terminal TaskRun and Work
// Item recovery has its own command-level coverage.
type testNoOpTerminalWorkRecoveryExecution struct {
	testDriverRunExecution
}

func (testNoOpTerminalWorkRecoveryExecution) RecoverTerminalDriverRunWork(
	_ context.Context,
	_ authority.SystemAuthority,
	command execution.RecoverTerminalDriverRunWorkCommand,
) (execution.RecoverTerminalDriverRunWorkResult, error) {
	commit := &execution.RecoverTerminalDriverRunWorkCommit{
		WorkspaceKey: command.WorkspaceKey,
		DriverRunID:  command.DriverRunID,
		ParentStatus: command.ParentStatus,
		Reason:       command.Reason,
		ErrorClass:   command.ErrorClass,
		RecoveredAt:  command.RecoveredAt,
	}
	return execution.RecoverTerminalDriverRunWorkResult{
		Committed: commit,
		ActionID:  command.RequestID,
	}, nil
}

func (adapter testDriverRunExecution) HeartbeatDriverRun(
	ctx context.Context,
	auth authority.ExecutionAuthority,
	command execution.DriverRunHeartbeatCommand,
) (*execution.DriverRun, error) {
	if adapter.DriverRunAPI != nil {
		return adapter.DriverRunAPI.HeartbeatDriverRun(ctx, auth, command)
	}
	run, err := adapter.store.DriverRuns().Heartbeat(
		ctx,
		command.WorkspaceKey,
		command.Owner.ResourceID,
		command.Owner.NodeID,
		command.Owner.LeaseID,
		command.Owner.FencingToken,
	)
	return testExecutionDriverRun(run), err
}

func (adapter testDriverRunExecution) ClaimDriverRunWorkItem(
	_ context.Context,
	_ authority.ExecutionAuthority,
	command execution.ClaimDriverRunWorkItemCommand,
) (execution.DriverRunWorkItemMutationResult, error) {
	adapter.issues.typedClaims = append(adapter.issues.typedClaims, command)
	if err := adapter.issues.typedClaimErrors[command.WorkItemID]; err != nil {
		return execution.DriverRunWorkItemMutationResult{}, err
	}
	var source backend.IssueData
	for _, issue := range adapter.issues.ready {
		if issue.ID == command.WorkItemID {
			source = issue
			break
		}
	}
	actor := driverpkg.DriverRunActor(command.Owner.ResourceID)
	appliedAt := command.ClaimedAt
	actionID := execution.DriverRunWorkItemClaimActionID(command.RequestID)
	workItem := &execution.DriverRunWorkItem{
		WorkspaceKey: command.WorkspaceKey, WorkItemID: command.WorkItemID, Title: source.Title,
		Status: "in_progress", Priority: source.Priority, IssueType: source.IssueType, Assignee: actor,
		Labels: append([]string(nil), source.Labels...), SourceRepo: source.SourceRepo, ParentID: source.Parent,
		UpdatedAt: command.ClaimedAt,
	}
	if override := adapter.issues.typedClaimItems[command.WorkItemID]; override != nil {
		copy := *override
		copy.Labels = append([]string(nil), override.Labels...)
		workItem = &copy
	}
	return execution.DriverRunWorkItemMutationResult{
		WorkItem: workItem,
		Action: &execution.DriverRunWorkItemAction{
			WorkspaceKey: command.WorkspaceKey, ActionID: actionID, IdempotencyKey: actionID,
			ActionType: "claim_work_item", TargetRef: command.WorkItemID, RequestedBy: actor, Status: "applied",
			RequestRef: "sha256:" + strings.Repeat("0", 64), ResponseRef: "issue://" + command.WorkItemID + "#claimed",
			CreatedAt: command.ClaimedAt, AppliedAt: &appliedAt,
		},
	}, nil
}

func (adapter testDriverRunExecution) ReleaseDriverRunWorkItem(
	_ context.Context,
	_ authority.ExecutionAuthority,
	command execution.ReleaseDriverRunWorkItemCommand,
) (execution.DriverRunWorkItemMutationResult, error) {
	adapter.issues.typedReleases = append(adapter.issues.typedReleases, command)
	actor := driverpkg.DriverRunActor(command.Owner.ResourceID)
	appliedAt := command.ReleasedAt
	actionID := execution.DriverRunWorkItemReleaseActionID(command.RequestID)
	return execution.DriverRunWorkItemMutationResult{
		WorkItem: &execution.DriverRunWorkItem{
			WorkspaceKey: command.WorkspaceKey, WorkItemID: command.WorkItemID,
			Status: "open", UpdatedAt: command.ReleasedAt,
		},
		Action: &execution.DriverRunWorkItemAction{
			WorkspaceKey: command.WorkspaceKey, ActionID: actionID, IdempotencyKey: actionID,
			ActionType: "release_work_item", TargetRef: command.WorkItemID, RequestedBy: actor, Status: "applied",
			RequestRef: "sha256:" + strings.Repeat("0", 64), ResponseRef: "issue://" + command.WorkItemID + "#released",
			CreatedAt: command.ReleasedAt, AppliedAt: &appliedAt,
		},
	}, nil
}

func (adapter testDriverRunExecution) HandoffDriverRunReviewWorkItem(
	_ context.Context,
	_ authority.ExecutionAuthority,
	command execution.HandoffDriverRunReviewWorkItemCommand,
) (execution.DriverRunWorkItemMutationResult, error) {
	adapter.issues.typedHandoffs = append(adapter.issues.typedHandoffs, command)
	actor := driverpkg.DriverRunActor(command.Owner.ResourceID)
	appliedAt := command.HandedOffAt
	actionID := execution.DriverRunReviewWorkItemHandoffActionID(command.RequestID)
	return execution.DriverRunWorkItemMutationResult{
		WorkItem: &execution.DriverRunWorkItem{
			WorkspaceKey: command.WorkspaceKey, WorkItemID: command.WorkItemID,
			Status: command.TargetStatus, Assignee: "", UpdatedAt: command.HandedOffAt,
			Priority: reviewHandoffPriority(command.Priority), Labels: append([]string(nil), command.Labels...),
			ExternalRef: reviewHandoffExternalRef(command.ExternalRef),
		},
		Action: &execution.DriverRunWorkItemAction{
			WorkspaceKey: command.WorkspaceKey, ActionID: actionID, IdempotencyKey: actionID,
			ActionType: "handoff_review_work_item", TargetRef: command.WorkItemID, RequestedBy: actor, Status: "applied",
			RequestRef: "sha256:" + strings.Repeat("0", 64), ResponseRef: "issue://" + command.WorkItemID + "#handed-off",
			CreatedAt: command.HandedOffAt, AppliedAt: &appliedAt,
		},
	}, nil
}

func reviewHandoffPriority(priority *int) int {
	if priority == nil {
		return 0
	}
	return *priority
}

func reviewHandoffExternalRef(externalRef *string) string {
	if externalRef == nil {
		return ""
	}
	return *externalRef
}

func (testDriverRunExecution) CascadeChildDriverRuns(
	_ context.Context,
	_ authority.ExecutionAuthority,
	command execution.CascadeChildDriverRunsCommand,
) (execution.CascadeChildDriverRunsResult, error) {
	return execution.CascadeChildDriverRunsResult{
		ActionID: command.RequestID,
		Committed: &execution.CascadeChildDriverRunsCommit{
			WorkspaceKey: command.WorkspaceKey,
			ParentRunID:  command.ParentRunID,
			ParentStatus: command.ParentStatus,
			Reason:       command.Reason,
			ErrorClass:   command.ErrorClass,
			CascadedAt:   command.CascadedAt,
			MaxDepth:     command.MaxDepth,
		},
	}, nil
}

func (testDriverRunExecution) RecoverChildDriverRunCascade(
	_ context.Context,
	_ authority.SystemAuthority,
	command execution.RecoverChildDriverRunCascadeCommand,
) (execution.CascadeChildDriverRunsResult, error) {
	return execution.CascadeChildDriverRunsResult{
		ActionID: command.RequestID,
		Committed: &execution.CascadeChildDriverRunsCommit{
			WorkspaceKey: command.WorkspaceKey,
			ParentRunID:  command.ParentRunID,
			ParentStatus: command.ParentStatus,
			Reason:       command.Reason,
			ErrorClass:   command.ErrorClass,
			CascadedAt:   command.CascadedAt,
			MaxDepth:     command.MaxDepth,
		},
	}, nil
}

func (adapter testDriverRunExecution) StartChildDriverRun(
	ctx context.Context,
	_ authority.ExecutionAuthority,
	command execution.StartChildDriverRunCommand,
) (*execution.DriverRun, error) {
	childDepth := 1
	ancestorID := command.Owner.ResourceID
	for childDepth <= command.MaxDepth {
		ancestor, err := adapter.store.DriverRuns().Get(ctx, command.WorkspaceKey, ancestorID)
		if err != nil || ancestor.ParentRunID == "" {
			break
		}
		ancestorID = ancestor.ParentRunID
		childDepth++
	}
	if childDepth > command.MaxDepth {
		return nil, domain.ErrCompositionDepthExceeded
	}
	childRunID := execution.ChildDriverRunID(command.Owner.ResourceID, command.ChildKey)
	payload := append(json.RawMessage(nil), command.Payload...)
	if len(payload) == 0 {
		payload = json.RawMessage(`{}`)
	}
	run, err := adapter.store.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: command.WorkspaceKey, RunID: childRunID,
		DriverID: command.DriverID, DriverVersionID: command.DriverVersionID,
		Entrypoint: driverpkg.EntrypointRun, SourceKind: driverpkg.ChildRunSourceKind,
		SourceRef: command.Owner.ResourceID, ParentRunID: command.Owner.ResourceID,
		IdempotencyKey: execution.ChildDriverRunRequestID(command.Owner.ResourceID, command.ChildKey),
		Payload:        payload,
	})
	if errors.Is(err, domain.ErrAlreadyExists) {
		run, err = adapter.store.DriverRuns().Get(ctx, command.WorkspaceKey, childRunID)
	}
	if err != nil {
		return nil, err
	}
	return testExecutionDriverRun(run), nil
}

type testDriverRunAuthorityResolver struct{}

func (testDriverRunAuthorityResolver) ResolveDriverRunAuthority(
	context.Context,
	string,
	authority.Action,
	execution.Owner,
) (authority.ExecutionAuthority, error) {
	return authority.ExecutionAuthority{}, nil
}

// testTaskRunClaimPort is the driver-API fixture's deliberately test-only
// stand-in for FleetDB's atomic TaskRun commands. Keeping the compatibility
// mutation here prevents the production Execution composition from regaining
// a generic Store-writing request fallback just to support HTTP unit tests.
type testTaskRunClaimPort struct {
	store           store.Store
	mu              sync.Mutex
	requestReceipts map[string]execution.RequestTaskRunResult
}

func (*testTaskRunClaimPort) UpdateTaskRunWorkItemDesign(context.Context, execution.UpdateTaskRunWorkItemDesignCommand) (execution.UpdateTaskRunWorkItemDesignResult, error) {
	return execution.UpdateTaskRunWorkItemDesignResult{}, execution.ErrUnavailable
}

func (port *testTaskRunClaimPort) ReplayTaskRunRequest(_ context.Context, command execution.RequestTaskRunCommand) (execution.RequestTaskRunResult, error) {
	port.mu.Lock()
	defer port.mu.Unlock()
	receipt, ok := port.requestReceipts[testTaskRunRequestReceiptKey(command)]
	if !ok {
		return execution.RequestTaskRunResult{}, execution.ErrTaskRunRequestReplayNotFound
	}
	return cloneTestTaskRunRequestReceipt(receipt, true), nil
}

func (port *testTaskRunClaimPort) RequestTaskRun(ctx context.Context, command execution.RequestTaskRunCommand) (execution.RequestTaskRunResult, error) {
	port.mu.Lock()
	defer port.mu.Unlock()
	key := testTaskRunRequestReceiptKey(command)
	if receipt, ok := port.requestReceipts[key]; ok {
		return cloneTestTaskRunRequestReceipt(receipt, true), nil
	}
	actionID := command.RequestID
	step, err := port.store.DriverSteps().CreateForRun(ctx, command.WorkspaceKey, command.DriverRunID, store.DriverStepCreate{
		StepID: command.DriverStepID, StepKind: "task_run", Status: domain.DriverStepQueued,
		TaskRunID: command.TaskRunID, ActionLedgerID: actionID,
		NodeID: command.ParentOwner.NodeID, LeaseID: command.ParentOwner.LeaseID, FencingToken: command.ParentOwner.FencingToken,
	})
	if err != nil {
		return execution.RequestTaskRunResult{}, err
	}
	metadata := cloneTestStringMap(command.RuntimeMetadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["execution_request_id"] = command.RequestID
	run, err := port.store.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: command.WorkspaceKey, TaskRunID: command.TaskRunID, DriverRunID: command.DriverRunID,
		DriverStepID: command.DriverStepID, TaskID: command.WorkItemID, WorkerProfileID: command.WorkerProfileID,
		Runner: command.Runner, RunnerRef: command.RunnerRef, RunnerKind: command.RunnerKind,
		RunnerEntrypoint: command.RunnerEntrypoint, RunnerVersionID: command.RunnerVersionID,
		ProviderProfile: command.ProviderProfile, Status: domain.TaskRunQueued, TargetNodeID: command.TargetNodeID,
		RunnerPlacement:  testDomainTaskRunPlacement(command.RunnerPlacement),
		SandboxPlacement: testDomainTaskRunPlacement(command.SandboxPlacement),
		RuntimeMetadata:  metadata, Input: append(json.RawMessage(nil), command.Input...),
	})
	if err != nil {
		return execution.RequestTaskRunResult{}, err
	}
	receipt := execution.RequestTaskRunResult{
		Run: &execution.TaskRun{
			WorkspaceKey: run.WorkspaceKey, TaskRunID: run.TaskRunID, DriverRunID: run.DriverRunID,
			DriverStepID: run.DriverStepID, WorkItemID: run.TaskID, WorkerProfileID: run.WorkerProfileID,
			Runner: run.Runner, RunnerRef: run.RunnerRef, RunnerKind: run.RunnerKind,
			RunnerEntrypoint: run.RunnerEntrypoint, RunnerVersionID: run.RunnerVersionID,
			ProviderProfile: run.ProviderProfile, TargetNodeID: run.TargetNodeID, Status: execution.StatusQueued,
			RunnerPlacement: command.RunnerPlacement, SandboxPlacement: command.SandboxPlacement,
			RuntimeMetadata: cloneTestStringMap(run.RuntimeMetadata), Input: append(json.RawMessage(nil), run.Input...),
			CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
		},
		Step: &execution.TaskRunDriverStep{
			WorkspaceKey: step.WorkspaceKey, StepID: step.StepID, DriverRunID: step.DriverRunID,
			TaskRunID: step.TaskRunID, Status: string(step.Status), ActionLedgerID: step.ActionLedgerID,
		},
		ActionID: actionID, ClaimActionID: command.ClaimActionID,
	}
	port.requestReceipts[key] = cloneTestTaskRunRequestReceipt(receipt, false)
	return cloneTestTaskRunRequestReceipt(receipt, false), nil
}

func testTaskRunRequestReceiptKey(command execution.RequestTaskRunCommand) string {
	return command.WorkspaceKey + "\x00" + command.RequestID
}

func cloneTestTaskRunRequestReceipt(receipt execution.RequestTaskRunResult, replay bool) execution.RequestTaskRunResult {
	copy := receipt
	copy.Replay = replay
	if receipt.Run != nil {
		run := *receipt.Run
		run.RuntimeMetadata = cloneTestStringMap(receipt.Run.RuntimeMetadata)
		run.Input = append(json.RawMessage(nil), receipt.Run.Input...)
		copy.Run = &run
	}
	if receipt.Step != nil {
		step := *receipt.Step
		copy.Step = &step
	}
	return copy
}

func cloneTestStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func testDomainTaskRunPlacement(value execution.Placement) domain.TaskRunPlacement {
	return domain.TaskRunPlacement{
		Provider: value.Provider, NodeID: value.NodeID, RunnerID: value.RunnerID,
		SandboxID: value.SandboxID, CWD: value.CWD, RepoRef: value.RepoRef,
	}
}

func (*testTaskRunClaimPort) ClaimTaskRun(context.Context, execution.ClaimTaskRunCommand) (execution.ClaimTaskRunResult, error) {
	return execution.ClaimTaskRunResult{}, execution.ErrUnavailable
}

func (*testTaskRunClaimPort) RequeueTaskRun(context.Context, execution.RequeueTaskRunCommand) (execution.RequeueTaskRunResult, error) {
	return execution.RequeueTaskRunResult{}, execution.ErrUnavailable
}

func (*testTaskRunClaimPort) ExhaustTaskRunRetries(context.Context, execution.ExhaustTaskRunRetriesCommand) (execution.ExhaustTaskRunRetriesResult, error) {
	return execution.ExhaustTaskRunRetriesResult{}, execution.ErrUnavailable
}

type testWorkflowCatalog struct {
	workflowcatalog.API
	store store.Store
}

func (catalog testWorkflowCatalog) GetDriver(ctx context.Context, workspace, driverRef string) (*workflowcatalog.Driver, error) {
	return catalog.store.Drivers().Get(ctx, workspace, driverRef)
}

func (catalog testWorkflowCatalog) GetVersion(ctx context.Context, workspace, versionID string) (*workflowcatalog.DriverVersion, error) {
	return catalog.store.DriverVersions().Get(ctx, workspace, versionID)
}

func testExecutionDriverRun(run *domain.DriverRun) *execution.DriverRun {
	if run == nil {
		return nil
	}
	return &execution.DriverRun{
		WorkspaceKey: run.WorkspaceKey, RunID: run.RunID, DriverID: run.DriverID, DriverVersionID: run.DriverVersionID,
		Entrypoint: run.Entrypoint, SourceKind: run.SourceKind, SourceRef: run.SourceRef, EpicID: run.EpicID,
		ParentRunID: run.ParentRunID, TriggerBindingID: run.TriggerBindingID, Status: execution.DriverRunStatus(run.Status),
		Owner:          execution.Owner{ResourceKind: execution.ResourceDriverRun, ResourceID: run.RunID, NodeID: run.NodeID, LeaseID: run.LeaseID, FencingToken: run.FencingToken},
		IdempotencyKey: run.IdempotencyKey, Payload: append(json.RawMessage(nil), run.Payload...),
		Summary: run.Summary, ErrorClass: run.ErrorClass, AwaitInstanceKey: run.AwaitInstanceKey,
		ResumeSourceEventID: run.ResumeSourceEventID, CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt,
	}
}

func testExecutionAwait(instance *domain.AwaitInstance) *execution.DriverAwaitInstance {
	if instance == nil {
		return nil
	}
	return &execution.DriverAwaitInstance{
		WorkspaceKey: instance.WorkspaceKey, InstanceKey: instance.InstanceKey, RunID: instance.RunID,
		Pattern: instance.Pattern, ActorAllow: append([]string(nil), instance.ActorAllow...),
		Deadline: instance.Deadline, RegisteredAt: instance.RegisteredAt, Status: execution.DriverAwaitStatus(instance.Status),
		SatisfiedByEventID: instance.SatisfiedByEventID, SatisfiedActor: instance.SatisfiedActor,
		SatisfiedPayload: append(json.RawMessage(nil), instance.SatisfiedPayload...),
		ResumedAt:        instance.ResumedAt,
	}
}

func newTestHarness(t *testing.T, apiToken string) *testHarness {
	t.Helper()
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS",
		DriverID:     "driver-1",
		Name:         "epic-runner",
		OwnerType:    workflowcatalog.DriverOwnerSystem,
		Status:       workflowcatalog.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     "WS",
		VersionID:        "version-1",
		DriverID:         "driver-1",
		Version:          1,
		SourceDigest:     "sha256:source",
		BundleDigest:     "sha256:bundle",
		ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	if _, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey:    "WS",
		RunID:           "run-1",
		DriverID:        "driver-1",
		DriverVersionID: "version-1",
		EpicID:          "EPIC-1",
	}); err != nil {
		t.Fatalf("Create driver run: %v", err)
	}
	claimed, err := st.DriverRuns().Claim(ctx, "WS", "run-1", "node-1", "lease-1")
	if err != nil {
		t.Fatalf("Claim driver run: %v", err)
	}
	fake := &fakeIssueBackend{}
	// Every harness carries a run-token signing key so all existing
	// header-quad/static-bearer tests double as proof the legacy path is
	// unchanged when the token auth path is enabled.
	runTokenKey := bytes.Repeat([]byte{0x42}, 32)
	eventWorkflow, err := workfloweventing.New(testWorkflowEventAuthorityProvider{}, testLegacyEventAdmission{st: st})
	if err != nil {
		t.Fatalf("new test workflow eventing: %v", err)
	}
	repairs, ok := st.DriverSteps().(store.TerminalDriverStepRepairStore)
	if !ok {
		t.Fatal("test DriverStep store lacks terminal repair support")
	}
	taskRunCommands := &testTaskRunClaimPort{store: st, requestReceipts: make(map[string]execution.RequestTaskRunResult)}
	executionCapability, err := appserve.NewExecutionCapability(appserve.ExecutionDependencies{
		TaskRuns: st.TaskRuns(), DriverRuns: st.DriverRuns(), DriverSteps: st.DriverSteps(),
		TerminalStepRepairs: repairs, TaskRunEvents: st.TaskRunEvents(), Nodes: st.Nodes(),
		WorkerProfiles: st.WorkerProfiles(), AgentQueries: testutil.StaticAgentQueries{}, Outbox: st.Outbox(), Awaits: st.Awaits(), TriggerEvents: st.TriggerEvents(),
		Workspaces: st.Workspaces(), AtomicTaskRunRequests: taskRunCommands, AtomicTaskRunClaims: taskRunCommands,
		AtomicTaskRunWorkItemDesign: taskRunCommands,
		AtomicTaskRunRequeues:       taskRunCommands, AtomicTaskRunRetryExhaustion: taskRunCommands,
		AllowLegacyStoreAdapters: true,
	})
	if err != nil {
		t.Fatalf("new test Execution capability: %v", err)
	}
	module := NewModule(Config{
		Store:                st,
		APIToken:             apiToken,
		RunTokenKey:          runTokenKey,
		WorkflowEventing:     eventWorkflow,
		Execution:            testDriverRunExecution{DriverRunAPI: executionCapability.DriverRunAPI(), store: st, issues: fake},
		ExecutionAuthorities: executionCapability.DriverRunAuthorityResolver(),
		AgentIdentities:      testStoreAgentIdentities{store: st.AgentServices()},
		TaskRunRequests:      executionCapability.TaskRunRequestAPI(),
		TaskRunRecovery:      executionCapability.TaskRunRecoveryAPI(),
		TaskRuns:             executionCapability.TaskRunAPI(),
		TaskRunAuthorities:   executionCapability.TaskRunAuthorityResolver(),
		WorkflowCatalog:      testWorkflowCatalog{store: st},
		IssueBackends: func(_, actor string) (backend.IssueBackend, error) {
			fake.actor = actor
			return fake, nil
		},
	})
	mux := http.NewServeMux()
	module.Register(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return &testHarness{
		server:      server,
		store:       st,
		module:      module,
		backend:     fake,
		runID:       claimed.RunID,
		nodeID:      claimed.NodeID,
		leaseID:     claimed.LeaseID,
		fence:       claimed.FencingToken,
		runTokenKey: runTokenKey,
		execution:   executionCapability,
	}
}

type opRequest struct {
	op      string
	body    any
	headers map[string]string
}

func (h *testHarness) do(t *testing.T, req opRequest) (*http.Response, map[string]any) {
	resp, decoded := h.doAny(t, req)
	asMap, _ := decoded.(map[string]any)
	return resp, asMap
}

func (h *testHarness) doAny(t *testing.T, req opRequest) (*http.Response, any) {
	t.Helper()
	payload, err := json.Marshal(req.body)
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	if req.body == nil {
		payload = []byte("{}")
	}
	httpReq, err := http.NewRequest(http.MethodPost, h.server.URL+"/api/workspaces/WS/driver/"+req.op, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	for name, value := range req.headers {
		if value != "" {
			httpReq.Header.Set(name, value)
		}
	}
	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	var decoded any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil && !strings.Contains(err.Error(), "EOF") {
		t.Fatalf("decode response: %v", err)
	}
	return resp, decoded
}

func (h *testHarness) ownerHeaders() map[string]string {
	return map[string]string{
		HeaderDriverRunID:        h.runID,
		HeaderDriverNodeID:       h.nodeID,
		HeaderDriverLeaseID:      h.leaseID,
		HeaderDriverLeaseToken:   "driver-test-token",
		HeaderDriverFencingToken: fmt.Sprintf("%d", h.fence),
	}
}

func TestVerifyRunOpProvesOwnerThroughExecution(t *testing.T) {
	h := newTestHarness(t, "")
	resp, decoded := h.do(t, opRequest{op: "verify-run", headers: h.ownerHeaders()})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d body=%v, want 200", resp.StatusCode, decoded)
	}
	if decoded["run_id"] != h.runID || decoded["workspace_key"] != "WS" {
		t.Fatalf("verified run = %v", decoded)
	}

	wrongOwner := h.ownerHeaders()
	wrongOwner[HeaderDriverFencingToken] = fmt.Sprint(h.fence + 1)
	resp, decoded = h.do(t, opRequest{op: "verify-run", headers: wrongOwner})
	if resp.StatusCode != http.StatusForbidden || errorCode(t, decoded) != "not_owner" {
		t.Fatalf("wrong-owner status/body = %d/%v, want 403 not_owner", resp.StatusCode, decoded)
	}
}

func TestUpdateAgentParentUsesVerifiedDriverRunGeneration(t *testing.T) {
	h := newTestHarness(t, "")
	if _, err := h.store.Roles().Create(t.Context(), store.RoleCreate{WorkspaceKey: "WS", Name: "task"}); err != nil {
		t.Fatalf("create task role: %v", err)
	}
	for _, name := range []string{"child", "wrong-generation-child"} {
		profileID := name + "-profile"
		if _, err := h.store.WorkerProfiles().Create(t.Context(), store.WorkerProfileCreate{
			WorkspaceKey: "WS",
			ProfileID:    profileID,
			Role:         "task",
		}); err != nil {
			t.Fatalf("create %s profile: %v", name, err)
		}
		if _, err := h.store.AgentServices().Create(t.Context(), store.AgentServiceCreate{
			WorkspaceKey: "WS",
			ServiceID:    name,
			Kind:         domain.AgentServiceKindSupport,
			RoleName:     "task",
			ProfileName:  profileID,
		}); err != nil {
			t.Fatalf("create %s identity: %v", name, err)
		}
	}

	response, decoded := h.do(t, opRequest{
		op: "update-agent-parent",
		body: map[string]string{
			"agent": "child", "parent": h.runID, "expectParent": "",
		},
		headers: h.ownerHeaders(),
	})
	if response.StatusCode != http.StatusOK {
		t.Fatalf("update parent status/body = %d/%v, want 200", response.StatusCode, decoded)
	}
	child, err := h.store.WorkerProfiles().Get(t.Context(), "WS", "child-profile")
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentEpic != h.runID {
		t.Fatalf("child parent = %q, want %q", child.ParentEpic, h.runID)
	}

	staleHeaders := h.ownerHeaders()
	staleHeaders[HeaderDriverFencingToken] = fmt.Sprint(h.fence + 1)
	response, decoded = h.do(t, opRequest{
		op: "update-agent-parent",
		body: map[string]string{
			"agent": "wrong-generation-child", "parent": h.runID, "expectParent": "",
		},
		headers: staleHeaders,
	})
	if response.StatusCode != http.StatusForbidden || errorCode(t, decoded) != "not_owner" {
		t.Fatalf("stale generation status/body = %d/%v, want 403 not_owner", response.StatusCode, decoded)
	}
	unchanged, err := h.store.WorkerProfiles().Get(t.Context(), "WS", "wrong-generation-child-profile")
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.ParentEpic != "" {
		t.Fatalf("stale generation changed parent to %q", unchanged.ParentEpic)
	}
}

func TestVerifyRunOpRejectsTrailingJSON(t *testing.T) {
	h := newTestHarness(t, "")
	req, err := http.NewRequest(http.MethodPost, h.server.URL+"/api/workspaces/WS/driver/verify-run", strings.NewReader(`{} {}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for name, value := range h.ownerHeaders() {
		req.Header.Set(name, value)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	var decoded map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest || errorCode(t, decoded) != "invalid" {
		t.Fatalf("status/body = %d/%v, want 400 invalid", resp.StatusCode, decoded)
	}
}

func errorCode(t *testing.T, decoded map[string]any) string {
	t.Helper()
	envelope, ok := decoded["error"].(map[string]any)
	if !ok {
		t.Fatalf("response has no structured error: %v", decoded)
	}
	code, _ := envelope["code"].(string)
	return code
}

func TestDriverAPIRequiresRunIDHeader(t *testing.T) {
	h := newTestHarness(t, "")
	resp, decoded := h.do(t, opRequest{op: "list-agents"})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	if code := errorCode(t, decoded); code != "unauthenticated" {
		t.Fatalf("error code = %q, want unauthenticated", code)
	}
}

func TestDriverAPIBearerToken(t *testing.T) {
	h := newTestHarness(t, "secret-token")

	headers := h.ownerHeaders()
	resp, decoded := h.do(t, opRequest{op: "list-agents", headers: headers})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status without token = %d, want 401", resp.StatusCode)
	}
	if code := errorCode(t, decoded); code != "unauthenticated" {
		t.Fatalf("error code = %q, want unauthenticated", code)
	}

	headers["Authorization"] = "Bearer wrong"
	resp, _ = h.do(t, opRequest{op: "list-agents", headers: headers})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status with wrong token = %d, want 401", resp.StatusCode)
	}

	headers["Authorization"] = "Bearer secret-token"
	resp, _ = h.do(t, opRequest{op: "list-agents", headers: headers})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status with correct token = %d, want 200", resp.StatusCode)
	}
}

func TestDriverAPIUnknownOp(t *testing.T) {
	h := newTestHarness(t, "")
	resp, decoded := h.do(t, opRequest{op: "no-such-op", headers: h.ownerHeaders()})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if code := errorCode(t, decoded); code != "unknown_op" {
		t.Fatalf("error code = %q, want unknown_op", code)
	}
}

func TestDriverAPIBlocksRepositoryRequiredIssueThroughAtomicBackend(t *testing.T) {
	h := newTestHarness(t, "")
	h.backend.repositoryBlockResult = &backend.RepositoryRequirementResult{
		Issue:   &backend.IssueData{ID: "TASK-REPO", Status: "blocked"},
		Changed: true,
	}
	resp, decoded := h.do(t, opRequest{
		op: "issue-block-repository-required", body: map[string]any{"issueId": "TASK-REPO"}, headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status/body = %d/%v, want 200", resp.StatusCode, decoded)
	}
	if !slices.Equal(h.backend.repositoryBlocks, []string{"TASK-REPO"}) || decoded["changed"] != true {
		t.Fatalf("repository blocks/body = %v/%v", h.backend.repositoryBlocks, decoded)
	}
	issue, _ := decoded["issue"].(map[string]any)
	if issue["id"] != "TASK-REPO" || issue["status"] != "blocked" {
		t.Fatalf("canonical issue = %v", issue)
	}
}

func TestDriverAPIRepositoryBlockMapsBackendUnavailable(t *testing.T) {
	h := newTestHarness(t, "")
	h.backend.repositoryBlockErr = backend.ErrUnavailable("BlockRepositoryRequired", "fleet unavailable", nil)
	resp, decoded := h.do(t, opRequest{
		op: "issue-block-repository-required", body: map[string]any{"issueId": "TASK-REPO"}, headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusServiceUnavailable || errorCode(t, decoded) != "unavailable" {
		t.Fatalf("status/body = %d/%v, want 503 unavailable", resp.StatusCode, decoded)
	}
	errorBody, _ := decoded["error"].(map[string]any)
	if errorBody["retryable"] != true {
		t.Fatalf("error = %v, want retryable", errorBody)
	}
}

func TestDriverAPIRejectsForeignOwnerCredentials(t *testing.T) {
	h := newTestHarness(t, "")
	headers := h.ownerHeaders()
	headers[HeaderDriverFencingToken] = "999999"
	resp, decoded := h.do(t, opRequest{op: "active-task-runs", headers: headers})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if code := errorCode(t, decoded); code != "not_owner" {
		t.Fatalf("error code = %q, want not_owner", code)
	}
}

func TestDriverAPIRecoverStaleTasksRequiresOwnership(t *testing.T) {
	h := newTestHarness(t, "")

	// Missing owner credentials must not be able to fail this run's tasks.
	resp, decoded := h.do(t, opRequest{
		op:      "recover-stale-tasks",
		headers: map[string]string{HeaderDriverRunID: h.runID},
	})
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status without owner creds = %d, want 401", resp.StatusCode)
	}
	if code := errorCode(t, decoded); code != "unauthenticated" {
		t.Fatalf("error code = %q, want unauthenticated", code)
	}

	resp, _ = h.do(t, opRequest{op: "recover-stale-tasks", headers: h.ownerHeaders()})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status with owner creds = %d, want 200", resp.StatusCode)
	}
}

func TestDriverAPIClaimReady(t *testing.T) {
	h := newTestHarness(t, "")
	h.backend.ready = []backend.IssueData{{ID: "TASK-7", Title: "do the thing"}}

	resp, decoded := h.do(t, opRequest{op: "claim-ready", headers: h.ownerHeaders()})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if decoded["id"] != "TASK-7" {
		t.Fatalf("claimed id = %v, want TASK-7", decoded["id"])
	}
	if decoded["claimedBy"] != "driver-run:run-1" {
		t.Fatalf("claimedBy = %v, want driver-run:run-1", decoded["claimedBy"])
	}
	wantRequestID := execution.ClaimDriverRunWorkItemRequestID(h.runID, "TASK-7")
	wantActionID := execution.DriverRunWorkItemClaimActionID(wantRequestID)
	if decoded["claimActionId"] != wantActionID {
		t.Fatalf("claimActionId = %v, want %q", decoded["claimActionId"], wantActionID)
	}
	if len(h.backend.typedClaims) != 1 || h.backend.typedClaims[0].RequestID != wantRequestID ||
		h.backend.typedClaims[0].Owner.ResourceID != h.runID || h.backend.typedClaims[0].Owner.LeaseToken != "driver-test-token" {
		t.Fatalf("typed claims = %+v, want exact parent owner/request envelope", h.backend.typedClaims)
	}
	if len(h.backend.claimed) != 0 {
		t.Fatalf("generic IssueBackend claim was called: %v", h.backend.claimed)
	}
}

func TestDriverAPIClaimReadyReturnsOnlyCommittedWorkItemMetadata(t *testing.T) {
	h := newTestHarness(t, "")
	h.backend.ready = []backend.IssueData{{
		ID: "TASK-7", Title: "stale title", Priority: 9, IssueType: "stale", Labels: []string{"stale"},
		SourceRepo: "stale/repo", Parent: "STALE-EPIC",
	}}
	h.backend.typedClaimItems = map[string]*execution.DriverRunWorkItem{
		"TASK-7": {
			WorkspaceKey: "TEST", WorkItemID: "TASK-7", Title: "committed title", Status: "in_progress",
			Priority: 1, IssueType: "task", Assignee: "driver-run:run-1", Labels: []string{"committed"},
			SourceRepo: "owner/repo", ParentID: "EPIC-1", UpdatedAt: time.Now().UTC(),
		},
	}

	resp, decoded := h.do(t, opRequest{op: "claim-ready", headers: h.ownerHeaders()})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%v", resp.StatusCode, decoded)
	}
	if decoded["title"] != "committed title" || decoded["priority"] != float64(1) || decoded["issueType"] != "task" ||
		decoded["sourceRepo"] != "owner/repo" || decoded["parent"] != "EPIC-1" {
		t.Fatalf("claim response mixed stale candidate metadata: %#v", decoded)
	}
	labels, ok := decoded["labels"].([]any)
	if !ok || len(labels) != 1 || labels[0] != "committed" {
		t.Fatalf("claim labels=%#v, want committed metadata", decoded["labels"])
	}
}

func TestDriverAPIClaimReadyScansPastTypedClaimConflict(t *testing.T) {
	h := newTestHarness(t, "")
	h.backend.ready = []backend.IssueData{{ID: "TASK-1"}, {ID: "TASK-2"}}
	h.backend.typedClaimErrors = map[string]error{"TASK-1": execution.ErrConflict}

	resp, decoded := h.do(t, opRequest{op: "claim-ready", headers: h.ownerHeaders()})
	if resp.StatusCode != http.StatusOK || decoded["id"] != "TASK-2" {
		t.Fatalf("status/body = %d/%v, want TASK-2 after first conflict", resp.StatusCode, decoded)
	}
	if len(h.backend.typedClaims) != 2 || h.backend.typedClaims[0].WorkItemID != "TASK-1" || h.backend.typedClaims[1].WorkItemID != "TASK-2" {
		t.Fatalf("typed claim scan = %+v, want TASK-1 then TASK-2", h.backend.typedClaims)
	}
}

// TestDriverAPIClaimReadyThreadsTypeFilter proves the claim-ready `type` param
// reaches the ready view server-side (ITEM 3): the op decodes it and threads it
// into ReadyOpts.Type so a caller can claim only, e.g., bugs.
func TestDriverAPIClaimReadyThreadsTypeFilter(t *testing.T) {
	h := newTestHarness(t, "")
	h.backend.ready = []backend.IssueData{{ID: "BUG-1", IssueType: "bug"}}

	resp, decoded := h.do(t, opRequest{
		op:      "claim-ready",
		body:    map[string]any{"type": "bug"},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if decoded["id"] != "BUG-1" {
		t.Fatalf("claimed id = %v, want BUG-1", decoded["id"])
	}
	if len(h.backend.readyOpts) != 1 || h.backend.readyOpts[0].Type != "bug" {
		t.Fatalf("ready opts = %+v, want the type=bug filter threaded to the ready view", h.backend.readyOpts)
	}
}

// TestDriverAPIClaimTaskIgnoresBodyActor is the ITEM 1 security regression:
// presenting a victim's actor label in the claim body must NOT key the lock by
// that label. The lock actor is always derived from the verified run, so a run
// can only ever claim under its own lease — no cross-agent lock takeover.
func TestDriverAPIClaimTaskIgnoresBodyActor(t *testing.T) {
	h := newTestHarness(t, "")
	h.backend.ready = []backend.IssueData{{ID: "TASK-7"}}

	resp, decoded := h.do(t, opRequest{
		op:      "claim-task",
		body:    map[string]any{"taskId": "TASK-7", "actor": "driver-run:victim"},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if decoded["claimedBy"] != "driver-run:run-1" {
		t.Fatalf("claimedBy = %v, want the derived run actor (body actor must be ignored)", decoded["claimedBy"])
	}
	if len(h.backend.typedClaims) != 1 || h.backend.typedClaims[0].Owner.ResourceID != h.runID {
		t.Fatalf("typed claims = %+v, want owner derived from verified run", h.backend.typedClaims)
	}
	if got := h.backend.typedClaims[0].RequestID; got != execution.ClaimDriverRunWorkItemRequestID(h.runID, "TASK-7") {
		t.Fatalf("request id = %q, want exact parent/task claim identity", got)
	}
	if len(h.backend.claimed) != 0 {
		t.Fatalf("generic actor claim was called with body-derived authority: %v", h.backend.claimed)
	}
}

// TestDriverAPIReleaseTaskIgnoresBodyActor is the release half of ITEM 1: a run
// cannot present a victim's actor and release a lock it never held. The release
// ownership actor is always the run's derived actor, so failure-recovery stays
// symmetric with the claim path (same run -> same actor) while cross-agent
// theft is impossible.
func TestDriverAPIReleaseTaskIgnoresBodyActor(t *testing.T) {
	h := newTestHarness(t, "")

	resp, _ := h.do(t, opRequest{
		op:      "release-task",
		body:    map[string]any{"taskId": "TASK-7", "actor": "driver-run:victim"},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	wantClaimActionID := execution.DriverRunWorkItemClaimActionID(execution.ClaimDriverRunWorkItemRequestID(h.runID, "TASK-7"))
	if len(h.backend.typedReleases) != 1 || h.backend.typedReleases[0].Owner.ResourceID != h.runID ||
		h.backend.typedReleases[0].ClaimActionID != wantClaimActionID {
		t.Fatalf("typed releases = %+v, want exact owner and claim action", h.backend.typedReleases)
	}
	if len(h.backend.releases) != 0 {
		t.Fatalf("generic IssueBackend release was called: %+v", h.backend.releases)
	}
}

func TestDriverAPIHandoffReviewBindsExactParentClaimAndChild(t *testing.T) {
	h := newTestHarness(t, "")
	taskID := "TASK-REVIEW-7"
	taskRunID := "review-child-7"

	resp, decoded := h.do(t, opRequest{
		op: "handoff-review",
		body: map[string]any{
			"taskId": taskID, "taskRunId": taskRunID, "status": "closed", "reason": "approved",
		},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusOK || decoded["id"] != taskID || decoded["status"] != "closed" {
		t.Fatalf("status/body = %d/%v, want closed handoff", resp.StatusCode, decoded)
	}
	if len(h.backend.typedHandoffs) != 1 {
		t.Fatalf("typed handoffs = %+v, want one", h.backend.typedHandoffs)
	}
	command := h.backend.typedHandoffs[0]
	wantClaimActionID := execution.DriverRunWorkItemClaimActionID(
		execution.ClaimDriverRunWorkItemRequestID(h.runID, taskID),
	)
	wantRequestID := execution.HandoffDriverRunReviewWorkItemRequestID(h.runID, taskID, taskRunID)
	if command.Owner.ResourceID != h.runID || command.Owner.LeaseToken != "driver-test-token" ||
		command.ClaimActionID != wantClaimActionID || command.RequestID != wantRequestID ||
		command.TaskRunID != taskRunID || command.TargetStatus != "closed" || command.Reason != "approved" {
		t.Fatalf("handoff command = %+v, want exact parent/claim/child envelope", command)
	}
}

func TestDriverAPIHandoffReviewCarriesAtomicReviewAnnotations(t *testing.T) {
	h := newTestHarness(t, "")
	taskID := "TASK-TRIAGE-7"
	taskRunID := "triage-child-7"
	externalRef := "local-branch:loom/TASK-TRIAGE-7@" + strings.Repeat("a", 40)

	resp, decoded := h.do(t, opRequest{
		op: "handoff-review",
		body: map[string]any{
			"taskId": taskID, "taskRunId": taskRunID, "status": "review",
			"priority": 4, "labels": []string{"bug", "triaged"},
			"commentBody": "Automated bug triage completed.",
			"externalRef": externalRef,
		},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusOK || decoded["id"] != taskID || decoded["status"] != "review" {
		t.Fatalf("status/body = %d/%v, want review handoff", resp.StatusCode, decoded)
	}
	if len(h.backend.typedHandoffs) != 1 {
		t.Fatalf("typed handoffs = %+v, want one", h.backend.typedHandoffs)
	}
	command := h.backend.typedHandoffs[0]
	if command.Priority == nil || *command.Priority != 4 ||
		!slices.Equal(command.Labels, []string{"bug", "triaged"}) ||
		command.CommentBody != "Automated bug triage completed." ||
		command.ExternalRef == nil || *command.ExternalRef != externalRef {
		t.Fatalf("handoff annotations = %+v, want exact priority, labels, comment, and ref", command)
	}
}

func TestDriverAPIHandoffReviewRejectsInvalidAnnotationEnvelope(t *testing.T) {
	tests := []struct {
		name string
		body map[string]any
	}{
		{
			name: "review missing priority",
			body: map[string]any{
				"taskId": "TASK-1", "taskRunId": "run-1", "status": "review", "commentBody": "triaged",
			},
		},
		{
			name: "review invalid priority",
			body: map[string]any{
				"taskId": "TASK-1", "taskRunId": "run-1", "status": "review",
				"priority": 5, "commentBody": "triaged",
			},
		},
		{
			name: "review blank comment",
			body: map[string]any{
				"taskId": "TASK-1", "taskRunId": "run-1", "status": "review",
				"priority": 2, "commentBody": "   ",
			},
		},
		{
			name: "review null labels",
			body: map[string]any{
				"taskId": "TASK-1", "taskRunId": "run-1", "status": "review",
				"priority": 2, "labels": nil, "commentBody": "triaged",
			},
		},
		{
			name: "review null external ref",
			body: map[string]any{
				"taskId": "TASK-1", "taskRunId": "run-1", "status": "review",
				"priority": 2, "commentBody": "documented", "externalRef": nil,
			},
		},
		{
			name: "open with priority",
			body: map[string]any{
				"taskId": "TASK-1", "taskRunId": "run-1", "status": "open", "priority": 0,
			},
		},
		{
			name: "closed with empty labels field",
			body: map[string]any{
				"taskId": "TASK-1", "taskRunId": "run-1", "status": "closed", "labels": []string{},
			},
		},
		{
			name: "open with empty comment field",
			body: map[string]any{
				"taskId": "TASK-1", "taskRunId": "run-1", "status": "open", "commentBody": "",
			},
		},
		{
			name: "open with null priority field",
			body: map[string]any{
				"taskId": "TASK-1", "taskRunId": "run-1", "status": "open", "priority": nil,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newTestHarness(t, "")
			resp, decoded := h.do(t, opRequest{
				op: "handoff-review", body: tc.body, headers: h.ownerHeaders(),
			})
			if resp.StatusCode != http.StatusBadRequest || errorCode(t, decoded) != "invalid" {
				t.Fatalf("status/body = %d/%v, want invalid", resp.StatusCode, decoded)
			}
			if len(h.backend.typedHandoffs) != 0 {
				t.Fatalf("invalid handoff reached execution: %+v", h.backend.typedHandoffs)
			}
		})
	}
}

func TestTaskRunRequestMetadataRetainedClaimIsServerOwned(t *testing.T) {
	closeTask := false
	metadata := taskRunRequestMetadata(driverpkg.TaskRunRequestOptions{
		DriverRunID: "run-review-1", CloseTaskOnSuccess: &closeTask, RetainWorkItemClaim: true,
	})
	if metadata[driverpkg.TaskRunCloseOnSuccessMetaKey] != "false" ||
		metadata[driverpkg.TaskRunRetainWorkItemClaimMetaKey] != "true" {
		t.Fatalf("metadata = %+v, want non-closing retained review policy", metadata)
	}

	h := newTestHarness(t, "")
	resp, decoded := h.do(t, opRequest{
		op: "exec-task",
		body: map[string]any{
			"taskId": "TASK-1", "taskRunId": "review-child-1", "retainWorkItemClaim": true,
		},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusBadRequest || errorCode(t, decoded) != "invalid" {
		t.Fatalf("retain without closeTask=false status/body=%d/%v", resp.StatusCode, decoded)
	}
}

func TestDriverAPIEpicGet(t *testing.T) {
	h := newTestHarness(t, "")
	h.backend.epic = &backend.IssueDetailData{IssueData: backend.IssueData{ID: "EPIC-1", Title: "epic"}}

	resp, decoded := h.do(t, opRequest{op: "epic-get", headers: h.ownerHeaders()})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if decoded["id"] != "EPIC-1" {
		t.Fatalf("epic id = %v, want EPIC-1", decoded["id"])
	}
}

func TestDriverAPIActiveTaskRunsEmpty(t *testing.T) {
	h := newTestHarness(t, "")
	resp, decoded := h.do(t, opRequest{op: "active-task-runs", headers: h.ownerHeaders()})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if decoded["driverRunId"] != "run-1" {
		t.Fatalf("driverRunId = %v, want run-1", decoded["driverRunId"])
	}
	if count, ok := decoded["activeCount"].(float64); !ok || count != 0 {
		t.Fatalf("activeCount = %v, want 0", decoded["activeCount"])
	}
}

func TestDriverAPIExecTaskEnqueueUnschedulable(t *testing.T) {
	h := newTestHarness(t, "")
	resp, decoded := h.do(t, opRequest{
		op: "exec-task",
		body: map[string]any{
			"taskId":          "TASK-9",
			"taskRunId":       "task-run-unschedulable",
			"providerProfile": "local-noop",
			"enqueueOnly":     true,
		},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	if code := errorCode(t, decoded); code != "unschedulable" {
		t.Fatalf("error code = %q, want unschedulable", code)
	}
	envelope := decoded["error"].(map[string]any)
	if retryable, _ := envelope["retryable"].(bool); !retryable {
		t.Fatalf("retryable = %v, want true", envelope["retryable"])
	}
	children, err := h.store.TaskRuns().List(context.Background(), "WS", store.TaskRunFilter{DriverRunID: h.runID})
	if err != nil {
		t.Fatalf("List children: %v", err)
	}
	if len(children) != 0 {
		t.Fatalf("children = %+v, want none for unschedulable enqueue", children)
	}
}

func TestDriverAPITaskRunGetNotFound(t *testing.T) {
	h := newTestHarness(t, "")
	resp, decoded := h.do(t, opRequest{
		op:      "task-run-get",
		body:    map[string]string{"taskRunId": "missing-run"},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if code := errorCode(t, decoded); code != "not_found" {
		t.Fatalf("error code = %q, want not_found", code)
	}
}

func TestDriverAPIInvalidParams(t *testing.T) {
	h := newTestHarness(t, "")
	resp, decoded := h.do(t, opRequest{
		op:      "deliver-agent-message",
		body:    map[string]string{"agent": "", "message": ""},
		headers: h.ownerHeaders(),
	})
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if code := errorCode(t, decoded); code != "invalid" {
		t.Fatalf("error code = %q, want invalid", code)
	}
}
