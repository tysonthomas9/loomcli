package serve

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type executionTestClaimPort struct{}

func (executionTestClaimPort) ReplayTaskRunRequest(context.Context, execution.RequestTaskRunCommand) (execution.RequestTaskRunResult, error) {
	return execution.RequestTaskRunResult{}, execution.ErrUnavailable
}

func (executionTestClaimPort) RequestTaskRun(context.Context, execution.RequestTaskRunCommand) (execution.RequestTaskRunResult, error) {
	return execution.RequestTaskRunResult{}, execution.ErrUnavailable
}

func (executionTestClaimPort) ClaimTaskRun(context.Context, execution.ClaimTaskRunCommand) (execution.ClaimTaskRunResult, error) {
	return execution.ClaimTaskRunResult{}, execution.ErrUnavailable
}

func (executionTestClaimPort) UpdateTaskRunWorkItemDesign(context.Context, execution.UpdateTaskRunWorkItemDesignCommand) (execution.UpdateTaskRunWorkItemDesignResult, error) {
	return execution.UpdateTaskRunWorkItemDesignResult{}, execution.ErrUnavailable
}

func (executionTestClaimPort) RequeueTaskRun(context.Context, execution.RequeueTaskRunCommand) (execution.RequeueTaskRunResult, error) {
	return execution.RequeueTaskRunResult{}, execution.ErrUnavailable
}

func (executionTestClaimPort) ExhaustTaskRunRetries(context.Context, execution.ExhaustTaskRunRetriesCommand) (execution.ExhaustTaskRunRetriesResult, error) {
	return execution.ExhaustTaskRunRetriesResult{}, execution.ErrUnavailable
}

func executionTestDependencies(t *testing.T, st store.Store) ExecutionDependencies {
	t.Helper()
	repairs, ok := st.DriverSteps().(store.TerminalDriverStepRepairStore)
	if !ok {
		t.Fatal("test DriverStep store lacks terminal repair support")
	}
	return ExecutionDependencies{
		TaskRuns: st.TaskRuns(), DriverRuns: st.DriverRuns(), DriverSteps: st.DriverSteps(),
		TerminalStepRepairs: repairs, TaskRunEvents: st.TaskRunEvents(), Nodes: st.Nodes(),
		WorkerProfiles: st.WorkerProfiles(), Agents: st.Agents(), Outbox: st.Outbox(), Awaits: st.Awaits(),
		TriggerEvents: st.TriggerEvents(), Workspaces: st.Workspaces(),
		AtomicTaskRunRequests: executionTestClaimPort{}, AtomicTaskRunClaims: executionTestClaimPort{},
		AtomicTaskRunWorkItemDesign: executionTestClaimPort{},
		AtomicTaskRunRequeues:       executionTestClaimPort{}, AtomicTaskRunRetryExhaustion: executionTestClaimPort{},
		AllowLegacyStoreAdapters: true,
	}
}

func TestExecutionCapabilityOwnsTaskRunMutationAndForwardsOpaqueToken(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	owner := execution.Owner{
		ResourceKind: execution.ResourceTaskRun,
		ResourceID:   "task-run-1",
		NodeID:       "node-1",
		LeaseID:      "lease-1",
		LeaseToken:   "secret-token",
		FencingToken: 42,
	}
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS", TaskRunID: owner.ResourceID, TaskID: "TASK-1",
		Status: domain.TaskRunRunning, NodeID: owner.NodeID, LeaseID: owner.LeaseID,
		LeaseToken: owner.LeaseToken, FencingToken: owner.FencingToken,
	}); err != nil {
		t.Fatalf("create TaskRun: %v", err)
	}

	capability, err := NewExecutionCapability(executionTestDependencies(t, st))
	if err != nil {
		t.Fatalf("NewExecutionCapability: %v", err)
	}
	auth, err := capability.TaskRunAuthorityResolver().ResolveTaskRunAuthority(
		ctx, "WS", execution.ActionHeartbeat, owner,
	)
	if err != nil {
		t.Fatalf("resolve heartbeat authority: %v", err)
	}
	if _, err := capability.TaskRunAPI().Heartbeat(ctx, auth, execution.HeartbeatCommand{
		WorkspaceKey: "WS", Owner: owner, At: time.Now().UTC(),
		RuntimeMetadata: map[string]string{"phase": "working"},
	}); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	run, err := st.TaskRuns().Get(ctx, "WS", owner.ResourceID)
	if err != nil || run.RuntimeMetadata["phase"] != "working" {
		t.Fatalf("persisted heartbeat = %+v, err=%v", run, err)
	}

	encoded, err := json.Marshal(owner)
	if err != nil {
		t.Fatalf("marshal owner: %v", err)
	}
	if strings.Contains(string(encoded), owner.LeaseToken) {
		t.Fatalf("Owner leaked LeaseToken: %s", encoded)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, leaked := decoded["LeaseToken"]; leaked {
		t.Fatalf("Owner leaked LeaseToken: %s", encoded)
	}
}

