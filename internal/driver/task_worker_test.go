//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestTaskWorkerDefaultBridgeUsesServeSessionRegistry(t *testing.T) {
	ctx, st, run := setupRunningDriverRun(t)
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "TEST", TaskRunID: "task-run-worker-registry", DriverRunID: run.RunID,
		TaskID: "TEST-REGISTRY", ProviderProfile: "flue-local", Status: domain.TaskRunQueued,
	}); err != nil {
		t.Fatalf("Create queued task run: %v", err)
	}
	commandJSON, err := json.Marshal([]string{"sh", "-c", `printf '%s\n' '{"status":"completed","exit_code":0}'`})
	if err != nil {
		t.Fatalf("Marshal helper command: %v", err)
	}
	t.Setenv(TaskRunnerCommandJSONEnv, string(commandJSON))
	fixedNow := time.Unix(1_700_000_000, 0).UTC()
	registry := NewTaskRunSessionOpenRegistry()
	runContext := store.SessionRunContext{
		WorkspaceKey: "TEST", TaskRunID: "task-run-worker-registry", Attempt: 1,
		FencingToken: fixedNow.UnixNano(),
	}
	registry.Record(runContext, store.SessionRef{WorkspaceKey: "TEST", SessionID: "registry-only", Attempt: 1})
	outcome, err := (&TaskWorker{
		Store: st, WorkspaceKey: "TEST", WorkDir: t.TempDir(), NodeID: "task-worker-node-registry",
		RunnerID: "task-worker-runner-registry", SupportedProviders: []string{"flue-local"},
		HeartbeatInterval: -1, MaxAttempts: 1, SessionOpenRegistry: registry, Now: func() time.Time { return fixedNow },
	}).RunOnce(ctx)
	if err != nil || outcome.Run.Status != domain.TaskRunCompleted || outcome.Run.RuntimeMetadata["unclosed_sessions"] != "0" {
		t.Fatalf("RunOnce run = %+v, err=%v", outcome.Run, err)
	}
	if live := registry.Live(runContext); len(live) != 0 {
		t.Fatalf("serve registry not consumed by default worker bridge: %+v", live)
	}
}

func TestTaskWorkerRunOnceClaimsQueuedTaskRunAndClosesTask(t *testing.T) {
	ctx, st, run := setupRunningDriverRun(t)
	if _, err := st.DriverSteps().Create(ctx, store.DriverStepCreate{
		WorkspaceKey: "TEST",
		StepID:       "step-worker-loop",
		DriverRunID:  run.RunID,
		StepKind:     "task_run",
		Status:       domain.DriverStepQueued,
		NodeID:       run.NodeID,
		LeaseID:      run.LeaseID,
		FencingToken: run.FencingToken,
	}); err != nil {
		t.Fatalf("Create driver step: %v", err)
	}
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey:    "TEST",
		TaskRunID:       "task-run-worker-loop",
		DriverRunID:     run.RunID,
		DriverStepID:    "step-worker-loop",
		TaskID:          "TEST-11",
		ProviderProfile: "flue-local",
		Status:          domain.TaskRunQueued,
		SandboxPlacement: domain.TaskRunPlacement{
			Provider: "flue-local",
		},
		RuntimeMetadata: map[string]string{
			"parent_session_id": "lead-session-1",
		},
	}); err != nil {
		t.Fatalf("Create queued task run: %v", err)
	}
	executor := &recordingTaskExecutor{result: TaskExecResult{
		Status:       domain.TaskRunCompleted,
		ExitCode:     0,
		LogsRef:      "logs://task-run-worker-loop",
		ArtifactsRef: "artifacts://task-run-worker-loop",
	}}

	outcome, err := (&TaskWorker{
		Store:              st,
		WorkspaceKey:       "TEST",
		NodeID:             "task-worker-node-1",
		RunnerID:           "task-worker-runner-1",
		SupportedProviders: []string{"flue-local"},
		HeartbeatInterval:  -1,
		Executor:           executor,
	}).RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if outcome.Run.TaskRunID != "task-run-worker-loop" || outcome.Run.Status != domain.TaskRunCompleted {
		t.Fatalf("outcome run = %+v, want completed task-run-worker-loop", outcome.Run)
	}
	if executor.req.NodeID != "task-worker-node-1" || executor.req.LeaseToken == "" {
		t.Fatalf("executor req owner = node:%q token:%q, want worker owner with generated token", executor.req.NodeID, executor.req.LeaseToken)
	}
	if executor.req.ParentSessionID != "lead-session-1" || outcome.Run.RuntimeMetadata["parent_session_id"] != "lead-session-1" {
		t.Fatalf("parent session propagation req=%q metadata=%q, want lead-session-1", executor.req.ParentSessionID, outcome.Run.RuntimeMetadata["parent_session_id"])
	}
	replayed, err := st.TaskRuns().Complete(ctx, "TEST", "task-run-worker-loop", store.TaskRunComplete{
		CompletionID: "worker-complete-task-run-worker-loop",
		Status:       domain.TaskRunCompleted,
	})
	if err != nil {
		t.Fatalf("replay worker completion: %v", err)
	}
	if replayed.TaskRunID != "task-run-worker-loop" {
		t.Fatalf("replayed completion = %+v", replayed)
	}
	step, err := st.DriverSteps().Get(ctx, "TEST", "step-worker-loop")
	if err != nil {
		t.Fatalf("Get driver step: %v", err)
	}
	if step.Status != domain.DriverStepCompleted || step.TaskRunID != "task-run-worker-loop" || step.OutputRef != "artifacts://task-run-worker-loop" {
		t.Fatalf("driver step = %+v, want completed linked step with task output", step)
	}
}

