package execution

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type driverRunChildStartPortStub struct {
	command StartChildDriverRunCommand
	result  StartChildDriverRunResult
	err     error
	calls   int
}

func (stub *driverRunChildStartPortStub) StartChildDriverRun(_ context.Context, command StartChildDriverRunCommand) (StartChildDriverRunResult, error) {
	stub.calls++
	stub.command = command
	return stub.result, stub.err
}

type driverRunCascadePortStub struct {
	command CascadeChildDriverRunsCommand
	result  CascadeChildDriverRunsResult
	err     error
	calls   int
}

type driverRunWorkItemPortStub struct {
	claimCommand   ClaimDriverRunWorkItemCommand
	releaseCommand ReleaseDriverRunWorkItemCommand
	handoffCommand HandoffDriverRunReviewWorkItemCommand
	claimResult    DriverRunWorkItemMutationResult
	releaseResult  DriverRunWorkItemMutationResult
	handoffResult  DriverRunWorkItemMutationResult
	claimErr       error
	releaseErr     error
	handoffErr     error
	claimCalls     int
	releaseCalls   int
	handoffCalls   int
}

func (stub *driverRunWorkItemPortStub) ClaimDriverRunWorkItem(_ context.Context, command ClaimDriverRunWorkItemCommand) (DriverRunWorkItemMutationResult, error) {
	stub.claimCalls++
	stub.claimCommand = command
	return stub.claimResult, stub.claimErr
}

func (stub *driverRunWorkItemPortStub) ReleaseDriverRunWorkItem(_ context.Context, command ReleaseDriverRunWorkItemCommand) (DriverRunWorkItemMutationResult, error) {
	stub.releaseCalls++
	stub.releaseCommand = command
	return stub.releaseResult, stub.releaseErr
}

func (stub *driverRunWorkItemPortStub) HandoffDriverRunReviewWorkItem(_ context.Context, command HandoffDriverRunReviewWorkItemCommand) (DriverRunWorkItemMutationResult, error) {
	stub.handoffCalls++
	stub.handoffCommand = command
	return stub.handoffResult, stub.handoffErr
}

func (stub *driverRunCascadePortStub) CascadeChildDriverRuns(_ context.Context, command CascadeChildDriverRunsCommand) (CascadeChildDriverRunsResult, error) {
	stub.calls++
	stub.command = command
	return stub.result, stub.err
}

func TestStartChildDriverRunUsesOneParentFencedAtomicPort(t *testing.T) {
	owner := Owner{ResourceKind: ResourceDriverRun, ResourceID: "run-parent", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "driver-token-1", FencingToken: 7}
	childKey := "review"
	childRunID := ChildDriverRunID(owner.ResourceID, childKey)
	requestID := ChildDriverRunRequestID(owner.ResourceID, childKey)
	payload := json.RawMessage(`{"prompt":"review"}`)
	port := &driverRunChildStartPortStub{result: StartChildDriverRunResult{
		Parent: &DriverRun{WorkspaceKey: "TEST", RunID: owner.ResourceID, Status: DriverRunRunning, Owner: owner},
		Child: &DriverRun{
			WorkspaceKey: "TEST", RunID: childRunID, DriverID: "driver-1", DriverVersionID: "version-1",
			Entrypoint: "run", SourceKind: "workflow", SourceRef: owner.ResourceID, ParentRunID: owner.ResourceID,
			Status: DriverRunQueued, IdempotencyKey: requestID, Payload: append(json.RawMessage(nil), payload...),
		},
		ParentDepth: 1, ChildDepth: 2, ActionID: "child-start-action",
	}}
	service, issuer := newDriverRunTestService(t, DriverRunDependencies{ChildStarts: port})
	command := StartChildDriverRunCommand{
		WorkspaceKey: "TEST", RequestID: requestID, Owner: owner, ChildKey: childKey,
		DriverID: "driver-1", DriverVersionID: "version-1", Payload: payload, MaxDepth: 8,
	}
	child, err := service.StartChildDriverRun(context.Background(), issueExecution(t, issuer, ActionStartChildDriverRun, owner), command)
	if err != nil || child.RunID != childRunID || port.command.ChildRunID != childRunID || port.calls != 1 {
		t.Fatalf("child=%+v command=%+v calls=%d err=%v", child, port.command, port.calls, err)
	}

	// A lost response replays the immutable original queued child receipt even
	// when the live child has advanced separately.
	port.result.Replay = true
	if child, err = service.StartChildDriverRun(context.Background(), issueExecution(t, issuer, ActionStartChildDriverRun, owner), command); err != nil || child.Status != DriverRunQueued {
		t.Fatalf("immutable child replay=%+v err=%v", child, err)
	}
	port.result.Child.Status = DriverRunRunning
	if _, err := service.StartChildDriverRun(context.Background(), issueExecution(t, issuer, ActionStartChildDriverRun, owner), command); !errors.Is(err, ErrConflict) {
		t.Fatalf("mutable child replay error=%v, want conflict", err)
	}
}

