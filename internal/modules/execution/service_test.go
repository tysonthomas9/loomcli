package execution

import (
	"context"
	"errors"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type claimPortStub struct {
	claim       ClaimStart
	claimErr    error
	failure     ExitClassification
	failureCall int
}

func (stub *claimPortStub) ClaimAndStart(context.Context, ClaimAndLaunchCommand) (ClaimStart, error) {
	return stub.claim, stub.claimErr
}

func (stub *claimPortStub) RecordLaunchFailure(_ context.Context, _ ClaimStart, classification ExitClassification) error {
	stub.failureCall++
	stub.failure = classification
	return nil
}

type launcherStub struct {
	receipt LaunchReceipt
	err     error
}

func (stub launcherStub) Launch(context.Context, ClaimStart, []byte) (LaunchReceipt, error) {
	return stub.receipt, stub.err
}

type heartbeatPortStub struct{ calls int }

func (stub *heartbeatPortStub) Heartbeat(_ context.Context, command HeartbeatCommand) (HeartbeatResult, error) {
	stub.calls++
	return HeartbeatResult{Owner: command.Owner}, nil
}

type logPortStub struct {
	calls   int
	command AppendLogCommand
	entry   LogEntry
}

func (stub *logPortStub) AppendLog(_ context.Context, command AppendLogCommand) (LogEntry, error) {
	stub.calls++
	stub.command = command
	return stub.entry, nil
}

type finalizerPortStub struct{}

func (finalizerPortStub) Finalize(_ context.Context, command FinalizeCommand) (FinalizeResult, error) {
	return FinalizeResult{
		Owner: command.Owner, Status: command.Classification.Status, FinishedAt: command.FinishedAt,
	}, nil
}

func TestClaimAndLaunchUsesAtomicClaimBeforeExternalLaunch(t *testing.T) {
	now := time.Now().UTC()
	owner := Owner{ResourceKind: ResourceTaskRun, ResourceID: "task-run-1", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "token-1", FencingToken: 7}
	claims := &claimPortStub{claim: ClaimStart{Owner: owner, WorkItemID: "TASK-1", TaskRunID: "task-run-1", StartedAt: now}}
	service, issuer := newTestService(t, Dependencies{Claims: claims, Launcher: launcherStub{receipt: LaunchReceipt{ProcessRef: "pid:42", StartedAt: now}}})
	command := ClaimAndLaunchCommand{
		WorkspaceKey: "TEST", RequestID: "request-1", WorkItemID: "TASK-1", TaskRunID: "task-run-1",
		RunnerRef: "local-task-runner", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "token-1", LeaseTTL: time.Minute,
	}
	result, err := service.ClaimAndLaunch(context.Background(), issueSystem(t, issuer, ActionClaimAndLaunch), command)
	if err != nil {
		t.Fatalf("ClaimAndLaunch: %v", err)
	}
	wantPublicOwner := owner
	wantPublicOwner.LeaseToken = ""
	if result.Claim.Owner != wantPublicOwner || result.Launch.ProcessRef != "pid:42" || claims.failureCall != 0 {
		t.Fatalf("result = %#v, failure calls = %d", result, claims.failureCall)
	}
}

func TestClaimAndLaunchFencesLaunchFailureCompensation(t *testing.T) {
	owner := Owner{ResourceKind: ResourceTaskRun, ResourceID: "task-run-1", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "token-1", FencingToken: 8}
	claims := &claimPortStub{claim: ClaimStart{Owner: owner, WorkItemID: "TASK-1", TaskRunID: "task-run-1"}}
	service, issuer := newTestService(t, Dependencies{Claims: claims, Launcher: launcherStub{err: errors.New("backend unavailable")}})
	_, err := service.ClaimAndLaunch(context.Background(), issueSystem(t, issuer, ActionClaimAndLaunch), ClaimAndLaunchCommand{
		WorkspaceKey: "TEST", RequestID: "request-1", WorkItemID: "TASK-1", TaskRunID: "task-run-1",
		RunnerRef: "local-task-runner", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "token-1", LeaseTTL: time.Minute,
	})
	if !errors.Is(err, ErrLaunchFailed) || claims.failureCall != 1 || claims.failure.ErrorClass != "launch_failed" || !claims.failure.Retryable {
		t.Fatalf("error = %v, failure = %#v, calls = %d", err, claims.failure, claims.failureCall)
	}
}

func TestExecutionResultsNeverExposeLeaseToken(t *testing.T) {
	owner := Owner{
		ResourceKind: ResourceTaskRun, ResourceID: "task-run-1", NodeID: "node-1",
		LeaseID: "lease-1", LeaseToken: "secret-token", FencingToken: 10,
	}
	heartbeats := &heartbeatPortStub{}
	service, issuer := newTestService(t, Dependencies{Heartbeats: heartbeats, Finalizer: finalizerPortStub{}})
	heartbeat, err := service.Heartbeat(context.Background(), issueExecution(t, issuer, ActionHeartbeat, owner), HeartbeatCommand{
		WorkspaceKey: "TEST", Owner: owner, At: time.Now(),
	})
	if err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if heartbeat.Owner.LeaseToken != "" {
		t.Fatal("Heartbeat result leaked lease token")
	}
	finishedAt := time.Now()
	finalized, err := service.Finalize(context.Background(), issueExecution(t, issuer, ActionFinalize, owner), FinalizeCommand{
		WorkspaceKey: "TEST", RequestID: "completion-1", Owner: owner,
		Classification: ExitClassification{Status: StatusSucceeded}, FinishedAt: finishedAt,
	})
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if finalized.Owner.LeaseToken != "" {
		t.Fatal("Finalize result leaked lease token")
	}
}

func TestAppendLogRequiresStableReplayIdentityAndTimestamp(t *testing.T) {
	owner := Owner{
		ResourceKind: ResourceTaskRun, ResourceID: "task-run-1", NodeID: "node-1",
		LeaseID: "lease-1", LeaseToken: "secret-token", FencingToken: 10,
	}
	logs := &logPortStub{entry: LogEntry{TaskRunID: "task-run-1", Sequence: 1}}
	service, issuer := newTestService(t, Dependencies{Logs: logs})
	auth := issueExecution(t, issuer, ActionAppendLog, owner)
	timestamp := time.Date(2026, 7, 16, 19, 30, 0, 0, time.UTC)

	for _, command := range []AppendLogCommand{
		{WorkspaceKey: "TEST", Owner: owner, Text: "line\n", Timestamp: timestamp},
		{WorkspaceKey: "TEST", RequestID: "log-1", Owner: owner, Text: "line\n"},
	} {
		if _, err := service.AppendLog(context.Background(), auth, command); !errors.Is(err, ErrInvalid) {
			t.Fatalf("AppendLog(%+v) error = %v, want ErrInvalid", command, err)
		}
	}
	if logs.calls != 0 {
		t.Fatalf("log port calls after invalid commands = %d, want 0", logs.calls)
	}

	entry, err := service.AppendLog(context.Background(), auth, AppendLogCommand{
		WorkspaceKey: "TEST", RequestID: " log-1 ", Owner: owner,
		Stream: "stdout", Text: "line\n", Timestamp: timestamp,
	})
	if err != nil {
		t.Fatalf("AppendLog valid command: %v", err)
	}
	if entry.Sequence != 1 || logs.calls != 1 || logs.command.RequestID != "log-1" || !logs.command.Timestamp.Equal(timestamp) {
		t.Fatalf("entry/captured command = %+v / %+v, calls=%d", entry, logs.command, logs.calls)
	}
}

func TestFinalizeRejectsNonFiniteUsage(t *testing.T) {
	owner := Owner{ResourceKind: ResourceTaskRun, ResourceID: "task-run-1", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "secret", FencingToken: 10}
	service, issuer := newTestService(t, Dependencies{Finalizer: finalizerPortStub{}})
	auth := issueExecution(t, issuer, ActionFinalize, owner)
	for _, cost := range []float64{math.NaN(), math.Inf(1)} {
		_, err := service.Finalize(context.Background(), auth, FinalizeCommand{
			WorkspaceKey: "TEST", RequestID: "completion-1", Owner: owner,
			Classification:   ExitClassification{Status: StatusSucceeded},
			EstimatedCostUSD: cost, FinishedAt: time.Now().UTC(),
		})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("non-finite cost %v error=%v, want invalid", cost, err)
		}
	}
}

