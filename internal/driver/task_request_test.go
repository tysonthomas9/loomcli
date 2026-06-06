package driver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestRequestTaskRunCreatesExecutesAndFinishesChild(t *testing.T) {
	ctx, st, run := setupRunningDriverRun(t)
	if _, err := st.DriverSteps().Create(ctx, store.DriverStepCreate{
		WorkspaceKey: "TEST",
		StepID:       "step-1",
		DriverRunID:  run.RunID,
		StepKind:     "task_run",
		Status:       domain.DriverStepQueued,
		NodeID:       run.NodeID,
		LeaseID:      run.LeaseID,
		FencingToken: run.FencingToken,
	}); err != nil {
		t.Fatalf("Create driver step: %v", err)
	}
	executor := &recordingTaskExecutor{result: TaskExecResult{
		ExitCode:         0,
		LogsRef:          "logs://task-run-1",
		ArtifactsRef:     "artifacts://task-run-1",
		ArtifactIDs:      []string{" artifact-1 ", "artifact-2", "artifact-1", ""},
		InputTokens:      11,
		OutputTokens:     7,
		CacheReadTokens:  5,
		CacheWriteTokens: 3,
		EstimatedCostUSD: 0.125,
		RuntimeMetadata:  map[string]string{"duration_ms": "42"},
	}}

	outcome, err := RequestTaskRunWithResult(ctx, st, TaskRunRequestOptions{
		WorkspaceKey:    "TEST",
		DriverRunID:     run.RunID,
		DriverStepID:    "step-1",
		TaskRunID:       "task-run-1",
		TaskID:          "TEST-1",
		ProviderProfile: "codex-default",
		ParentNodeID:    run.NodeID,
		ParentLeaseID:   run.LeaseID,
		ParentFence:     run.FencingToken,
		NodeID:          "node-2",
		RunnerID:        "runner-2",
		SandboxPlacement: domain.TaskRunPlacement{
			Provider:  "codex-default",
			SandboxID: "sandbox-1",
			CWD:       "/workspace",
		},
	}, executor)
	if err != nil {
		t.Fatalf("RequestTaskRun: %v", err)
	}
	final := outcome.Run
	if final.Status != domain.TaskRunCompleted || final.DriverRunID != run.RunID || final.DriverStepID != "step-1" || final.TaskID != "TEST-1" {
		t.Fatalf("final = %+v, want completed child linked to parent", final)
	}
	if final.NodeID != "node-2" || final.ProviderProfile != "codex-default" {
		t.Fatalf("final node/provider = %q/%q, want node-2/codex-default", final.NodeID, final.ProviderProfile)
	}
	if final.LeaseID != "task-run-1-lease" || final.FencingToken == 0 {
		t.Fatalf("final lease/fence = %q/%d, want generated lease with fence", final.LeaseID, final.FencingToken)
	}
	if final.ExitCode == nil || *final.ExitCode != 0 {
		t.Fatalf("exit code = %v, want 0", final.ExitCode)
	}
	if final.InputTokens != 11 || final.OutputTokens != 7 || final.CacheReadTokens != 5 || final.CacheWriteTokens != 3 || final.EstimatedCostUSD != 0.125 {
		t.Fatalf("final usage = %+v, want executor usage", final)
	}
	result := TaskRunResultFromOutcome(outcome)
	if result.DriverStepID != "step-1" {
		t.Fatalf("result driver step id = %q, want step-1", result.DriverStepID)
	}
	if result.InputTokens != 11 || result.OutputTokens != 7 || result.CacheReadTokens != 5 || result.CacheWriteTokens != 3 || result.EstimatedCostUSD != 0.125 {
		t.Fatalf("result usage = %+v, want executor usage", result)
	}
	if len(result.ArtifactIDs) != 2 || result.ArtifactIDs[0] != "artifact-1" || result.ArtifactIDs[1] != "artifact-2" {
		t.Fatalf("result artifact IDs = %+v, want normalized executor artifact IDs", result.ArtifactIDs)
	}
	if final.RuntimeMetadata["driver_run_id"] != run.RunID || final.RuntimeMetadata["driver_step_id"] != "step-1" || final.RuntimeMetadata["duration_ms"] != "42" {
		t.Fatalf("metadata = %+v, want driver_run_id and executor metadata", final.RuntimeMetadata)
	}
	if executor.req.TaskRunID != "task-run-1" || executor.req.DriverRunID != run.RunID || executor.req.DriverStepID != "step-1" {
		t.Fatalf("executor req = %+v, want child and parent ids", executor.req)
	}
	if executor.req.LeaseID != final.LeaseID || executor.req.FencingToken != final.FencingToken {
		t.Fatalf("executor lease/fence = %q/%d, want final %q/%d", executor.req.LeaseID, executor.req.FencingToken, final.LeaseID, final.FencingToken)
	}
	if executor.req.RunnerPlacement.RunnerID != "runner-2" || executor.req.SandboxPlacement.SandboxID != "sandbox-1" || final.SandboxPlacement.SandboxID != "sandbox-1" {
		t.Fatalf("executor/final placement = %+v/%+v final=%+v, want runner-2 sandbox-1", executor.req.RunnerPlacement, executor.req.SandboxPlacement, final.SandboxPlacement)
	}
	children, err := st.TaskRuns().List(ctx, "TEST", store.TaskRunFilter{DriverRunID: run.RunID})
	if err != nil {
		t.Fatalf("List children: %v", err)
	}
	if len(children) != 1 || children[0].TaskRunID != "task-run-1" {
		t.Fatalf("children = %+v, want task-run-1", children)
	}
	stepChildren, err := st.TaskRuns().List(ctx, "TEST", store.TaskRunFilter{DriverStepID: "step-1"})
	if err != nil {
		t.Fatalf("List step children: %v", err)
	}
	if len(stepChildren) != 1 || stepChildren[0].TaskRunID != "task-run-1" {
		t.Fatalf("step children = %+v, want task-run-1", stepChildren)
	}
	step, err := st.DriverSteps().Get(ctx, "TEST", "step-1")
	if err != nil {
		t.Fatalf("Get driver step: %v", err)
	}
	if step.Status != domain.DriverStepCompleted || step.TaskRunID != "task-run-1" || step.OutputRef != "artifacts://task-run-1" || step.EndedAt == nil {
		t.Fatalf("step = %+v, want completed step linked to task output", step)
	}
	parent, err := st.DriverRuns().Get(ctx, "TEST", run.RunID)
	if err != nil {
		t.Fatalf("Get parent: %v", err)
	}
	if parent.Status != domain.DriverRunRunning {
		t.Fatalf("parent status = %s, want still running", parent.Status)
	}
}