func TestTaskWorkerRunOnceMapsFlueSessionUnderParent(t *testing.T) {
	ctx, st, run := setupRunningDriverRun(t)
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey:     "TEST",
		TaskRunID:        "task-run-worker-flue",
		DriverRunID:      run.RunID,
		TaskID:           "TEST-12",
		Runner:           "local-task-runner",
		RunnerKind:       RunnerKindFlueWorkflow,
		RunnerEntrypoint: "local-task-runner",
		Status:           domain.TaskRunQueued,
		RuntimeMetadata: map[string]string{
			"parent_session_id":  "lead-session-1",
			"runner_trust_level": string(domain.DriverTrustTrusted),
		},
	}); err != nil {
		t.Fatalf("Create queued task run: %v", err)
	}
	executor := HostBridgeTaskExecutor{
		Store:        st,
		WorktreePath: t.TempDir(),
		Command:      hostBridgeHelperCommand(t, "flue-transcript", "unused-base", "unused-patch"),
	}

	outcome, err := (&TaskWorker{
		Store:             st,
		WorkspaceKey:      "TEST",
		NodeID:            "task-worker-node-1",
		RunnerID:          "task-worker-runner-1",
		HeartbeatInterval: -1,
		MaxAttempts:       1,
		Executor:          executor,
	}).RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if outcome.Run.Status != domain.TaskRunCompleted {
		t.Fatalf("outcome status = %s error=%s, want completed", outcome.Run.Status, outcome.Run.ErrorMessage)
	}
	session, err := st.AgentSessions().Get(ctx, "TEST", "flue-task-run-worker-flue")
	if err != nil {
		t.Fatalf("get flue agent session: %v", err)
	}
	if session.Kind != domain.AgentSessionKindTask || session.TaskID != "TEST-12" || session.ParentSessionID != "lead-session-1" {
		t.Fatalf("session = %+v, want task session under lead-session-1", session)
	}
	if session.Metadata["runtime"] != "flue" || session.Metadata["task_run_id"] != "task-run-worker-flue" || session.Metadata["transcript_ref"] == "" {
		t.Fatalf("session metadata = %+v, want flue transcript metadata", session.Metadata)
	}
}

func TestTaskWorkerRunOnceRefusesUntrustedQueuedNamedRunner(t *testing.T) {
	ctx, st, run := setupRunningDriverRun(t)
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey:     "TEST",
		TaskRunID:        "task-run-worker-untrusted",
		DriverRunID:      run.RunID,
		TaskID:           "TEST-14",
		Runner:           "local-task-runner",
		RunnerKind:       RunnerKindFlueWorkflow,
		RunnerEntrypoint: "local-task-runner",
		Status:           domain.TaskRunQueued,
		RuntimeMetadata: map[string]string{
			"runner_trust_level": string(domain.DriverTrustUntrusted),
		},
	}); err != nil {
		t.Fatalf("Create queued task run: %v", err)
	}
	ranPath := filepath.Join(t.TempDir(), "ran")
	executor := HostBridgeTaskExecutor{
		Store:        st,
		WorktreePath: t.TempDir(),
		Command:      []string{"sh", "-c", "printf ran > \"$1\"; printf '%s\n' '{\"status\":\"completed\",\"exit_code\":0}'", "sh", ranPath},
	}

	outcome, err := (&TaskWorker{
		Store:             st,
		WorkspaceKey:      "TEST",
		NodeID:            "task-worker-node-1",
		RunnerID:          "task-worker-runner-1",
		HeartbeatInterval: -1,
		MaxAttempts:       1,
		Executor:          executor,
	}).RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if outcome.Run.Status != domain.TaskRunFailed || outcome.Run.ErrorClass != ErrorClassSandboxRequired {
		t.Fatalf("outcome = %+v, want failed %s", outcome.Run, ErrorClassSandboxRequired)
	}
	if outcome.Run.RuntimeMetadata[ErrorCodeOutputKey] != ErrorClassSandboxRequired ||
		outcome.Run.RuntimeMetadata[SandboxLauncherOutputKey] != SandboxProviderProcess ||
		outcome.Run.RuntimeMetadata["runner_trust_level"] != string(domain.DriverTrustUntrusted) {
		t.Fatalf("runtime metadata = %+v, want sandbox refusal persisted", outcome.Run.RuntimeMetadata)
	}
	if _, err := os.Stat(ranPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("host bridge command ran despite untrusted queued runner refusal; stat err=%v", err)
	}
}