func TestStartChildDriverRunRejectsDepthDivergenceAndMissingAtomicPort(t *testing.T) {
	owner := Owner{ResourceKind: ResourceDriverRun, ResourceID: "run-parent", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "driver-token-1", FencingToken: 7}
	childKey := "review"
	requestID := ChildDriverRunRequestID(owner.ResourceID, childKey)
	command := StartChildDriverRunCommand{
		WorkspaceKey: "TEST", RequestID: requestID, Owner: owner, ChildKey: childKey,
		DriverID: "driver-1", DriverVersionID: "version-1", Payload: json.RawMessage(`{}`), MaxDepth: 2,
	}
	childRunID := ChildDriverRunID(owner.ResourceID, childKey)
	port := &driverRunChildStartPortStub{result: StartChildDriverRunResult{
		Parent: &DriverRun{WorkspaceKey: "TEST", RunID: owner.ResourceID, Status: DriverRunRunning, Owner: owner},
		Child: &DriverRun{
			WorkspaceKey: "TEST", RunID: childRunID, DriverID: "driver-1", DriverVersionID: "version-1", Entrypoint: "run",
			SourceKind: "workflow", SourceRef: owner.ResourceID, ParentRunID: owner.ResourceID,
			Status: DriverRunQueued, IdempotencyKey: requestID, Payload: json.RawMessage(`{}`),
		},
		ParentDepth: 2, ChildDepth: 3, ActionID: "child-start-action",
	}}
	service, issuer := newDriverRunTestService(t, DriverRunDependencies{ChildStarts: port})
	if _, err := service.StartChildDriverRun(context.Background(), issueExecution(t, issuer, ActionStartChildDriverRun, owner), command); !errors.Is(err, ErrCompositionDepthExceeded) {
		t.Fatalf("depth overflow error=%v, want composition depth", err)
	}
	service, issuer = newDriverRunTestService(t, DriverRunDependencies{})
	if _, err := service.StartChildDriverRun(context.Background(), issueExecution(t, issuer, ActionStartChildDriverRun, owner), command); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing atomic port error=%v, want unavailable", err)
	}
}

func TestStartChildDriverRunReplayAcceptsSuccessorOwnerAndRejectsStaleOwner(t *testing.T) {
	oldOwner := Owner{ResourceKind: ResourceDriverRun, ResourceID: "run-parent", NodeID: "node-old", LeaseID: "lease-old", LeaseToken: "old-token", FencingToken: 7}
	successor := Owner{ResourceKind: ResourceDriverRun, ResourceID: "run-parent", NodeID: "node-new", LeaseID: "lease-new", LeaseToken: "new-token", FencingToken: 8}
	childKey := "review"
	childRunID := ChildDriverRunID(oldOwner.ResourceID, childKey)
	requestID := ChildDriverRunRequestID(oldOwner.ResourceID, childKey)
	port := &driverRunChildStartPortStub{result: StartChildDriverRunResult{
		Parent: &DriverRun{WorkspaceKey: "TEST", RunID: oldOwner.ResourceID, Status: DriverRunRunning, Owner: publicOwner(oldOwner)},
		Child: &DriverRun{
			WorkspaceKey: "TEST", RunID: childRunID, DriverID: "driver-1", DriverVersionID: "version-1", Entrypoint: "run",
			SourceKind: "workflow", SourceRef: successor.ResourceID, ParentRunID: successor.ResourceID,
			Status: DriverRunQueued, IdempotencyKey: requestID, Payload: json.RawMessage(`{}`),
		},
		ParentDepth: 1, ChildDepth: 2, ActionID: "child-start-action", Replay: true,
	}}
	service, issuer := newDriverRunTestService(t, DriverRunDependencies{ChildStarts: port})
	command := StartChildDriverRunCommand{
		WorkspaceKey: "TEST", RequestID: requestID, Owner: successor, ChildKey: childKey,
		DriverID: "driver-1", DriverVersionID: "version-1", Payload: json.RawMessage(`{}`), MaxDepth: 8,
	}
	if _, err := service.StartChildDriverRun(context.Background(), issueExecution(t, issuer, ActionStartChildDriverRun, successor), command); err != nil {
		t.Fatalf("successor replay: %v", err)
	}
	port.err = ErrFenceConflict
	command.Owner = oldOwner
	if _, err := service.StartChildDriverRun(context.Background(), issueExecution(t, issuer, ActionStartChildDriverRun, oldOwner), command); !errors.Is(err, ErrFenceConflict) {
		t.Fatalf("stale predecessor replay error=%v, want fence conflict", err)
	}
}

func TestCascadeChildDriverRunsRequiresAtomicCommittedSubtree(t *testing.T) {
	now := time.Now().UTC()
	owner := Owner{ResourceKind: ResourceDriverRun, ResourceID: "run-parent", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "driver-token-1", FencingToken: 7}
	reason := "parent run reached terminal status failed"
	requestedAt := now.Add(-time.Second)
	port := &driverRunCascadePortStub{result: CascadeChildDriverRunsResult{
		CancelledRuns: []*DriverRun{
			{WorkspaceKey: "TEST", RunID: "run-child-a", ParentRunID: owner.ResourceID, Status: DriverRunCancelled, Summary: reason, ErrorClass: "parent_run_terminal"},
			{WorkspaceKey: "TEST", RunID: "run-grandchild", ParentRunID: "run-child-a", Status: DriverRunCancelled, Summary: reason, ErrorClass: "parent_run_terminal"},
		},
		CancelRequestedRuns: []*DriverRun{
			{WorkspaceKey: "TEST", RunID: "run-child-running", ParentRunID: "run-child-a", Status: DriverRunRunning,
				Owner:             Owner{ResourceKind: ResourceDriverRun, ResourceID: "run-child-running", NodeID: "node-2", LeaseID: "lease-2", LeaseToken: "child-token", FencingToken: 9},
				CancelRequestedAt: &requestedAt, CancelRequestedReason: reason},
		},
		Committed: &CascadeChildDriverRunsCommit{
			WorkspaceKey: "TEST", ParentRunID: owner.ResourceID, ParentStatus: DriverRunFailed,
			Reason: reason, ErrorClass: "parent_run_terminal", CascadedAt: now, MaxDepth: 8,
			CancelledRunIDs: []string{"run-child-a", "run-grandchild"}, CancelRequestedRunIDs: []string{"run-child-running"},
		},
		ActionID: "cascade-action",
	}}
	service, issuer := newDriverRunTestService(t, DriverRunDependencies{Cascades: port})
	command := CascadeChildDriverRunsCommand{
		WorkspaceKey: "TEST", RequestID: CascadeChildDriverRunsRequestID(owner.ResourceID, DriverRunFailed),
		Owner: owner, ParentStatus: DriverRunFailed, Reason: reason, ErrorClass: "parent_run_terminal", CascadedAt: now, MaxDepth: 8,
	}
	result, err := service.CascadeChildDriverRuns(context.Background(), issueExecution(t, issuer, ActionCascadeChildDriverRuns, owner), command)
	if err != nil || len(result.CancelledRuns) != 2 || len(result.CancelRequestedRuns) != 1 || port.calls != 1 {
		t.Fatalf("result=%+v calls=%d err=%v", result, port.calls, err)
	}
	if result.CancelRequestedRuns[0].Owner != (Owner{}) {
		t.Fatalf("cascade result leaked current child owner: %+v", result.CancelRequestedRuns[0].Owner)
	}

	// The cooperative request can finish before a lost response is retried.
	port.result.Replay = true
	port.result.CancelRequestedRuns[0].Status = DriverRunCompleted
	port.result.CancelRequestedRuns[0].Owner = Owner{}
	port.result.CancelRequestedRuns[0].CancelRequestedAt = nil
	if _, err := service.CascadeChildDriverRuns(context.Background(), issueExecution(t, issuer, ActionCascadeChildDriverRuns, owner), command); err != nil {
		t.Fatalf("cascade replay after child terminalized: %v", err)
	}
	port.result.Committed.CancelRequestedRunIDs = []string{"divergent-child"}
	if _, err := service.CascadeChildDriverRuns(context.Background(), issueExecution(t, issuer, ActionCascadeChildDriverRuns, owner), command); !errors.Is(err, ErrConflict) {
		t.Fatalf("divergent cascade receipt error=%v, want conflict", err)
	}
}

