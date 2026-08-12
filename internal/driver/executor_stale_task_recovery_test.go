package driver

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type periodicRecoveryAPIStub struct {
	mu       sync.Mutex
	commands []execution.RecoverStaleChildTaskRunsCommand
	called   chan execution.RecoverStaleChildTaskRunsCommand
}

func (stub *periodicRecoveryAPIStub) RecoverStaleChildTaskRuns(
	_ context.Context,
	_ authority.ExecutionAuthority,
	command execution.RecoverStaleChildTaskRunsCommand,
) (execution.RecoverStaleTaskRunsResult, error) {
	stub.mu.Lock()
	stub.commands = append(stub.commands, command)
	stub.mu.Unlock()
	stub.called <- command
	return execution.RecoverStaleTaskRunsResult{
		WorkspaceKey: command.WorkspaceKey,
		StaleBefore:  command.StaleBefore,
		RecoveredAt:  command.ObservedAt,
	}, nil
}

func (stub *periodicRecoveryAPIStub) callCount() int {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return len(stub.commands)
}

type recoveryGatedRunner struct {
	started chan struct{}
	release chan struct{}
}

func (runner *recoveryGatedRunner) Run(context.Context, RunRequest) (RunResult, error) {
	close(runner.started)
	<-runner.release
	return RunResult{Status: execution.DriverRunCompleted, Summary: "recovery loop tested"}, nil
}

type fenceAfterRunnerStartRecoveryAPIStub struct {
	runnerStarted <-chan struct{}
}

func (stub *fenceAfterRunnerStartRecoveryAPIStub) RecoverStaleChildTaskRuns(
	ctx context.Context,
	_ authority.ExecutionAuthority,
	_ execution.RecoverStaleChildTaskRunsCommand,
) (execution.RecoverStaleTaskRunsResult, error) {
	select {
	case <-ctx.Done():
		return execution.RecoverStaleTaskRunsResult{}, ctx.Err()
	case <-stub.runnerStarted:
		return execution.RecoverStaleTaskRunsResult{}, execution.ErrFenceConflict
	}
}

type fenceCancellationGatedRunner struct {
	started   chan struct{}
	cancelled chan struct{}
}

func (runner *fenceCancellationGatedRunner) Run(ctx context.Context, _ RunRequest) (RunResult, error) {
	close(runner.started)
	<-ctx.Done()
	close(runner.cancelled)
	return RunResult{Status: execution.DriverRunCompleted, Summary: "stale owner must not complete"}, ctx.Err()
}

type staleOwnerDriverRunAPIStub struct {
	execution.DriverRunAPI
	finalized chan execution.FinalizeDriverRunCommand
}

func (stub *staleOwnerDriverRunAPIStub) FinalizeDriverRun(
	_ context.Context,
	_ authority.ExecutionAuthority,
	command execution.FinalizeDriverRunCommand,
) (*execution.DriverRun, error) {
	stub.finalized <- command
	return nil, execution.ErrFenceConflict
}

type heartbeatFenceDriverRunAPIStub struct {
	execution.DriverRunAPI
	runnerStarted <-chan struct{}
	finalized     chan execution.FinalizeDriverRunCommand
}

func (stub *heartbeatFenceDriverRunAPIStub) HeartbeatDriverRun(
	ctx context.Context,
	_ authority.ExecutionAuthority,
	_ execution.DriverRunHeartbeatCommand,
) (*execution.DriverRun, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-stub.runnerStarted:
		return nil, execution.ErrFenceConflict
	}
}

func (stub *heartbeatFenceDriverRunAPIStub) FinalizeDriverRun(
	_ context.Context,
	_ authority.ExecutionAuthority,
	command execution.FinalizeDriverRunCommand,
) (*execution.DriverRun, error) {
	stub.finalized <- command
	return nil, execution.ErrFenceConflict
}