func TestRequestTaskRunCompatibilityWrapperReturnsRun(t *testing.T) {
	ctx, st, run := setupRunningDriverRun(t)
	final, err := RequestTaskRun(ctx, st, TaskRunRequestOptions{
		WorkspaceKey:    "TEST",
		DriverRunID:     run.RunID,
		TaskRunID:       "task-run-wrapper",
		TaskID:          "TEST-1",
		ProviderProfile: "local-noop",
		ParentNodeID:    run.NodeID,
		ParentLeaseID:   run.LeaseID,
		ParentFence:     run.FencingToken,
	}, LocalTaskExecutor{})
	if err != nil {
		t.Fatalf("RequestTaskRun: %v", err)
	}
	if final.TaskRunID != "task-run-wrapper" || final.Status != domain.TaskRunCompleted {
		t.Fatalf("final = %+v, want completed task-run-wrapper", final)
	}
}

func TestRequestTaskRunUnsupportedProviderRecordsFailedChild(t *testing.T) {
	ctx, st, run := setupRunningDriverRun(t)

	final, err := RequestTaskRun(ctx, st, TaskRunRequestOptions{
		WorkspaceKey:    "TEST",
		DriverRunID:     run.RunID,
		TaskRunID:       "task-run-unsupported",
		TaskID:          "TEST-2",
		ProviderProfile: "flue-daytona",
		ParentNodeID:    run.NodeID,
		ParentLeaseID:   run.LeaseID,
		ParentFence:     run.FencingToken,
	}, LocalTaskExecutor{})
	if err != nil {
		t.Fatalf("RequestTaskRun unsupported provider: %v", err)
	}
	if final.Status != domain.TaskRunFailed || final.ErrorClass != "provider_unsupported" {
		t.Fatalf("final = %+v, want failed provider_unsupported", final)
	}
	if final.ExitCode == nil || *final.ExitCode != 2 {
		t.Fatalf("exit code = %v, want 2", final.ExitCode)
	}
	if final.ErrorMessage == "" {
		t.Fatal("unsupported provider did not record an error message")
	}
	parent, err := st.DriverRuns().Get(ctx, "TEST", run.RunID)
	if err != nil {
		t.Fatalf("Get parent: %v", err)
	}
	if parent.Status != domain.DriverRunRunning {
		t.Fatalf("parent status = %s, want still running", parent.Status)
	}
}

