//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	artifactsmodule "github.com/tysonthomas9/loomcli/internal/modules/artifacts"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type countingTaskWorkerLifecycleAPI struct {
	execution.TaskRunWorkerAPI
	mu            sync.Mutex
	registrations int
	heartbeats    int
	activations   int
	claims        int
	heartbeatErr  error
	capacities    []int
}

func (api *countingTaskWorkerLifecycleAPI) RegisterWorkerNode(
	ctx context.Context,
	auth authority.SystemAuthority,
	command execution.RegisterWorkerNodeCommand,
) (*execution.WorkerNode, error) {
	api.mu.Lock()
	api.registrations++
	api.capacities = append(api.capacities, command.Capacity)
	api.mu.Unlock()
	return api.TaskRunWorkerAPI.RegisterWorkerNode(ctx, auth, command)
}

func (api *countingTaskWorkerLifecycleAPI) HeartbeatWorkerNode(
	ctx context.Context,
	auth authority.SystemAuthority,
	command execution.HeartbeatWorkerNodeCommand,
) (*execution.WorkerNode, error) {
	api.mu.Lock()
	api.heartbeats++
	err := api.heartbeatErr
	api.heartbeatErr = nil
	api.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return api.TaskRunWorkerAPI.HeartbeatWorkerNode(ctx, auth, command)
}

func (api *countingTaskWorkerLifecycleAPI) SetWorkerNodeDrain(
	ctx context.Context,
	auth authority.SystemAuthority,
	command execution.SetWorkerNodeDrainCommand,
) (*execution.WorkerNode, error) {
	api.mu.Lock()
	api.activations++
	api.mu.Unlock()
	return api.TaskRunWorkerAPI.SetWorkerNodeDrain(ctx, auth, command)
}

func (api *countingTaskWorkerLifecycleAPI) ClaimTaskRun(
	ctx context.Context,
	auth authority.SystemAuthority,
	command execution.ClaimTaskRunCommand,
) (execution.ClaimTaskRunResult, error) {
	api.mu.Lock()
	api.claims++
	api.mu.Unlock()
	return api.TaskRunWorkerAPI.ClaimTaskRun(ctx, auth, command)
}

func (api *countingTaskWorkerLifecycleAPI) counts() (registrations, heartbeats, activations, claims int) {
	api.mu.Lock()
	defer api.mu.Unlock()
	return api.registrations, api.heartbeats, api.activations, api.claims
}

func (api *countingTaskWorkerLifecycleAPI) failNextHeartbeat(err error) {
	api.mu.Lock()
	defer api.mu.Unlock()
	api.heartbeatErr = err
}

func (api *countingTaskWorkerLifecycleAPI) registeredCapacities() []int {
	api.mu.Lock()
	defer api.mu.Unlock()
	return append([]int(nil), api.capacities...)
}

type blockingTaskWorkerLifecycleAPI struct {
	execution.TaskRunWorkerAPI
	workspace string
	entered   chan struct{}
	release   chan struct{}
	once      sync.Once
}

func (api *blockingTaskWorkerLifecycleAPI) RegisterWorkerNode(
	ctx context.Context,
	auth authority.SystemAuthority,
	command execution.RegisterWorkerNodeCommand,
) (*execution.WorkerNode, error) {
	if command.WorkspaceKey == api.workspace {
		api.once.Do(func() { close(api.entered) })
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-api.release:
		}
	}
	return api.TaskRunWorkerAPI.RegisterWorkerNode(ctx, auth, command)
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

	worker := &TaskWorker{
		Store:              st,
		WorkspaceKey:       "TEST",
		NodeID:             "task-worker-node-1",
		RunnerID:           "task-worker-runner-1",
		SupportedProviders: []string{"flue-local"},
		HeartbeatInterval:  -1,
		Executor:           executor,
	}
	wireTaskWorkerTestExecution(worker, st)
	outcome, err := worker.RunOnce(ctx)
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
		NodeID:       outcome.Run.NodeID,
		LeaseID:      outcome.Run.LeaseID,
		LeaseToken:   executor.req.LeaseToken,
		FencingToken: outcome.Run.FencingToken,
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