func TestCascadeChildDriverRunsAuthorizationAndMissingPortFailClosed(t *testing.T) {
	now := time.Now().UTC()
	owner := Owner{ResourceKind: ResourceDriverRun, ResourceID: "run-parent", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "driver-token-1", FencingToken: 7}
	command := CascadeChildDriverRunsCommand{
		WorkspaceKey: "TEST", RequestID: CascadeChildDriverRunsRequestID(owner.ResourceID, DriverRunFailed),
		Owner: owner, ParentStatus: DriverRunFailed, Reason: "parent failed", ErrorClass: "parent_run_terminal", CascadedAt: now, MaxDepth: 8,
	}
	service, issuer := newDriverRunTestService(t, DriverRunDependencies{})
	if _, err := service.CascadeChildDriverRuns(context.Background(), issueExecution(t, issuer, ActionCascadeChildDriverRuns, owner), command); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing cascade port error=%v, want unavailable", err)
	}
	port := &driverRunCascadePortStub{}
	service, issuer = newDriverRunTestService(t, DriverRunDependencies{Cascades: port})
	foreign := owner
	foreign.ResourceID = "run-foreign"
	if _, err := service.CascadeChildDriverRuns(context.Background(), issueExecution(t, issuer, ActionCascadeChildDriverRuns, foreign), command); !errors.Is(err, ErrFenceConflict) || port.calls != 0 {
		t.Fatalf("foreign cascade error=%v calls=%d", err, port.calls)
	}
}

func TestCascadeChildDriverRunsRejectsDetachedCyclesAndOverDepthSubtrees(t *testing.T) {
	now := time.Now().UTC()
	owner := Owner{ResourceKind: ResourceDriverRun, ResourceID: "run-parent", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "driver-token-1", FencingToken: 7}
	reason := "parent failed"
	command := CascadeChildDriverRunsCommand{
		WorkspaceKey: "TEST", RequestID: CascadeChildDriverRunsRequestID(owner.ResourceID, DriverRunFailed),
		Owner: owner, ParentStatus: DriverRunFailed, Reason: reason, ErrorClass: "parent_run_terminal",
		CascadedAt: now, MaxDepth: 8,
	}
	port := &driverRunCascadePortStub{result: CascadeChildDriverRunsResult{
		CancelledRuns: []*DriverRun{
			{WorkspaceKey: "TEST", RunID: "run-a", ParentRunID: "run-b", Status: DriverRunCancelled, Summary: reason, ErrorClass: "parent_run_terminal"},
			{WorkspaceKey: "TEST", RunID: "run-b", ParentRunID: "run-a", Status: DriverRunCancelled, Summary: reason, ErrorClass: "parent_run_terminal"},
		},
		Committed: &CascadeChildDriverRunsCommit{
			WorkspaceKey: "TEST", ParentRunID: owner.ResourceID, ParentStatus: DriverRunFailed,
			Reason: reason, ErrorClass: "parent_run_terminal", CascadedAt: now, MaxDepth: 8,
			CancelledRunIDs: []string{"run-a", "run-b"},
		},
		ActionID: "cascade-action",
	}}
	service, issuer := newDriverRunTestService(t, DriverRunDependencies{Cascades: port})
	auth := issueExecution(t, issuer, ActionCascadeChildDriverRuns, owner)
	if _, err := service.CascadeChildDriverRuns(context.Background(), auth, command); !errors.Is(err, ErrConflict) {
		t.Fatalf("detached cycle error=%v, want conflict", err)
	}

	port.result.CancelledRuns[0].ParentRunID = owner.ResourceID
	port.result.CancelledRuns[1].ParentRunID = "run-a"
	command.MaxDepth = 1
	port.result.Committed.MaxDepth = 1
	if _, err := service.CascadeChildDriverRuns(context.Background(), auth, command); !errors.Is(err, ErrConflict) {
		t.Fatalf("over-depth subtree error=%v, want conflict", err)
	}

	port.result.CancelledRuns = []*DriverRun{{
		WorkspaceKey: "TEST", RunID: owner.ResourceID, ParentRunID: owner.ResourceID,
		Status: DriverRunCancelled, Summary: reason, ErrorClass: "parent_run_terminal",
	}}
	port.result.Committed.CancelledRunIDs = []string{owner.ResourceID}
	command.MaxDepth = 8
	port.result.Committed.MaxDepth = 8
	if _, err := service.CascadeChildDriverRuns(context.Background(), auth, command); !errors.Is(err, ErrConflict) {
		t.Fatalf("parent included as descendant error=%v, want conflict", err)
	}
}