func TestRequestTaskRunExecutorErrorRecordsFailedChild(t *testing.T) {
	ctx, st, run := setupRunningDriverRun(t)
	executor := &recordingTaskExecutor{err: errors.New("agent command failed")}

	final, err := RequestTaskRun(ctx, st, TaskRunRequestOptions{
		WorkspaceKey:    "TEST",
		DriverRunID:     run.RunID,
		TaskRunID:       "task-run-error",
		TaskID:          "TEST-3",
		ProviderProfile: "codex-default",
		ParentNodeID:    run.NodeID,
		ParentLeaseID:   run.LeaseID,
		ParentFence:     run.FencingToken,
	}, executor)
	if err != nil {
		t.Fatalf("RequestTaskRun executor error: %v", err)
	}
	if final.Status != domain.TaskRunFailed || final.ErrorClass != "task_executor_error" {
		t.Fatalf("final = %+v, want failed task_executor_error", final)
	}
	if final.ExitCode == nil || *final.ExitCode != 1 {
		t.Fatalf("exit code = %v, want 1", final.ExitCode)
	}
	if final.ErrorMessage != "agent command failed" {
		t.Fatalf("error message = %q, want executor error", final.ErrorMessage)
	}
}

func TestRequestTaskRunHeartbeatsChildDuringExecution(t *testing.T) {
	ctx, st, run := setupRunningDriverRun(t)
	executor := &waitingTaskHeartbeatExecutor{store: st}

	final, err := RequestTaskRun(ctx, st, TaskRunRequestOptions{
		WorkspaceKey:      "TEST",
		DriverRunID:       run.RunID,
		TaskRunID:         "task-run-heartbeat",
		TaskID:            "TEST-6",
		ProviderProfile:   "codex-default",
		ParentNodeID:      run.NodeID,
		ParentLeaseID:     run.LeaseID,
		ParentFence:       run.FencingToken,
		NodeID:            "node-2",
		HeartbeatInterval: time.Millisecond,
	}, executor)
	if err != nil {
		t.Fatalf("RequestTaskRun: %v", err)
	}
	if final.Status != domain.TaskRunCompleted {
		t.Fatalf("final status = %s, want completed", final.Status)
	}
	if !executor.sawHeartbeat {
		t.Fatal("executor did not observe task run heartbeat metadata during execution")
	}
}

func TestRequestTaskRunRequiresRunningParent(t *testing.T) {
	ctx, st, run := setupQueuedDriverRun(t)

	if _, err := RequestTaskRun(ctx, st, TaskRunRequestOptions{
		WorkspaceKey:    "TEST",
		DriverRunID:     run.RunID,
		TaskRunID:       "task-run-queued-parent",
		TaskID:          "TEST-4",
		ProviderProfile: "local-noop",
	}, LocalTaskExecutor{}); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("RequestTaskRun queued parent err = %v, want ErrInvalidTransition", err)
	}
	children, err := st.TaskRuns().List(ctx, "TEST", store.TaskRunFilter{DriverRunID: run.RunID})
	if err != nil {
		t.Fatalf("List children: %v", err)
	}
	if len(children) != 0 {
		t.Fatalf("children = %+v, want none for queued parent", children)
	}
}