func TestTaskWorkerRunOnceRetriesThenBlocksFailedTaskRun(t *testing.T) {
	ctx, st, run := setupRunningDriverRun(t)
	if _, err := st.DriverSteps().Create(ctx, store.DriverStepCreate{
		WorkspaceKey: "TEST",
		StepID:       "step-worker-retry",
		DriverRunID:  run.RunID,
		StepKind:     "task_run",
		Status:       domain.DriverStepQueued,
		NodeID:       run.NodeID,
		LeaseID:      run.LeaseID,
		FencingToken: run.FencingToken,
	}); err != nil {
		t.Fatalf("Create driver step: %v", err)
	}
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey:    "TEST",
		TaskRunID:       "task-run-worker-retry",
		DriverRunID:     run.RunID,
		DriverStepID:    "step-worker-retry",
		TaskID:          "TEST-13",
		ProviderProfile: "flue-local",
		Status:          domain.TaskRunQueued,
		SandboxPlacement: domain.TaskRunPlacement{
			Provider: "flue-local",
		},
	}); err != nil {
		t.Fatalf("Create queued task run: %v", err)
	}
	executor := &recordingTaskExecutor{result: TaskExecResult{
		Status:       domain.TaskRunFailed,
		ExitCode:     1,
		LogsRef:      "logs://task-run-worker-retry",
		ErrorClass:   "task_runner_error",
		ErrorMessage: "boom",
	}}
	now := time.Now().UTC()
	worker := &TaskWorker{
		Store:              st,
		WorkspaceKey:       "TEST",
		NodeID:             "task-worker-node-1",
		RunnerID:           "task-worker-runner-1",
		SupportedProviders: []string{"flue-local"},
		HeartbeatInterval:  -1,
		MaxAttempts:        2,
		Executor:           executor,
		Now:                func() time.Time { return now },
	}

	first, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce first: %v", err)
	}
	if first.Run.Status != domain.TaskRunQueued || first.Run.NodeID != "" || first.Run.LeaseID != "" || first.Run.FencingToken != 0 {
		t.Fatalf("first outcome = %+v, want requeued unowned task run", first.Run)
	}
	if first.Run.RuntimeMetadata["scheduler_state"] != "retrying" || first.Run.RuntimeMetadata["scheduler_attempt"] != "1" || first.Run.RuntimeMetadata["scheduler_max_attempts"] != "2" {
		t.Fatalf("first metadata = %+v, want retry scheduler metadata", first.Run.RuntimeMetadata)
	}
	step, err := st.DriverSteps().Get(ctx, "TEST", "step-worker-retry")
	if err != nil {
		t.Fatalf("Get step after retry: %v", err)
	}
	if step.Status != domain.DriverStepQueued || step.TaskRunID != "task-run-worker-retry" {
		t.Fatalf("step after retry = %+v, want queued linked step", step)
	}
	requeued, err := st.TaskRuns().Get(ctx, "TEST", "task-run-worker-retry")
	if err != nil {
		t.Fatalf("Get requeued task run: %v", err)
	}
	if requeued.NextEligibleAt.IsZero() || !requeued.NextEligibleAt.After(now) {
		t.Fatalf("requeued NextEligibleAt = %v, want future retry backoff after %v", requeued.NextEligibleAt, now)
	}

	// Advance the clock past the retry backoff so the requeued run is
	// claimable again.
	now = now.Add(taskRunRetryBackoff(1))

	second, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce second: %v", err)
	}
	if second.Run.Status != domain.TaskRunFailed || second.Run.NodeID != "task-worker-node-1" {
		t.Fatalf("second outcome = %+v, want blocked failed owned terminal task run", second.Run)
	}
	if second.Run.RuntimeMetadata["scheduler_state"] != "blocked" || second.Run.RuntimeMetadata["scheduler_attempt"] != "2" || second.Run.RuntimeMetadata["scheduler_max_attempts"] != "2" {
		t.Fatalf("second metadata = %+v, want blocked scheduler metadata", second.Run.RuntimeMetadata)
	}
	step, err = st.DriverSteps().Get(ctx, "TEST", "step-worker-retry")
	if err != nil {
		t.Fatalf("Get step after blocked: %v", err)
	}
	if step.Status != domain.DriverStepFailed || step.TaskRunID != "task-run-worker-retry" || step.OutputRef != "logs://task-run-worker-retry" {
		t.Fatalf("step after blocked = %+v, want failed linked step with logs output", step)
	}
}

func TestTaskWorkerRunOnceReturnsNoQueuedTaskRun(t *testing.T) {
	ctx, st, _ := setupRunningDriverRun(t)
	_, err := (&TaskWorker{
		Store:             st,
		WorkspaceKey:      "TEST",
		NodeID:            "task-worker-node-1",
		HeartbeatInterval: -1,
		Executor:          &recordingTaskExecutor{},
	}).RunOnce(ctx)
	if !errors.Is(err, ErrNoQueuedTaskRun) {
		t.Fatalf("RunOnce err = %v, want ErrNoQueuedTaskRun", err)
	}
}