func TestTaskWorkerUnscopedIdleLifecycleIsBoundedAcrossConcurrencySlots(t *testing.T) {
	ctx, st, run := setupRunningDriverRun(t)
	for _, workspace := range []string{"WS2", "WS3", "WS4", "WS5"} {
		if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: workspace, Name: workspace}); err != nil {
			t.Fatalf("Create workspace %s: %v", workspace, err)
		}
	}

	now := time.Now().UTC()
	executor := &recordingTaskExecutor{result: TaskExecResult{Status: domain.TaskRunCompleted, ExitCode: 0}}
	template := TaskWorker{
		Store: st, WorkspaceKey: "", NodeID: "task-worker-node-shared",
		NodeCapacity:       2,
		SupportedProviders: []string{"flue-local"}, HeartbeatInterval: -1,
		Executor: executor, Now: func() time.Time { return now },
	}
	wireTaskWorkerTestExecution(&template, st)
	counted := &countingTaskWorkerLifecycleAPI{TaskRunWorkerAPI: template.Execution}
	template.Execution = counted
	workers := []TaskWorker{template.CloneForRuntime(), template.CloneForRuntime()}
	workers[0].ExecutionComponentID = "execution-task-run-worker-1"
	workers[1].ExecutionComponentID = "execution-task-run-worker-2"

	for pass := 0; pass < 3; pass++ {
		if pass > 0 {
			for index := range workers {
				if _, err := workers[index].RunOnce(ctx); !errors.Is(err, ErrNoQueuedTaskRun) {
					t.Fatalf("idle pass %d worker %d error = %v, want ErrNoQueuedTaskRun", pass+1, index+1, err)
				}
			}
			continue
		}
		errs := make(chan error, len(workers))
		var wait sync.WaitGroup
		for index := range workers {
			wait.Add(1)
			go func(worker *TaskWorker) {
				defer wait.Done()
				_, err := worker.RunOnce(ctx)
				errs <- err
			}(&workers[index])
		}
		wait.Wait()
		close(errs)
		for err := range errs {
			if !errors.Is(err, ErrNoQueuedTaskRun) {
				t.Fatalf("idle pass %d error = %v, want ErrNoQueuedTaskRun", pass+1, err)
			}
		}
	}
	registrations, heartbeats, activations, claims := counted.counts()
	if registrations != 5 || heartbeats != 5 || activations != 5 {
		t.Fatalf("idle node lifecycle calls = register:%d heartbeat:%d activate:%d, want one each for 5 workspaces", registrations, heartbeats, activations)
	}
	for index, capacity := range counted.registeredCapacities() {
		if capacity != 2 {
			t.Fatalf("registration %d capacity = %d, want 2 shared concurrency slots", index+1, capacity)
		}
	}
	if claims < 25 || claims > 30 {
		t.Fatalf("idle claim calls = %d, want 25..30 while same-key lifecycle passes singleflight", claims)
	}
	claimsBeforeHeartbeat := claims
	now = now.Add(30 * time.Second)
	if _, err := workers[0].RunOnce(ctx); !errors.Is(err, ErrNoQueuedTaskRun) {
		t.Fatalf("heartbeat cadence pass error = %v, want ErrNoQueuedTaskRun", err)
	}
	registrations, heartbeats, activations, claims = counted.counts()
	if registrations != 5 || heartbeats != 10 || activations != 5 {
		t.Fatalf("heartbeat cadence calls = register:%d heartbeat:%d activate:%d, want only 5 due heartbeats", registrations, heartbeats, activations)
	}
	if claims != claimsBeforeHeartbeat+5 {
		t.Fatalf("heartbeat cadence claim calls = %d, want one additional 5-workspace pass after %d", claims, claimsBeforeHeartbeat)
	}

	if _, err := st.DriverSteps().Create(ctx, store.DriverStepCreate{
		WorkspaceKey: "TEST", StepID: "step-worker-after-idle", DriverRunID: run.RunID,
		StepKind: "task_run", Status: domain.DriverStepQueued,
		NodeID: run.NodeID, LeaseID: run.LeaseID, FencingToken: run.FencingToken,
	}); err != nil {
		t.Fatalf("Create driver step after idle: %v", err)
	}
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "TEST", TaskRunID: "task-run-after-idle", DriverRunID: run.RunID,
		DriverStepID: "step-worker-after-idle", TaskID: "TEST-AFTER-IDLE",
		ProviderProfile: "flue-local", Status: domain.TaskRunQueued,
		SandboxPlacement: domain.TaskRunPlacement{Provider: "flue-local"},
	}); err != nil {
		t.Fatalf("Create queued task run after idle: %v", err)
	}
	claimsBeforeWork := claims
	outcome, err := workers[0].RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce after work arrived: %v", err)
	}
	if outcome == nil || outcome.Run == nil || outcome.Run.TaskRunID != "task-run-after-idle" || outcome.Run.Status != domain.TaskRunCompleted {
		t.Fatalf("outcome after idle = %+v, want completed task-run-after-idle", outcome)
	}
	registrations, heartbeats, activations, claims = counted.counts()
	if registrations != 5 || heartbeats != 10 || activations != 5 {
		t.Fatalf("work claim repeated node lifecycle calls = register:%d heartbeat:%d activate:%d", registrations, heartbeats, activations)
	}
	if claims <= claimsBeforeWork || claims > claimsBeforeWork+5 {
		t.Fatalf("work claim calls advanced from %d to %d, want claim within one all-workspace pass", claimsBeforeWork, claims)
	}
}