func TestExecutionMutationRejectsAuthorityForAnotherResource(t *testing.T) {
	heartbeats := &heartbeatPortStub{}
	service, issuer := newTestService(t, Dependencies{Heartbeats: heartbeats})
	owner := Owner{ResourceKind: ResourceTaskRun, ResourceID: "task-run-1", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "token-1", FencingToken: 9}
	auth := issueExecution(t, issuer, ActionHeartbeat, Owner{ResourceKind: ResourceTaskRun, ResourceID: "task-run-other", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "token-1", FencingToken: 9})
	_, err := service.Heartbeat(context.Background(), auth, HeartbeatCommand{WorkspaceKey: "TEST", Owner: owner, At: time.Now()})
	if !errors.Is(err, ErrFenceConflict) || heartbeats.calls != 0 {
		t.Fatalf("error = %v, calls = %d", err, heartbeats.calls)
	}
}

func TestRuntimeRegistrationsOwnStableExecutionComponents(t *testing.T) {
	pass := RuntimePassFunc(func(context.Context) error { return nil })
	registrations, err := RuntimeRegistrations(RuntimeConfig{
		DriverExecutor: pass, TaskWorkers: []RuntimePass{pass, pass}, AwaitTimeouts: pass,
		TaskRunConvergence: pass,
	})
	if err != nil {
		t.Fatalf("RuntimeRegistrations: %v", err)
	}
	ids := make([]string, 0, len(registrations))
	for _, registration := range registrations {
		ids = append(ids, string(registration.Component.ID()))
		if !registration.Policy.Immediate || registration.Policy.Cadence <= 0 || registration.Policy.Timeout != 0 {
			t.Fatalf("policy for %s = %#v", registration.Component.ID(), registration.Policy)
		}
	}
	want := []string{
		"execution-driver-run", "execution-task-run-worker-1", "execution-task-run-worker-2",
		"execution-await-timeout-recovery", "execution-task-run-convergence",
	}
	if !slices.Equal(ids, want) {
		t.Fatalf("component ids = %v, want %v", ids, want)
	}
}