func TestExecutorPeriodicallyRecoversStaleChildrenOnlyDuringOwnedRun(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	state := memstore.New()
	if _, err := state.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	writeFlueDist(t, root, "epic-runner", "done")
	registered, err := SeedFlueDriverFixture(ctx, state, RegisterFlueOptions{
		WorkspaceKey: "TEST", WorkDir: root, DistPath: "dist", DriverName: "epic-runner", CreatedBy: "tester", Activate: true,
	})
	if err != nil {
		t.Fatalf("SeedFlueDriverFixture: %v", err)
	}
	if _, err := createDriverRunFixture(ctx, state, driverRunFixtureOptions{
		WorkspaceKey: "TEST", DriverID: registered.Driver.DriverID, EpicID: "TEST-1", RunID: "run-1",
	}); err != nil {
		t.Fatalf("CreateDriverRun: %v", err)
	}

	runner := &recoveryGatedRunner{started: make(chan struct{}), release: make(chan struct{})}
	recoveryAPI := &periodicRecoveryAPIStub{called: make(chan execution.RecoverStaleChildTaskRunsCommand, 8)}
	executor := testExecutor(state, Executor{
		Store: state, WorkspaceKey: "TEST", WorkDir: root, NodeID: "node-1", LeaseID: "lease-1",
		Runner: runner, HeartbeatInterval: time.Millisecond,
		TaskRunRecovery: recoveryAPI, StaleTaskRunMaxAge: 20 * time.Millisecond,
	})

	type runResult struct {
		result *ExecutionResult
		err    error
	}
	done := make(chan runResult, 1)
	go func() {
		result, runErr := executor.RunOnce(ctx)
		done <- runResult{result: result, err: runErr}
	}()

	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not start")
	}
	commands := make([]execution.RecoverStaleChildTaskRunsCommand, 0, 2)
	for range 2 {
		select {
		case command := <-recoveryAPI.called:
			commands = append(commands, command)
		case <-time.After(2 * time.Second):
			t.Fatal("stale-child recovery did not run immediately and after a successful parent heartbeat")
		}
	}
	second := commands[1]
	if second.WorkspaceKey != "TEST" || second.DriverRunID != "run-1" || second.ParentOwner.ResourceID != "run-1" ||
		second.ParentOwner.NodeID != "node-1" || second.ParentOwner.LeaseID != "lease-1" ||
		second.ParentOwner.LeaseToken == "" || second.ParentOwner.FencingToken <= 0 {
		t.Fatalf("periodic recovery owner = %+v command=%+v", second.ParentOwner, second)
	}
	if got := second.ObservedAt.Sub(second.StaleBefore); got != 20*time.Millisecond {
		t.Fatalf("recovery max age = %v, want 20ms", got)
	}
	if got := second.ObservedAt.Sub(commands[0].ObservedAt); got < 5*time.Millisecond {
		t.Fatalf("periodic recovery cadence = %v, want at least max-age/4 (5ms)", got)
	}

	close(runner.release)
	var completed runResult
	select {
	case completed = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunOnce did not finish")
	}
	if completed.err != nil {
		t.Fatalf("RunOnce: %v", completed.err)
	}
	if completed.result.Final == nil || completed.result.Final.Status != execution.DriverRunCompleted {
		t.Fatalf("final result = %+v", completed.result.Final)
	}

	// RunOnce drains the owner loop before finalizing, so it cannot issue a
	// stale-child command after the parent generation becomes terminal.
	callsAtFinish := recoveryAPI.callCount()
	time.Sleep(4 * executor.HeartbeatInterval)
	if got := recoveryAPI.callCount(); got != callsAtFinish {
		t.Fatalf("recovery calls after parent finish = %d -> %d", callsAtFinish, got)
	}
}