func TestCascadeChildDriverRunsRejectsDivergentSemanticReceipt(t *testing.T) {
	now := time.Now().UTC()
	owner := Owner{ResourceKind: ResourceDriverRun, ResourceID: "run-parent", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "driver-token-1", FencingToken: 7}
	reason := "parent failed"
	command := CascadeChildDriverRunsCommand{
		WorkspaceKey: "TEST", RequestID: CascadeChildDriverRunsRequestID(owner.ResourceID, DriverRunFailed),
		Owner: owner, ParentStatus: DriverRunFailed, Reason: reason, ErrorClass: "parent_run_terminal",
		CascadedAt: now.Add(time.Minute), MaxDepth: 8,
	}
	tests := []struct {
		name   string
		mutate func(*CascadeChildDriverRunsResult)
	}{
		{name: "reason", mutate: func(result *CascadeChildDriverRunsResult) { result.Committed.Reason = "different reason" }},
		{name: "error class", mutate: func(result *CascadeChildDriverRunsResult) { result.Committed.ErrorClass = "different_class" }},
		{name: "persisted time", mutate: func(result *CascadeChildDriverRunsResult) { result.Committed.CascadedAt = time.Time{} }},
		{name: "duplicate id", mutate: func(result *CascadeChildDriverRunsResult) {
			result.Committed.CancelledRunIDs = []string{"run-child", "run-child"}
		}},
		{name: "noncanonical id", mutate: func(result *CascadeChildDriverRunsResult) {
			result.Committed.CancelledRunIDs = []string{" run-child "}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := CascadeChildDriverRunsResult{
				CancelledRuns: []*DriverRun{{
					WorkspaceKey: "TEST", RunID: "run-child", ParentRunID: owner.ResourceID,
					Status: DriverRunCancelled, Summary: reason, ErrorClass: "parent_run_terminal",
				}},
				Committed: &CascadeChildDriverRunsCommit{
					WorkspaceKey: "TEST", ParentRunID: owner.ResourceID, ParentStatus: DriverRunFailed,
					Reason: reason, ErrorClass: "parent_run_terminal", CascadedAt: now, MaxDepth: 8,
					CancelledRunIDs: []string{"run-child"},
				},
				ActionID: "cascade-action", Replay: true,
			}
			test.mutate(&result)
			port := &driverRunCascadePortStub{result: result}
			service, issuer := newDriverRunTestService(t, DriverRunDependencies{Cascades: port})
			auth := issueExecution(t, issuer, ActionCascadeChildDriverRuns, owner)
			if _, err := service.CascadeChildDriverRuns(context.Background(), auth, command); !errors.Is(err, ErrConflict) {
				t.Fatalf("cascade error=%v, want conflict", err)
			}
		})
	}
}

func TestCascadeChildDriverRunsLiveAndRecoveryRaceConvergesOnSameReceipt(t *testing.T) {
	now := time.Now().UTC()
	owner := Owner{ResourceKind: ResourceDriverRun, ResourceID: "run-parent", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "driver-token-1", FencingToken: 7}
	reason := "parent failed"
	requestID := CascadeChildDriverRunsRequestID(owner.ResourceID, DriverRunFailed)
	port := &driverRunCascadePortStub{result: CascadeChildDriverRunsResult{
		Committed: &CascadeChildDriverRunsCommit{
			WorkspaceKey: "TEST", ParentRunID: owner.ResourceID, ParentStatus: DriverRunFailed,
			Reason: reason, ErrorClass: "parent_run_terminal", CascadedAt: now, MaxDepth: 8,
		},
		ActionID: "cascade-action",
	}}
	service, issuer := newDriverRunTestService(t, DriverRunDependencies{Cascades: port})
	live := CascadeChildDriverRunsCommand{
		WorkspaceKey: "TEST", RequestID: requestID, Owner: owner, ParentStatus: DriverRunFailed,
		Reason: reason, ErrorClass: "parent_run_terminal", CascadedAt: now, MaxDepth: 8,
	}
	if _, err := service.CascadeChildDriverRuns(context.Background(), issueExecution(t, issuer, ActionCascadeChildDriverRuns, owner), live); err != nil {
		t.Fatalf("live cascade: %v", err)
	}
	if port.command.SystemRecovery || port.command.ParentRunID != owner.ResourceID {
		t.Fatalf("live port command=%+v", port.command)
	}
	port.result.Replay = true
	recovery := RecoverChildDriverRunCascadeCommand{
		WorkspaceKey: "TEST", RequestID: requestID, ParentRunID: owner.ResourceID, ParentStatus: DriverRunFailed,
		Reason: reason, ErrorClass: "parent_run_terminal", CascadedAt: now.Add(time.Minute), MaxDepth: 8,
	}
	if _, err := service.RecoverChildDriverRunCascade(context.Background(), issueSystem(t, issuer, ActionRecoverChildDriverRunCascade), recovery); err != nil {
		t.Fatalf("recovery cascade replay: %v", err)
	}
	if !port.command.SystemRecovery || port.command.RequestID != live.RequestID || port.calls != 2 {
		t.Fatalf("recovery port command=%+v calls=%d", port.command, port.calls)
	}

	before := port.calls
	wrongStatus := recovery
	wrongStatus.ParentStatus = DriverRunRunning
	if _, err := service.RecoverChildDriverRunCascade(context.Background(), issueSystem(t, issuer, ActionRecoverChildDriverRunCascade), wrongStatus); !errors.Is(err, ErrInvalid) || port.calls != before {
		t.Fatalf("running recovery error=%v calls=%d", err, port.calls)
	}
	wrongWorkspace := recovery
	wrongWorkspace.WorkspaceKey = "OTHER"
	if _, err := service.RecoverChildDriverRunCascade(context.Background(), issueSystem(t, issuer, ActionRecoverChildDriverRunCascade), wrongWorkspace); err == nil || port.calls != before {
		t.Fatalf("wrong workspace recovery error=%v calls=%d", err, port.calls)
	}
	if _, err := service.RecoverChildDriverRunCascade(context.Background(), issueSystem(t, issuer, ActionClaimDriverRun), recovery); err == nil || port.calls != before {
		t.Fatalf("wrong component action recovery error=%v calls=%d", err, port.calls)
	}
}