func TestTaskWorkerNodeCapacityDefaultsToOne(t *testing.T) {
	ctx, st, _ := setupRunningDriverRun(t)
	worker := TaskWorker{
		Store: st, WorkspaceKey: "TEST", NodeID: "task-worker-node-default-capacity",
		HeartbeatInterval: -1, Executor: &recordingTaskExecutor{},
	}
	wireTaskWorkerTestExecution(&worker, st)
	counted := &countingTaskWorkerLifecycleAPI{TaskRunWorkerAPI: worker.Execution}
	worker.Execution = counted

	if _, err := worker.RunOnce(ctx); !errors.Is(err, ErrNoQueuedTaskRun) {
		t.Fatalf("RunOnce error = %v, want ErrNoQueuedTaskRun", err)
	}
	capacities := counted.registeredCapacities()
	if len(capacities) != 1 || capacities[0] != 1 {
		t.Fatalf("registered capacities = %v, want [1]", capacities)
	}
}

func TestTaskWorkerExpiredNodeNotFoundReRegistersWithRunHeartbeatsDisabled(t *testing.T) {
	ctx, st, _ := setupRunningDriverRun(t)
	now := time.Now().UTC()
	worker := TaskWorker{
		Store: st, WorkspaceKey: "TEST", NodeID: "task-worker-node-expiry",
		HeartbeatInterval: -1, Executor: &recordingTaskExecutor{}, Now: func() time.Time { return now },
	}
	wireTaskWorkerTestExecution(&worker, st)
	counted := &countingTaskWorkerLifecycleAPI{TaskRunWorkerAPI: worker.Execution}
	worker.Execution = counted

	if _, err := worker.RunOnce(ctx); !errors.Is(err, ErrNoQueuedTaskRun) {
		t.Fatalf("initial RunOnce error = %v, want ErrNoQueuedTaskRun", err)
	}
	now = now.Add(30 * time.Second)
	counted.failNextHeartbeat(execution.ErrNotFound)
	if _, err := worker.RunOnce(ctx); !errors.Is(err, execution.ErrNotFound) {
		t.Fatalf("expired node heartbeat error = %v, want Execution not found", err)
	}
	if _, err := worker.RunOnce(ctx); !errors.Is(err, ErrNoQueuedTaskRun) {
		t.Fatalf("RunOnce after expired node recovery = %v, want ErrNoQueuedTaskRun", err)
	}

	registrations, heartbeats, activations, claims := counted.counts()
	if registrations != 2 || heartbeats != 3 || activations != 2 || claims != 2 {
		t.Fatalf("expired node recovery calls = register:%d heartbeat:%d activate:%d claim:%d, want 2/3/2/2", registrations, heartbeats, activations, claims)
	}
}