func TestExecutorCancelsRunnerWhenStaleChildRecoveryLosesParentFence(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	state := memstore.New()
	if _, err := state.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	writeFlueDist(t, root, "epic-runner", "done")
	registered, err := SeedFlueDriverFixture(ctx, state, RegisterFlueOptions{
		WorkspaceKey: "TEST", WorkDir: root, DistPath: "dist", DriverName: "epic-runner", CreatedBy: "tester", Activate: true,
	})
	if err != nil {
		t.Fatalf("SeedFlueDriverFixture: %v", err)
	}
	if _, err := createDriverRunFixture(ctx, state, driverRunFixtureOptions{
		WorkspaceKey: "TEST", DriverID: registered.Driver.DriverID, EpicID: "TEST-1", RunID: "run-fenced",
	}); err != nil {
		t.Fatalf("CreateDriverRun: %v", err)
	}

	runner := &fenceCancellationGatedRunner{started: make(chan struct{}), cancelled: make(chan struct{})}
	executor := testExecutor(state, Executor{
		Store: state, WorkspaceKey: "TEST", WorkDir: root, NodeID: "node-1", LeaseID: "lease-1",
		Runner: runner, HeartbeatInterval: time.Hour,
		TaskRunRecovery: &fenceAfterRunnerStartRecoveryAPIStub{runnerStarted: runner.started},
	})
	staleOwnerAPI := &staleOwnerDriverRunAPIStub{
		DriverRunAPI: executor.Execution,
		finalized:    make(chan execution.FinalizeDriverRunCommand, 1),
	}
	executor.Execution = staleOwnerAPI

	type runOutcome struct {
		result *ExecutionResult
		err    error
	}
	done := make(chan runOutcome, 1)
	go func() {
		result, runErr := executor.RunOnce(ctx)
		done <- runOutcome{result: result, err: runErr}
	}()

	select {
	case <-runner.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("runner context was not cancelled after stale recovery lost the parent fence")
	}

	var outcome runOutcome
	select {
	case outcome = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunOnce did not return after canceling the fenced runner")
	}
	if !errors.Is(outcome.err, execution.ErrFenceConflict) {
		t.Fatalf("RunOnce error = %v, want parent fence conflict", outcome.err)
	}
	if outcome.result == nil || outcome.result.Claimed == nil || outcome.result.Final != nil {
		t.Fatalf("RunOnce result = %+v, want claimed run without successful finalization", outcome.result)
	}

	select {
	case command := <-staleOwnerAPI.finalized:
		if command.Status != execution.DriverRunStatus(domain.DriverRunCancelled) || command.ErrorClass != "driver_cancelled" {
			t.Fatalf("stale-owner finalization attempt = %+v, want cancelled/driver_cancelled", command)
		}
	default:
		t.Fatal("executor did not attempt fenced cancellation finalization")
	}
	stored, err := state.DriverRuns().Get(ctx, "TEST", "run-fenced")
	if err != nil {
		t.Fatalf("Get driver run: %v", err)
	}
	if stored.Status != domain.DriverRunRunning || stored.FinishedAt != nil {
		t.Fatalf("stale-owner run = %+v, must not be successfully finalized", stored)
	}
}

func TestExecutorCancelsRunnerWhenParentHeartbeatLosesFence(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	state := memstore.New()
	if _, err := state.Workspaces().Create(ctx, store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	writeFlueDist(t, root, "epic-runner", "done")
	registered, err := SeedFlueDriverFixture(ctx, state, RegisterFlueOptions{
		WorkspaceKey: "TEST", WorkDir: root, DistPath: "dist", DriverName: "epic-runner", CreatedBy: "tester", Activate: true,
	})
	if err != nil {
		t.Fatalf("SeedFlueDriverFixture: %v", err)
	}
	if _, err := createDriverRunFixture(ctx, state, driverRunFixtureOptions{
		WorkspaceKey: "TEST", DriverID: registered.Driver.DriverID, EpicID: "TEST-1", RunID: "run-heartbeat-fenced",
	}); err != nil {
		t.Fatalf("CreateDriverRun: %v", err)
	}

	runner := &fenceCancellationGatedRunner{started: make(chan struct{}), cancelled: make(chan struct{})}
	executor := testExecutor(state, Executor{
		Store: state, WorkspaceKey: "TEST", WorkDir: root, NodeID: "node-1", LeaseID: "lease-1",
		Runner: runner, HeartbeatInterval: time.Millisecond,
	})
	staleOwnerAPI := &heartbeatFenceDriverRunAPIStub{
		DriverRunAPI:  executor.Execution,
		runnerStarted: runner.started,
		finalized:     make(chan execution.FinalizeDriverRunCommand, 1),
	}
	executor.Execution = staleOwnerAPI

	type runOutcome struct {
		result *ExecutionResult
		err    error
	}
	done := make(chan runOutcome, 1)
	go func() {
		result, runErr := executor.RunOnce(ctx)
		done <- runOutcome{result: result, err: runErr}
	}()

	select {
	case <-runner.cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("runner context was not cancelled after parent heartbeat lost its fence")
	}

	var outcome runOutcome
	select {
	case outcome = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunOnce did not return after heartbeat fence cancellation")
	}
	if !errors.Is(outcome.err, execution.ErrFenceConflict) {
		t.Fatalf("RunOnce error = %v, want parent fence conflict", outcome.err)
	}
	select {
	case command := <-staleOwnerAPI.finalized:
		if command.Status != execution.DriverRunStatus(domain.DriverRunCancelled) || command.ErrorClass != "driver_cancelled" {
			t.Fatalf("stale-owner finalization attempt = %+v, want cancelled/driver_cancelled", command)
		}
	default:
		t.Fatal("executor did not attempt fenced cancellation finalization")
	}
}