func TestDriverRunWorkItemClaimAndReleaseRequireExactOwnerAndReceipts(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 123456789, time.UTC)
	owner := Owner{ResourceKind: ResourceDriverRun, ResourceID: "run-1", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "secret", FencingToken: 7}
	claimRequestID := ClaimDriverRunWorkItemRequestID(owner.ResourceID, "TASK-1")
	claimActionID := DriverRunWorkItemClaimActionID(claimRequestID)
	claimAppliedAt := now.Truncate(time.Microsecond)
	releaseAt := now.Add(time.Minute)
	releaseRequestID := ReleaseDriverRunWorkItemRequestID(owner.ResourceID, "TASK-1")
	releaseActionID := DriverRunWorkItemReleaseActionID(releaseRequestID)
	releaseAppliedAt := releaseAt.Truncate(time.Microsecond)
	port := &driverRunWorkItemPortStub{
		claimResult: DriverRunWorkItemMutationResult{
			WorkItem: &DriverRunWorkItem{WorkspaceKey: "TEST", WorkItemID: "TASK-1", Status: "in_progress", Assignee: "driver-run:run-1", UpdatedAt: claimAppliedAt},
			Action: &DriverRunWorkItemAction{
				WorkspaceKey: "TEST", ActionID: claimActionID, IdempotencyKey: claimActionID,
				ActionType: "claim_work_item", TargetRef: "TASK-1", RequestedBy: "driver-run:run-1", Status: "applied",
				RequestRef: "sha256:" + strings.Repeat("0", 64), ResponseRef: "issue://TASK-1#claimed",
				CreatedAt: claimAppliedAt, AppliedAt: &claimAppliedAt,
			},
		},
		releaseResult: DriverRunWorkItemMutationResult{
			WorkItem: &DriverRunWorkItem{WorkspaceKey: "TEST", WorkItemID: "TASK-1", Status: "open", UpdatedAt: releaseAppliedAt},
			Action: &DriverRunWorkItemAction{
				WorkspaceKey: "TEST", ActionID: releaseActionID, IdempotencyKey: releaseActionID,
				ActionType: "release_work_item", TargetRef: "TASK-1", RequestedBy: "driver-run:run-1", Status: "applied",
				RequestRef: "sha256:" + strings.Repeat("1", 64), ResponseRef: "issue://TASK-1#released",
				CreatedAt: releaseAppliedAt, AppliedAt: &releaseAppliedAt,
			},
		},
	}
	service, issuer := newDriverRunTestService(t, DriverRunDependencies{WorkItems: port})

	claim, err := service.ClaimDriverRunWorkItem(context.Background(), issueExecution(t, issuer, ActionClaimDriverRunWorkItem, owner), ClaimDriverRunWorkItemCommand{
		WorkspaceKey: "TEST", RequestID: claimRequestID, Owner: owner, WorkItemID: "TASK-1", ClaimedAt: now,
	})
	if err != nil || claim.Action == nil || claim.Action.ActionID != claimActionID || port.claimCommand.Owner != owner {
		t.Fatalf("claim=%+v command=%+v err=%v", claim, port.claimCommand, err)
	}
	released, err := service.ReleaseDriverRunWorkItem(context.Background(), issueExecution(t, issuer, ActionReleaseDriverRunWorkItem, owner), ReleaseDriverRunWorkItemCommand{
		WorkspaceKey: "TEST", RequestID: releaseRequestID, Owner: owner, WorkItemID: "TASK-1",
		ClaimActionID: claimActionID, ReleasedAt: releaseAt,
	})
	if err != nil || released.WorkItem == nil || released.WorkItem.Status != "open" || port.releaseCommand.ClaimActionID != claimActionID {
		t.Fatalf("release=%+v command=%+v err=%v", released, port.releaseCommand, err)
	}
	foreign := owner
	foreign.FencingToken++
	if _, err := service.ClaimDriverRunWorkItem(context.Background(), issueExecution(t, issuer, ActionClaimDriverRunWorkItem, foreign), ClaimDriverRunWorkItemCommand{
		WorkspaceKey: "TEST", RequestID: claimRequestID, Owner: owner, WorkItemID: "TASK-1", ClaimedAt: now,
	}); !errors.Is(err, ErrFenceConflict) || port.claimCalls != 1 {
		t.Fatalf("foreign claim err=%v calls=%d", err, port.claimCalls)
	}
}

func TestDriverRunReviewWorkItemClaimAcceptsOnlyVersionedReceiptFingerprint(t *testing.T) {
	now := time.Date(2026, 7, 23, 12, 44, 45, 841059362, time.UTC)
	appliedAt := now.Truncate(time.Microsecond)
	owner := Owner{
		ResourceKind: ResourceDriverRun, ResourceID: "run-review-1", NodeID: "node-1",
		LeaseID: "lease-1", LeaseToken: "secret", FencingToken: 7,
	}
	taskID := "TASK-REVIEW-1"
	requestID := ClaimDriverRunWorkItemRequestID(owner.ResourceID, taskID)
	actionID := DriverRunWorkItemClaimActionID(requestID)
	plainRef := "sha256:" + strings.Repeat("c", 64)
	versionedRef := driverRunWorkItemReviewClaimFingerprintPrefix + plainRef

	for _, tc := range []struct {
		name           string
		requiredStatus string
		requestRef     string
		wantConflict   bool
	}{
		{name: "review_versioned", requiredStatus: DriverRunWorkItemRestoreReview, requestRef: versionedRef},
		{name: "review_plain_rejected", requiredStatus: DriverRunWorkItemRestoreReview, requestRef: plainRef, wantConflict: true},
		{name: "open_plain", requestRef: plainRef},
		{name: "open_versioned_rejected", requestRef: versionedRef, wantConflict: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			port := &driverRunWorkItemPortStub{claimResult: DriverRunWorkItemMutationResult{
				WorkItem: &DriverRunWorkItem{
					WorkspaceKey: "TEST", WorkItemID: taskID, Status: "in_progress",
					Assignee: "driver-run:" + owner.ResourceID, UpdatedAt: appliedAt,
				},
				Action: &DriverRunWorkItemAction{
					WorkspaceKey: "TEST", ActionID: actionID, IdempotencyKey: actionID,
					ActionType: "claim_work_item", TargetRef: taskID, RequestedBy: "driver-run:" + owner.ResourceID,
					Status: "applied", RequestRef: tc.requestRef, ResponseRef: "issue://" + taskID + "#claimed",
					CreatedAt: appliedAt, AppliedAt: &appliedAt,
				},
			}}
			service, issuer := newDriverRunTestService(t, DriverRunDependencies{WorkItems: port})
			result, err := service.ClaimDriverRunWorkItem(
				context.Background(),
				issueExecution(t, issuer, ActionClaimDriverRunWorkItem, owner),
				ClaimDriverRunWorkItemCommand{
					WorkspaceKey: "TEST", RequestID: requestID, Owner: owner, WorkItemID: taskID,
					RequiredStatus: tc.requiredStatus, ClaimedAt: now,
				},
			)
			if tc.wantConflict {
				if !errors.Is(err, ErrConflict) {
					t.Fatalf("error = %v, want conflict", err)
				}
				return
			}
			if err != nil || result.WorkItem == nil || result.WorkItem.Assignee != "driver-run:"+owner.ResourceID ||
				port.claimCommand.RequiredStatus != tc.requiredStatus {
				t.Fatalf("claim=%+v command=%+v err=%v", result, port.claimCommand, err)
			}
		})
	}
}