func TestTaskWorkerBlockedLifecycleWorkspaceDoesNotBlockAnotherClone(t *testing.T) {
	ctx, st, run := setupRunningDriverRun(t)
	if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "BLOCKED", Name: "blocked"}); err != nil {
		t.Fatalf("Create blocked workspace: %v", err)
	}
	if _, err := st.DriverSteps().Create(ctx, store.DriverStepCreate{
		WorkspaceKey: "TEST", StepID: "step-worker-unblocked", DriverRunID: run.RunID,
		StepKind: "task_run", Status: domain.DriverStepQueued,
		NodeID: run.NodeID, LeaseID: run.LeaseID, FencingToken: run.FencingToken,
	}); err != nil {
		t.Fatalf("Create driver step: %v", err)
	}
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "TEST", TaskRunID: "task-run-unblocked", DriverRunID: run.RunID,
		DriverStepID: "step-worker-unblocked", TaskID: "TEST-UNBLOCKED",
		ProviderProfile: "flue-local", Status: domain.TaskRunQueued,
		SandboxPlacement: domain.TaskRunPlacement{Provider: "flue-local"},
	}); err != nil {
		t.Fatalf("Create queued task run: %v", err)
	}

	executor := &recordingTaskExecutor{result: TaskExecResult{Status: domain.TaskRunCompleted, ExitCode: 0}}
	template := TaskWorker{
		Store: st, NodeID: "task-worker-node-shared", SupportedProviders: []string{"flue-local"},
		HeartbeatInterval: 30 * time.Second, Executor: executor,
	}
	wireTaskWorkerTestExecution(&template, st)
	blockedAPI := &blockingTaskWorkerLifecycleAPI{
		TaskRunWorkerAPI: template.Execution, workspace: "BLOCKED",
		entered: make(chan struct{}), release: make(chan struct{}),
	}
	template.Execution = blockedAPI
	blockedWorker := template.CloneForRuntime()
	blockedWorker.WorkspaceKey = ""
	blockedWorker.ExecutionComponentID = "execution-task-run-worker-1"
	freeWorker := template.CloneForRuntime()
	freeWorker.WorkspaceKey = ""
	freeWorker.ExecutionComponentID = "execution-task-run-worker-2"

	var releaseOnce sync.Once
	releaseBlocked := func() { releaseOnce.Do(func() { close(blockedAPI.release) }) }
	defer releaseBlocked()
	blockedDone := make(chan error, 1)
	go func() {
		_, err := blockedWorker.RunOnce(ctx)
		blockedDone <- err
	}()
	select {
	case <-blockedAPI.entered:
	case <-time.After(time.Second):
		t.Fatal("blocked workspace never entered node registration")
	}

	type workerResult struct {
		outcome *TaskRunRequestOutcome
		err     error
	}
	freeDone := make(chan workerResult, 1)
	go func() {
		outcome, err := freeWorker.RunOnce(ctx)
		freeDone <- workerResult{outcome: outcome, err: err}
	}()
	select {
	case result := <-freeDone:
		if result.err != nil {
			t.Fatalf("unrelated workspace RunOnce: %v", result.err)
		}
		if result.outcome == nil || result.outcome.Run == nil || result.outcome.Run.TaskRunID != "task-run-unblocked" || result.outcome.Run.Status != domain.TaskRunCompleted {
			t.Fatalf("unrelated workspace outcome = %+v, want completed task-run-unblocked", result.outcome)
		}
	case <-time.After(time.Second):
		t.Fatal("unrelated workspace claim was blocked by another workspace's lifecycle I/O")
	}

	releaseBlocked()
	if err := <-blockedDone; !errors.Is(err, ErrNoQueuedTaskRun) {
		t.Fatalf("blocked workspace result after release = %v, want ErrNoQueuedTaskRun", err)
	}
}