func TestRequestTaskRunRejectsStaleParentOwner(t *testing.T) {
	ctx, st, run := setupRunningDriverRun(t)

	if _, err := RequestTaskRun(ctx, st, TaskRunRequestOptions{
		WorkspaceKey:    "TEST",
		DriverRunID:     run.RunID,
		TaskRunID:       "task-run-stale-parent",
		TaskID:          "TEST-5",
		ProviderProfile: "local-noop",
		ParentNodeID:    run.NodeID,
		ParentLeaseID:   "stale-lease",
		ParentFence:     run.FencingToken,
	}, LocalTaskExecutor{}); !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("RequestTaskRun stale parent err = %v, want ErrNotOwner", err)
	}
	children, err := st.TaskRuns().List(ctx, "TEST", store.TaskRunFilter{DriverRunID: run.RunID})
	if err != nil {
		t.Fatalf("List children: %v", err)
	}
	if len(children) != 0 {
		t.Fatalf("children = %+v, want none for stale parent owner", children)
	}
}

func setupRunningDriverRun(t *testing.T) (context.Context, store.Store, *domain.DriverRun) {
	t.Helper()
	ctx, st, run := setupQueuedDriverRun(t)
	claimed, err := st.DriverRuns().Claim(ctx, "TEST", run.RunID, "node-1", "lease-1")
	if err != nil {
		t.Fatalf("Claim driver run: %v", err)
	}
	return ctx, st, claimed
}

func setupQueuedDriverRun(t *testing.T) (context.Context, store.Store, *domain.DriverRun) {
	t.Helper()
	ctx := context.Background()
	root := t.TempDir()
	st := memstore.New()
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	source := writeWorkflow(t, root, "complete-epic.ts", `export async function run(ctx) {
  return ctx.run.complete({ summary: "done" });
}
`)
	published, err := PublishWorkflow(ctx, st, PublishOptions{WorkspaceKey: "TEST", WorkDir: root, SourcePath: source, CreatedBy: "tester", FlueCommand: fakeFlueCommand(t)})
	if err != nil {
		t.Fatalf("PublishWorkflow: %v", err)
	}
	run, err := CreateDriverRun(ctx, st, RunOptions{
		WorkspaceKey: "TEST",
		DriverID:     published.Driver.DriverID,
		EpicID:       "TEST-EPIC",
		RunID:        "run-1",
	})
	if err != nil {
		t.Fatalf("CreateDriverRun: %v", err)
	}
	return ctx, st, run
}

type recordingTaskExecutor struct {
	req    TaskExecRequest
	result TaskExecResult
	err    error
}

func (e *recordingTaskExecutor) ExecuteTask(_ context.Context, req TaskExecRequest) (TaskExecResult, error) {
	e.req = req
	return e.result, e.err
}

type waitingTaskHeartbeatExecutor struct {
	store        store.Store
	sawHeartbeat bool
}

func (e *waitingTaskHeartbeatExecutor) ExecuteTask(ctx context.Context, req TaskExecRequest) (TaskExecResult, error) {
	deadline, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-deadline.Done():
			return TaskExecResult{}, deadline.Err()
		case <-ticker.C:
			run, err := e.store.TaskRuns().Get(deadline, req.WorkspaceKey, req.TaskRunID)
			if err != nil {
				return TaskExecResult{}, err
			}
			if run.RuntimeMetadata["heartbeat_source"] == "driver_task_request" {
				e.sawHeartbeat = true
				return TaskExecResult{
					ExitCode:        0,
					LogsRef:         "logs://task-run-heartbeat",
					RuntimeMetadata: map[string]string{"observed_heartbeat": "true"},
				}, nil
			}
		}
	}
}