func TestDriverRunWorkItemCommandsRejectDivergentActionAndMissingPort(t *testing.T) {
	now := time.Now().UTC()
	owner := Owner{ResourceKind: ResourceDriverRun, ResourceID: "run-1", NodeID: "node-1", LeaseID: "lease-1", LeaseToken: "secret", FencingToken: 7}
	requestID := ClaimDriverRunWorkItemRequestID(owner.ResourceID, "TASK-1")
	actionID := DriverRunWorkItemClaimActionID(requestID)
	appliedAt := now
	port := &driverRunWorkItemPortStub{claimResult: DriverRunWorkItemMutationResult{
		WorkItem: &DriverRunWorkItem{WorkspaceKey: "TEST", WorkItemID: "TASK-1", Status: "in_progress", Assignee: "driver-run:run-1", UpdatedAt: now},
		Action: &DriverRunWorkItemAction{
			WorkspaceKey: "TEST", ActionID: actionID, IdempotencyKey: actionID, ActionType: "claim_work_item",
			TargetRef: "TASK-1", RequestedBy: "driver-run:foreign", Status: "applied",
			RequestRef: "sha256:" + strings.Repeat("0", 64), ResponseRef: "issue://TASK-1#claimed", CreatedAt: now, AppliedAt: &appliedAt,
		},
	}}
	service, issuer := newDriverRunTestService(t, DriverRunDependencies{WorkItems: port})
	command := ClaimDriverRunWorkItemCommand{WorkspaceKey: "TEST", RequestID: requestID, Owner: owner, WorkItemID: "TASK-1", ClaimedAt: now}
	if _, err := service.ClaimDriverRunWorkItem(context.Background(), issueExecution(t, issuer, ActionClaimDriverRunWorkItem, owner), command); !errors.Is(err, ErrConflict) {
		t.Fatalf("divergent action error=%v, want conflict", err)
	}
	service, issuer = newDriverRunTestService(t, DriverRunDependencies{})
	if _, err := service.ClaimDriverRunWorkItem(context.Background(), issueExecution(t, issuer, ActionClaimDriverRunWorkItem, owner), command); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("missing Work Item port error=%v, want unavailable", err)
	}
}

func TestDriverRunReviewWorkItemHandoffRequiresExactOwnerChildAndReceipt(t *testing.T) {
	now := time.Date(2026, 7, 23, 5, 0, 0, 123456789, time.UTC)
	appliedAt := now.Truncate(time.Microsecond)
	owner := Owner{
		ResourceKind: ResourceDriverRun, ResourceID: "run-review-1", NodeID: "node-1",
		LeaseID: "lease-1", LeaseToken: "secret", FencingToken: 7,
	}
	taskID := "TASK-REVIEW-1"
	taskRunID := "review-child-1"
	requestID := HandoffDriverRunReviewWorkItemRequestID(owner.ResourceID, taskID, taskRunID)
	actionID := DriverRunReviewWorkItemHandoffActionID(requestID)
	claimActionID := DriverRunWorkItemClaimActionID(ClaimDriverRunWorkItemRequestID(owner.ResourceID, taskID))
	port := &driverRunWorkItemPortStub{handoffResult: DriverRunWorkItemMutationResult{
		WorkItem: &DriverRunWorkItem{
			WorkspaceKey: "TEST", WorkItemID: taskID, Status: "open", Assignee: "", UpdatedAt: appliedAt,
		},
		Action: &DriverRunWorkItemAction{
			WorkspaceKey: "TEST", ActionID: actionID, IdempotencyKey: actionID,
			ActionType: "handoff_review_work_item", TargetRef: taskID, RequestedBy: "driver-run:" + owner.ResourceID,
			Status: "applied", RequestRef: "sha256:" + strings.Repeat("2", 64),
			ResponseRef: "issue://" + taskID + "#handed-off", CreatedAt: appliedAt, AppliedAt: &appliedAt,
		},
	}}
	service, issuer := newDriverRunTestService(t, DriverRunDependencies{WorkItems: port})
	command := HandoffDriverRunReviewWorkItemCommand{
		WorkspaceKey: "TEST", RequestID: requestID, Owner: owner, WorkItemID: taskID,
		ClaimActionID: claimActionID, TaskRunID: taskRunID, TargetStatus: "open",
		Reason: "changes requested", HandedOffAt: now,
	}
	result, err := service.HandoffDriverRunReviewWorkItem(
		context.Background(),
		issueExecution(t, issuer, ActionHandoffDriverRunReviewWorkItem, owner),
		command,
	)
	if err != nil || result.WorkItem == nil || result.WorkItem.Status != "open" ||
		port.handoffCalls != 1 || port.handoffCommand.TaskRunID != taskRunID ||
		port.handoffCommand.ClaimActionID != claimActionID {
		t.Fatalf("handoff=%+v command=%+v calls=%d err=%v", result, port.handoffCommand, port.handoffCalls, err)
	}

	for name, edit := range map[string]func(*HandoffDriverRunReviewWorkItemCommand){
		"wrong child":                func(c *HandoffDriverRunReviewWorkItemCommand) { c.TaskRunID = "other" },
		"wrong claim":                func(c *HandoffDriverRunReviewWorkItemCommand) { c.ClaimActionID = "other" },
		"review missing annotations": func(c *HandoffDriverRunReviewWorkItemCommand) { c.TargetStatus = "review" },
		"unsupported target":         func(c *HandoffDriverRunReviewWorkItemCommand) { c.TargetStatus = "blocked" },
	} {
		t.Run(name, func(t *testing.T) {
			bad := command
			edit(&bad)
			if _, err := service.HandoffDriverRunReviewWorkItem(
				context.Background(),
				issueExecution(t, issuer, ActionHandoffDriverRunReviewWorkItem, owner),
				bad,
			); !errors.Is(err, ErrInvalid) {
				t.Fatalf("error=%v, want invalid", err)
			}
		})
	}
	if port.handoffCalls != 1 {
		t.Fatalf("invalid commands reached port: %d calls", port.handoffCalls)
	}
}