func TestTaskWorkerRunOnceReplaysExactClaimEnvelopeAfterLostResponse(t *testing.T) {
	ctx, st, run := setupRunningDriverRun(t)
	if _, err := st.DriverSteps().Create(ctx, store.DriverStepCreate{
		WorkspaceKey: "TEST", StepID: "step-worker-lost-response", DriverRunID: run.RunID,
		StepKind: "task_run", Status: domain.DriverStepQueued,
		NodeID: run.NodeID, LeaseID: run.LeaseID, FencingToken: run.FencingToken,
	}); err != nil {
		t.Fatalf("Create driver step: %v", err)
	}
	if _, err := st.TaskRuns().Create(ctx, store.TaskRunCreate{
		WorkspaceKey: "TEST", TaskRunID: "task-run-worker-lost-response", DriverRunID: run.RunID,
		DriverStepID: "step-worker-lost-response", TaskID: "TEST-LOST-RESPONSE",
		ProviderProfile: "flue-local", Status: domain.TaskRunQueued,
		SandboxPlacement: domain.TaskRunPlacement{Provider: "flue-local"},
	}); err != nil {
		t.Fatalf("Create queued task run: %v", err)
	}
	executor := &recordingTaskExecutor{result: TaskExecResult{Status: domain.TaskRunCompleted, ExitCode: 0}}
	now := time.Now().UTC()
	worker := &TaskWorker{
		Store: st, WorkspaceKey: "TEST", NodeID: "task-worker-node-1", RunnerID: "task-worker-runner-1",
		SupportedProviders: []string{"flue-local"}, Capabilities: []string{"repo"},
		WorkerProfileIDs: []string{"profile-1"}, HeartbeatInterval: -1, Executor: executor,
		Now: func() time.Time { return now },
	}
	wireTaskWorkerTestExecution(worker, st)
	lost := &lostResponseTaskWorkerExecution{TaskRunWorkerAPI: worker.Execution}
	worker.Execution = lost

	if _, err := worker.RunOnce(ctx); !errors.Is(err, execution.ErrUnavailable) {
		t.Fatalf("first RunOnce error = %v, want ambiguous unavailable", err)
	}
	if executor.req.TaskRunID != "" {
		t.Fatalf("executor ran before claim receipt replay: %+v", executor.req)
	}
	now = now.Add(time.Minute)
	outcome, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatalf("replayed RunOnce: %v", err)
	}
	if len(lost.commands) != 2 || !lost.sameEnvelope {
		t.Fatalf("claim envelope was not replayed exactly; calls=%d same=%v", len(lost.commands), lost.sameEnvelope)
	}
	if outcome.Run.TaskRunID != "task-run-worker-lost-response" || outcome.Run.Status != domain.TaskRunCompleted ||
		executor.req.LeaseToken == "" || executor.req.LeaseToken != lost.commands[0].LeaseToken {
		t.Fatalf("replayed outcome=%+v executor task=%q", outcome.Run, executor.req.TaskRunID)
	}
}