func newTestService(t *testing.T, dependencies Dependencies) (*Service, *authority.Issuer) {
	t.Helper()
	issuer := authority.NewIssuer()
	admission, err := issuer.NewAdmission(OperationRules()...)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(dependencies, admission)
	if err != nil {
		t.Fatal(err)
	}
	return service, issuer
}

func issueSystem(t *testing.T, issuer *authority.Issuer, action authority.Action) authority.SystemAuthority {
	t.Helper()
	principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "runtime-test", Class: authority.ClassSystem, Workspace: "TEST",
		Actions: []authority.Action{action}, ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := issuer.IssueSystem(principal, "TEST", action, "execution module test")
	if err != nil {
		t.Fatal(err)
	}
	return auth
}

func issueExecution(t *testing.T, issuer *authority.Issuer, action authority.Action, owner Owner) authority.ExecutionAuthority {
	t.Helper()
	principal, err := issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: owner.ResourceID, Class: authority.ClassExecution, Workspace: "TEST",
		Actions: []authority.Action{action}, ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	auth, err := issuer.IssueExecutionForOwner(principal, "TEST", action, authority.ExecutionOwner{
		ResourceKind: authority.ExecutionResourceKind(owner.ResourceKind), ResourceID: owner.ResourceID,
		NodeID: owner.NodeID, LeaseID: owner.LeaseID, FencingToken: owner.FencingToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	return auth
}