func TestDriverRunReviewWorkItemHandoffValidatesAtomicReviewAnnotationsAndResult(t *testing.T) {
	now := time.Date(2026, 7, 23, 5, 30, 0, 123456789, time.UTC)
	appliedAt := now.Truncate(time.Microsecond)
	owner := Owner{
		ResourceKind: ResourceDriverRun, ResourceID: "run-triage-1", NodeID: "node-1",
		LeaseID: "lease-1", LeaseToken: "secret", FencingToken: 7,
	}
	taskID := "TASK-TRIAGE-1"
	taskRunID := "triage-child-1"
	requestID := HandoffDriverRunReviewWorkItemRequestID(owner.ResourceID, taskID, taskRunID)
	actionID := DriverRunReviewWorkItemHandoffActionID(requestID)
	claimActionID := DriverRunWorkItemClaimActionID(ClaimDriverRunWorkItemRequestID(owner.ResourceID, taskID))
	priority := 4
	port := &driverRunWorkItemPortStub{handoffResult: DriverRunWorkItemMutationResult{
		WorkItem: &DriverRunWorkItem{
			WorkspaceKey: "TEST", WorkItemID: taskID, Status: "review", Priority: priority,
			Labels: []string{"existing", "bug", "triaged"}, Assignee: "", UpdatedAt: appliedAt,
		},
		Action: &DriverRunWorkItemAction{
			WorkspaceKey: "TEST", ActionID: actionID, IdempotencyKey: actionID,
			ActionType: "handoff_review_work_item", TargetRef: taskID, RequestedBy: "driver-run:" + owner.ResourceID,
			Status: "applied", RequestRef: "sha256:" + strings.Repeat("2", 64),
			ResponseRef: "issue://" + taskID + "#handed-off", CreatedAt: appliedAt, AppliedAt: &appliedAt,
		},
		Comment: &DriverRunWorkItemComment{
			CommentID: "17", WorkItemID: taskID, Author: "driver-run:" + owner.ResourceID,
			Body: "Automated bug triage completed.", CreatedAt: appliedAt,
		},
	}}
	service, issuer := newDriverRunTestService(t, DriverRunDependencies{WorkItems: port})
	command := HandoffDriverRunReviewWorkItemCommand{
		WorkspaceKey: "TEST", RequestID: requestID, Owner: owner, WorkItemID: taskID,
		ClaimActionID: claimActionID, TaskRunID: taskRunID, TargetStatus: "review",
		Priority: &priority, Labels: []string{" bug ", "triaged", "bug"},
		CommentBody: "Automated bug triage completed.", HandedOffAt: now,
	}
	result, err := service.HandoffDriverRunReviewWorkItem(
		context.Background(),
		issueExecution(t, issuer, ActionHandoffDriverRunReviewWorkItem, owner),
		command,
	)
	if err != nil || result.WorkItem == nil || result.WorkItem.Status != "review" ||
		port.handoffCalls != 1 || port.handoffCommand.Priority == nil ||
		*port.handoffCommand.Priority != priority ||
		!slices.Equal(port.handoffCommand.Labels, []string{"bug", "triaged"}) ||
		port.handoffCommand.CommentBody != command.CommentBody {
		t.Fatalf("handoff=%+v command=%+v calls=%d err=%v", result, port.handoffCommand, port.handoffCalls, err)
	}

	port.handoffResult.Replay = true
	retry := command
	retry.HandedOffAt = now.Add(time.Minute)
	if _, err := service.HandoffDriverRunReviewWorkItem(
		context.Background(),
		issueExecution(t, issuer, ActionHandoffDriverRunReviewWorkItem, owner),
		retry,
	); err != nil {
		t.Fatalf("exact replay with regenerated request timestamp failed: %v", err)
	}
	port.handoffResult.Comment.CreatedAt = appliedAt.Add(time.Second)
	if _, err := service.HandoffDriverRunReviewWorkItem(
		context.Background(),
		issueExecution(t, issuer, ActionHandoffDriverRunReviewWorkItem, owner),
		retry,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("replay with divergent comment timestamp error=%v, want conflict", err)
	}
	port.handoffResult.Comment.CreatedAt = appliedAt
	port.handoffResult.WorkItem.UpdatedAt = appliedAt.Add(time.Second)
	if _, err := service.HandoffDriverRunReviewWorkItem(
		context.Background(),
		issueExecution(t, issuer, ActionHandoffDriverRunReviewWorkItem, owner),
		retry,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("replay with divergent Work Item timestamp error=%v, want conflict", err)
	}
	port.handoffResult.WorkItem.UpdatedAt = appliedAt
	port.handoffResult.Replay = false

	port.handoffResult.WorkItem.Priority = 3
	if _, err := service.HandoffDriverRunReviewWorkItem(
		context.Background(),
		issueExecution(t, issuer, ActionHandoffDriverRunReviewWorkItem, owner),
		command,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("divergent priority error=%v, want conflict", err)
	}
	port.handoffResult.WorkItem.Priority = priority
	port.handoffResult.WorkItem.Labels = []string{"bug"}
	if _, err := service.HandoffDriverRunReviewWorkItem(
		context.Background(),
		issueExecution(t, issuer, ActionHandoffDriverRunReviewWorkItem, owner),
		command,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("missing returned label error=%v, want conflict", err)
	}
	port.handoffResult.WorkItem.Labels = []string{"existing", "bug", "triaged"}
	port.handoffResult.Comment.Body = "different"
	if _, err := service.HandoffDriverRunReviewWorkItem(
		context.Background(),
		issueExecution(t, issuer, ActionHandoffDriverRunReviewWorkItem, owner),
		command,
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("divergent comment error=%v, want conflict", err)
	}
}