func TestTaskWorkerRunOnceSerializesClaimReceiptReplay(t *testing.T) {
	_, st, _ := setupRunningDriverRun(t)
	worker := &TaskWorker{
		Store: st, WorkspaceKey: "TEST", NodeID: "task-worker-node-1", HeartbeatInterval: -1,
		Executor: &recordingTaskExecutor{},
	}
	wireTaskWorkerTestExecution(worker, st)
	claims := &concurrentClaimTaskWorkerExecution{
		TaskRunWorkerAPI: worker.Execution, entered: make(chan struct{}, 2), release: make(chan struct{}),
	}
	worker.Execution = claims

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	run := func() {
		defer wg.Done()
		_, err := worker.RunOnce(context.Background())
		errs <- err
	}
	wg.Add(1)
	go run()
	<-claims.entered
	wg.Add(1)
	go run()
	select {
	case <-claims.entered:
		t.Fatal("second RunOnce entered ClaimTaskRun before the first pass completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(claims.release)
	wg.Wait()
	close(errs)
	for err := range errs {
		if !errors.Is(err, execution.ErrUnavailable) {
			t.Fatalf("RunOnce error = %v, want unavailable", err)
		}
	}
	if claims.maxActive() != 1 {
		t.Fatalf("max concurrent ClaimTaskRun calls = %d, want 1", claims.maxActive())
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
		APIBaseURL:   testTaskRunAPIURL,
		Command:      hostBridgeHelperCommand(t, "flue-transcript", "unused-base", "unused-patch"),
	}

	worker := &TaskWorker{
		Store:             st,
		WorkspaceKey:      "TEST",
		NodeID:            "task-worker-node-1",
		RunnerID:          "task-worker-runner-1",
		HeartbeatInterval: -1,
		MaxAttempts:       1,
		Executor:          executor,
	}
	wireTaskWorkerTestExecution(worker, st)
	outcome, err := worker.RunOnce(ctx)
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
		APIBaseURL:   testTaskRunAPIURL,
		Command:      []string{"sh", "-c", "printf ran > \"$1\"; printf '%s\n' '{\"status\":\"completed\",\"exit_code\":0}'", "sh", ranPath},
	}

	worker := &TaskWorker{
		Store:             st,
		WorkspaceKey:      "TEST",
		NodeID:            "task-worker-node-1",
		RunnerID:          "task-worker-runner-1",
		HeartbeatInterval: -1,
		MaxAttempts:       1,
		Executor:          executor,
	}
	wireTaskWorkerTestExecution(worker, st)
	outcome, err := worker.RunOnce(ctx)
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
	wireTaskWorkerTestExecution(worker, st)

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
	worker := &TaskWorker{
		Store:             st,
		WorkspaceKey:      "TEST",
		NodeID:            "task-worker-node-1",
		HeartbeatInterval: -1,
		Executor:          &recordingTaskExecutor{},
	}
	wireTaskWorkerTestExecution(worker, st)
	_, err := worker.RunOnce(ctx)
	if !errors.Is(err, ErrNoQueuedTaskRun) {
		t.Fatalf("RunOnce err = %v, want ErrNoQueuedTaskRun", err)
	}
}

func TestTaskWorkerRunOnceFailsClosedWithoutConvergence(t *testing.T) {
	ctx, st, _ := setupRunningDriverRun(t)
	worker := &TaskWorker{Store: st, WorkspaceKey: "TEST", NodeID: "task-worker-node-1", HeartbeatInterval: -1}
	wireTaskWorkerTestExecution(worker, st)
	worker.Convergence = nil
	if _, err := worker.RunOnce(ctx); !errors.Is(err, execution.ErrUnavailable) {
		t.Fatalf("RunOnce error = %v, want Execution unavailable", err)
	}
}

func TestTaskWorkerRunOnceFailsClosedWithoutArtifacts(t *testing.T) {
	ctx, st, _ := setupRunningDriverRun(t)
	worker := &TaskWorker{Store: st, WorkspaceKey: "TEST", NodeID: "task-worker-node-1", HeartbeatInterval: -1}
	wireTaskWorkerTestExecution(worker, st)
	worker.Artifacts = nil
	if _, err := worker.RunOnce(ctx); !errors.Is(err, artifactsmodule.ErrUnavailable) {
		t.Fatalf("RunOnce error = %v, want Artifacts unavailable", err)
	}
}