func TestExecutionAuthorityResolversAdmitOnlyRegisteredChildCascadeLanes(t *testing.T) {
	capability, err := NewExecutionCapability(executionTestDependencies(t, memstore.New()))
	if err != nil {
		t.Fatal(err)
	}
	owner := execution.Owner{
		ResourceKind: execution.ResourceDriverRun, ResourceID: "terminal-parent",
		NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "driver-token", FencingToken: 7,
	}
	if _, err := capability.DriverRunAuthorityResolver().ResolveDriverRunAuthority(
		t.Context(), "WS", execution.ActionCascadeChildDriverRuns, owner,
	); err != nil {
		t.Fatalf("resolve live child cascade authority: %v", err)
	}
	foreign := owner
	foreign.ResourceKind = execution.ResourceTaskRun
	if _, err := capability.DriverRunAuthorityResolver().ResolveDriverRunAuthority(
		t.Context(), "WS", execution.ActionCascadeChildDriverRuns, foreign,
	); err == nil {
		t.Fatal("task-run owner resolved DriverRun cascade authority")
	}
	if _, err := capability.SystemAuthorityResolver().ResolveExecutionSystemAuthority(
		t.Context(), "WS", execution.ActionRecoverChildDriverRunCascade, string(execution.DriverRunOutcomeComponentID),
	); err != nil {
		t.Fatalf("resolve outcome cascade recovery authority: %v", err)
	}
	if _, err := capability.SystemAuthorityResolver().ResolveExecutionSystemAuthority(
		t.Context(), "WS", execution.ActionRecoverChildDriverRunCascade, string(execution.DriverExecutorComponentID),
	); err == nil {
		t.Fatal("driver executor resolved outcome-only cascade recovery authority")
	}
}

func TestExecutionTaskRunAuthorityResolverAllowsExactWorkItemDesignAction(t *testing.T) {
	capability, err := NewExecutionCapability(executionTestDependencies(t, memstore.New()))
	if err != nil {
		t.Fatal(err)
	}
	owner := execution.Owner{
		ResourceKind: execution.ResourceTaskRun, ResourceID: "task-run-1",
		NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "task-token", FencingToken: 7,
	}
	auth, err := capability.TaskRunAuthorityResolver().ResolveTaskRunAuthority(
		t.Context(), "WS", execution.ActionUpdateTaskRunWorkItemDesign, owner,
	)
	if err != nil {
		t.Fatalf("resolve Work Item design authority: %v", err)
	}
	if auth.Action() != execution.ActionUpdateTaskRunWorkItemDesign || auth.ResourceID() != owner.ResourceID {
		t.Fatalf("resolved authority action=%q resource=%q", auth.Action(), auth.ResourceID())
	}
	foreign := owner
	foreign.ResourceKind = execution.ResourceDriverRun
	if _, err := capability.TaskRunAuthorityResolver().ResolveTaskRunAuthority(
		t.Context(), "WS", execution.ActionUpdateTaskRunWorkItemDesign, foreign,
	); err == nil {
		t.Fatal("DriverRun owner resolved TaskRun Work Item design authority")
	}
}

func TestExecutionSystemAuthorityResolverKeepsReconciliationQueuesComponentScoped(t *testing.T) {
	capability, err := NewExecutionCapability(executionTestDependencies(t, memstore.New()))
	if err != nil {
		t.Fatal(err)
	}
	resolver := capability.SystemAuthorityResolver()
	for _, tc := range []struct {
		component string
		actions   []authority.Action
	}{
		{
			component: string(AwaitEventNotificationComponentID),
			actions: []authority.Action{
				execution.ActionClaimAwaitEventNotifications,
				execution.ActionCompleteAwaitEventNotification,
				execution.ActionRetryAwaitEventNotification,
			},
		},
		{
			component: string(execution.DriverRunOutcomeComponentID),
			actions: []authority.Action{
				execution.ActionClaimDriverRunOutcomes,
				execution.ActionCompleteDriverRunOutcome,
				execution.ActionRetryDriverRunOutcome,
			},
		},
	} {
		for _, action := range tc.actions {
			if _, err := resolver.ResolveExecutionSystemAuthority(t.Context(), "WS", action, tc.component); err != nil {
				t.Fatalf("resolve %s for %s: %v", action, tc.component, err)
			}
		}
	}
	if _, err := resolver.ResolveExecutionSystemAuthority(
		t.Context(), "WS", execution.ActionClaimDriverRunOutcomes, string(AwaitEventNotificationComponentID),
	); err == nil {
		t.Fatal("await-event component resolved DriverRun-outcome queue authority")
	}
	if _, err := resolver.ResolveExecutionSystemAuthority(
		t.Context(), "WS", execution.ActionClaimAwaitEventNotifications, string(execution.DriverRunOutcomeComponentID),
	); err == nil {
		t.Fatal("DriverRun-outcome component resolved await-event queue authority")
	}
}

func TestExecutionCapabilityRejectsWrongTaskRunLeaseToken(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	owner := execution.Owner{
		ResourceKind: execution.ResourceTaskRun, ResourceID: "task-run-1",
		NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "secret-token", FencingToken: 42,
	}
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS", TaskRunID: owner.ResourceID, TaskID: "TASK-1",
		Status: domain.TaskRunRunning, NodeID: owner.NodeID, LeaseID: owner.LeaseID,
		LeaseToken: owner.LeaseToken, FencingToken: owner.FencingToken,
	}); err != nil {
		t.Fatal(err)
	}
	capability, err := NewExecutionCapability(executionTestDependencies(t, st))
	if err != nil {
		t.Fatal(err)
	}
	wrong := owner
	wrong.LeaseToken = "wrong-token"
	auth, err := capability.TaskRunAuthorityResolver().ResolveTaskRunAuthority(
		ctx, "WS", execution.ActionHeartbeat, wrong,
	)
	if err != nil {
		t.Fatalf("resolve structurally valid owner: %v", err)
	}
	_, err = capability.TaskRunAPI().Heartbeat(ctx, auth, execution.HeartbeatCommand{
		WorkspaceKey: "WS", Owner: wrong, At: time.Now().UTC(),
	})
	if !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("wrong-token heartbeat error = %v, want ErrNotOwner", err)
	}
}