func TestDriverRunReviewWorkItemHandoffRejectsInvalidReviewAnnotationsBeforePort(t *testing.T) {
	now := time.Date(2026, 7, 23, 5, 30, 0, 0, time.UTC)
	owner := Owner{
		ResourceKind: ResourceDriverRun, ResourceID: "run-triage-1", NodeID: "node-1",
		LeaseID: "lease-1", LeaseToken: "secret", FencingToken: 7,
	}
	taskID := "TASK-TRIAGE-1"
	taskRunID := "triage-child-1"
	requestID := HandoffDriverRunReviewWorkItemRequestID(owner.ResourceID, taskID, taskRunID)
	claimActionID := DriverRunWorkItemClaimActionID(ClaimDriverRunWorkItemRequestID(owner.ResourceID, taskID))
	priority := 2
	command := HandoffDriverRunReviewWorkItemCommand{
		WorkspaceKey: "TEST", RequestID: requestID, Owner: owner, WorkItemID: taskID,
		ClaimActionID: claimActionID, TaskRunID: taskRunID, TargetStatus: "review",
		Priority: &priority, CommentBody: "triaged", HandedOffAt: now,
	}
	tests := []struct {
		name string
		edit func(*HandoffDriverRunReviewWorkItemCommand)
	}{
		{"missing priority", func(c *HandoffDriverRunReviewWorkItemCommand) { c.Priority = nil }},
		{"out of range priority", func(c *HandoffDriverRunReviewWorkItemCommand) {
			value := 5
			c.Priority = &value
		}},
		{"blank comment", func(c *HandoffDriverRunReviewWorkItemCommand) { c.CommentBody = "   " }},
		{"oversized comment", func(c *HandoffDriverRunReviewWorkItemCommand) {
			c.CommentBody = strings.Repeat("x", DriverRunReviewWorkItemMaxCommentBytes+1)
		}},
		{"too many labels", func(c *HandoffDriverRunReviewWorkItemCommand) {
			c.Labels = []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}
		}},
		{"invalid label", func(c *HandoffDriverRunReviewWorkItemCommand) { c.Labels = []string{"bad,label"} }},
		{"open annotations", func(c *HandoffDriverRunReviewWorkItemCommand) { c.TargetStatus = "open" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			port := &driverRunWorkItemPortStub{}
			service, issuer := newDriverRunTestService(t, DriverRunDependencies{WorkItems: port})
			bad := command
			tc.edit(&bad)
			if _, err := service.HandoffDriverRunReviewWorkItem(
				context.Background(),
				issueExecution(t, issuer, ActionHandoffDriverRunReviewWorkItem, owner),
				bad,
			); !errors.Is(err, ErrInvalid) {
				t.Fatalf("error=%v, want invalid", err)
			}
			if port.handoffCalls != 0 {
				t.Fatalf("invalid command reached port: %+v", port.handoffCommand)
			}
		})
	}
}

func TestDriverRunWorkItemRequestIDsAreDeterministicAndBounded(t *testing.T) {
	for _, derive := range []func(string, string) string{ClaimDriverRunWorkItemRequestID, ReleaseDriverRunWorkItemRequestID} {
		short := derive("run-1", "TASK-1")
		if short != derive("run-1", "TASK-1") || len(short) > driverRunWorkItemRequestIDMaxLength {
			t.Fatalf("short identity = %q", short)
		}
		long := derive(strings.Repeat("r", 200), strings.Repeat("t", 200))
		if long != derive(strings.Repeat("r", 200), strings.Repeat("t", 200)) || len(long) > driverRunWorkItemRequestIDMaxLength {
			t.Fatalf("long identity length=%d value=%q", len(long), long)
		}
		if left, right := derive("a:b", "c"), derive("a", "b:c"); left == right {
			t.Fatalf("delimiter collision: %q", left)
		}
	}
	handoff := HandoffDriverRunReviewWorkItemRequestID("run-1", "TASK-1", "task-run-1")
	if handoff != HandoffDriverRunReviewWorkItemRequestID("run-1", "TASK-1", "task-run-1") ||
		len(handoff) > driverRunWorkItemRequestIDMaxLength ||
		handoff == HandoffDriverRunReviewWorkItemRequestID("run-1", "TASK-1", "task-run-2") {
		t.Fatalf("handoff identity = %q", handoff)
	}
}

func newDriverRunTestService(t *testing.T, dependencies DriverRunDependencies) (*Service, *authority.Issuer) {
	t.Helper()
	issuer := authority.NewIssuer()
	rules := append(OperationRules(), DriverRunOperationRules()...)
	admission, err := issuer.NewAdmission(rules...)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(Dependencies{DriverRuns: dependencies}, admission)
	if err != nil {
		t.Fatal(err)
	}
	return service, issuer
}
