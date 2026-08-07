package serve

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	agentsmodule "github.com/tysonthomas9/loomcli/internal/modules/agents"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type convergenceAgentQueries struct {
	agents []*agentsmodule.Agent
}

func (queries convergenceAgentQueries) GetAgent(_ context.Context, workspace, agentID string) (*agentsmodule.Agent, error) {
	for _, agent := range queries.agents {
		if agent != nil && agent.WorkspaceKey == workspace && agent.AgentID == agentID {
			return agent, nil
		}
	}
	return nil, domain.ErrNotFound
}

func (queries convergenceAgentQueries) ListAgents(_ context.Context, workspace string, _ agentsmodule.AgentFilter) ([]*agentsmodule.Agent, error) {
	out := make([]*agentsmodule.Agent, 0, len(queries.agents))
	for _, agent := range queries.agents {
		if agent != nil && agent.WorkspaceKey == workspace {
			out = append(out, agent)
		}
	}
	return out, nil
}

func TestExecutionTaskRunConvergenceAdaptersAreIdempotentAndTokenFree(t *testing.T) {
	ctx := context.Background()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "WS", Name: "workspace"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.WorkerProfiles().Create(ctx, store.WorkerProfileCreate{
		WorkspaceKey: "WS", ProfileID: "lead-profile", Name: "lead-profile", Role: "lead", ParentEpic: "EPIC-1",
	}); err != nil {
		t.Fatal(err)
	}
	agentQueries := convergenceAgentQueries{agents: []*agentsmodule.Agent{{
		WorkspaceKey: "WS", AgentID: "lead-1", Name: "lead-1", Kind: agentsmodule.AgentKindLead,
		Behavior: agentsmodule.BehaviorReference{RoleName: "lead"}, ProfileName: "lead-profile",
	}}}
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "WS", DriverID: "driver-1", Name: "driver", OwnerType: workflowcatalog.DriverOwnerSystem,
		Status: workflowcatalog.DriverStatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "version-1", DriverID: "driver-1", Version: 1,
		SourceDigest: "sha256:source", BundleDigest: "sha256:bundle", ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatal(err)
	}
	parent, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: "WS", RunID: "run-1", DriverID: "driver-1", DriverVersionID: "version-1",
		SourceKind: "manual", SourceRef: "test", EpicID: "EPIC-1", IdempotencyKey: "run-request-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	parent, err = st.DriverRuns().Claim(ctx, "WS", parent.RunID, "driver-node", "driver-lease")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DriverSteps().Create(ctx, store.DriverStepCreate{
		WorkspaceKey: "WS", StepID: "step-1", DriverRunID: parent.RunID, StepKind: "task_run",
		Status: domain.DriverStepRunning, NodeID: parent.NodeID, LeaseID: parent.LeaseID, FencingToken: parent.FencingToken,
	}); err != nil {
		t.Fatal(err)
	}
	const token = "worker-secret-token"
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS", TaskRunID: "task-run-1", DriverRunID: parent.RunID, DriverStepID: "step-1",
		TaskID: "TASK-1", Status: domain.TaskRunRunning, NodeID: "worker-node", LeaseID: "worker-lease",
		LeaseToken: token, FencingToken: 11, RuntimeMetadata: map[string]string{"scheduler_attempt": "1"},
	}); err != nil {
		t.Fatal(err)
	}
	exitCode := 0
	finished, err := st.TaskRuns().Complete(ctx, "WS", "task-run-1", store.TaskRunComplete{
		CompletionID: "complete-1", NodeID: "worker-node", LeaseID: "worker-lease", LeaseToken: token,
		FencingToken: 11, Status: domain.TaskRunCompleted, ExitCode: &exitCode,
		LogsRef: "logs://1", ArtifactsRef: "artifacts://1", FinishedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DriverRuns().Finish(ctx, "WS", parent.RunID, store.DriverRunFinish{
		NodeID: parent.NodeID, LeaseID: parent.LeaseID, FencingToken: parent.FencingToken,
		Status: domain.DriverRunCompleted, Summary: "parent completed before projection repair",
	}); err != nil {
		t.Fatal(err)
	}

	repairStore, ok := st.DriverSteps().(store.TerminalDriverStepRepairStore)
	if !ok {
		t.Fatal("memstore DriverSteps does not expose terminal repair store")
	}
	checkpoints, ok := st.TaskRuns().(store.TaskRunTerminalConvergenceStore)
	if !ok {
		t.Fatal("memstore TaskRuns does not expose terminal convergence checkpoints")
	}
	dependencies, err := NewExecutionTaskRunConvergenceDependencies(ExecutionTaskRunConvergenceDependencies{
		TaskRuns: st.TaskRuns(), Checkpoints: checkpoints, DriverRuns: st.DriverRuns(), DriverSteps: repairStore,
		Events: st.TaskRunEvents(), AgentQueries: agentQueries, WorkerProfiles: st.WorkerProfiles(), Outbox: st.Outbox(),
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := dependencies.Source.GetTerminalTaskRun(ctx, "WS", finished.TaskRunID)
	if err != nil {
		t.Fatal(err)
	}
	event := execution.TaskRunTerminalEvent{
		WorkspaceKey: record.WorkspaceKey, EventID: "task-run-1#1#taskRunCompleted", EpicID: record.EpicID,
		DriverRunID: record.DriverRunID, WorkItemID: record.WorkItemID, TaskRunID: record.TaskRunID,
		Type: execution.TaskRunTerminalCompleted, Status: record.Status, Attempt: record.Attempt,
		LogsRef: record.LogsRef, ArtifactsRef: record.ArtifactsRef, OccurredAt: record.FinishedAt,
	}
	for range 2 {
		if err := dependencies.Events.EnsureTaskRunTerminalEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	projection := execution.DriverStepTerminalProjection{
		RequestID:    "repair-step-1",
		WorkspaceKey: "WS", StepID: "step-1", DriverRunID: "run-1", TaskRunID: "task-run-1",
		Status: "completed", OutputRef: "artifacts://1",
	}
	for range 2 {
		if _, err := dependencies.DriverSteps.RepairTerminalDriverStep(ctx, projection); err != nil {
			t.Fatal(err)
		}
	}
	conflicting := projection
	conflicting.RequestID = "repair-step-1-conflict"
	conflicting.Status = "failed"
	if _, err := dependencies.DriverSteps.RepairTerminalDriverStep(ctx, conflicting); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("conflicting terminal repair error = %v, want invalid transition", err)
	}
	lead, err := dependencies.LeadResolver.ResolveEpicLead(ctx, "WS", "EPIC-1")
	if err != nil {
		t.Fatal(err)
	}
	notification := execution.LeadTaskNotification{
		WorkspaceKey: "WS", EpicID: "EPIC-1", DriverRunID: "run-1", TaskRunID: "task-run-1",
		WorkItemID: "TASK-1", TargetAgent: lead, Status: execution.StatusSucceeded,
		LogsRef: "logs://1", ArtifactsRef: "artifacts://1", DedupeKey: "lead-task-message:EPIC-1:task-run-1:succeeded",
	}
	for range 2 {
		if err := dependencies.Notifications.EnsureLeadTaskNotification(ctx, notification); err != nil {
			t.Fatal(err)
		}
	}
	events, err := st.TaskRunEvents().ListSince(ctx, "WS", store.TaskRunEventFilter{})
	if err != nil || len(events) != 1 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	wire, err := json.Marshal(events[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), token) || strings.Contains(string(wire), "leaseToken") || strings.Contains(string(wire), "lease_token") {
		t.Fatalf("event exposed lease credential: %s", wire)
	}
	step, err := st.DriverSteps().Get(ctx, "WS", "step-1")
	if err != nil || step.Status != domain.DriverStepCompleted || step.OutputRef != "artifacts://1" {
		t.Fatalf("step=%+v err=%v", step, err)
	}
	rows, err := st.Outbox().ListDue(ctx, "WS", store.OutboxDueFilter{Now: time.Now().UTC()})
	if err != nil || len(rows) != 1 {
		t.Fatalf("outbox=%+v err=%v", rows, err)
	}
}

func TestExecutionTaskRunConvergenceAdapterAcceptsCancelledToSkippedOnly(t *testing.T) {
	ctx := context.Background()
	st, parent := setupExecutionTaskRunParent(t, ctx)
	if _, err := st.DriverSteps().CreateForRun(ctx, "WS", parent.RunID, store.DriverStepCreate{
		StepID: "step-cancelled", StepKind: "task_run", Status: domain.DriverStepRunning,
		NodeID: parent.NodeID, LeaseID: parent.LeaseID, FencingToken: parent.FencingToken,
	}); err != nil {
		t.Fatal(err)
	}
	const token = "cancelled-secret"
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "WS", TaskRunID: "task-run-cancelled", DriverRunID: parent.RunID, DriverStepID: "step-cancelled",
		TaskID: "TASK-CANCELED", Status: domain.TaskRunRunning, NodeID: "worker", LeaseID: "lease",
		LeaseToken: token, FencingToken: 8,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.TaskRuns().Finish(ctx, "WS", "task-run-cancelled", store.TaskRunFinish{
		NodeID: "worker", LeaseID: "lease", LeaseToken: token, FencingToken: 8,
		Status: domain.TaskRunCancelled, FinishedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	repairs := st.DriverSteps().(store.TerminalDriverStepRepairStore)
	checkpoints := st.TaskRuns().(store.TaskRunTerminalConvergenceStore)
	dependencies, err := NewExecutionTaskRunConvergenceDependencies(ExecutionTaskRunConvergenceDependencies{
		TaskRuns: st.TaskRuns(), Checkpoints: checkpoints, DriverRuns: st.DriverRuns(), DriverSteps: repairs,
		Events: st.TaskRunEvents(), AgentQueries: convergenceAgentQueries{}, WorkerProfiles: st.WorkerProfiles(), Outbox: st.Outbox(),
	})
	if err != nil {
		t.Fatal(err)
	}
	projection := execution.DriverStepTerminalProjection{
		RequestID: "repair-cancelled", WorkspaceKey: "WS", StepID: "step-cancelled",
		DriverRunID: parent.RunID, TaskRunID: "task-run-cancelled", Status: "skipped",
	}
	if _, err := dependencies.DriverSteps.RepairTerminalDriverStep(ctx, projection); err != nil {
		t.Fatal(err)
	}
	projection.RequestID = "repair-cancelled-conflict"
	projection.Status = "failed"
	if _, err := dependencies.DriverSteps.RepairTerminalDriverStep(ctx, projection); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("cancelled->failed error=%v, want invalid transition", err)
	}
}
