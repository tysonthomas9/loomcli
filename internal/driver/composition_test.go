//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// newCompositionRun is newAwaitOpRun plus an activated driver version, so
// workflows/start can resolve "driver-1" as a startable workflow.
func newCompositionRun(t *testing.T) (*memstore.Store, *domain.DriverRun) {
	t.Helper()
	st, parent := newAwaitOpRun(t)
	active := "version-1"
	if _, err := st.Drivers().Update(context.Background(), "WS", "driver-1",
		store.DriverUpdate{ActiveVersionID: &active}); err != nil {
		t.Fatalf("activate driver version: %v", err)
	}
	return st, parent
}

func startOpts(parentRunID, idempotencyKey string, startIndex int) StartChildWorkflowOptions {
	return StartChildWorkflowOptions{
		WorkspaceKey:   "WS",
		ParentRunID:    parentRunID,
		WorkflowName:   "driver-1",
		IdempotencyKey: idempotencyKey,
		StartIndex:     startIndex,
	}
}

func awaitChildOpts(parent *domain.DriverRun, childRunID string, awaitIndex int) AwaitChildWorkflowOptions {
	return AwaitChildWorkflowOptions{
		WorkspaceKey: "WS",
		RunID:        parent.RunID,
		NodeID:       parent.NodeID,
		LeaseID:      parent.LeaseID,
		FencingToken: parent.FencingToken,
		ChildRunID:   childRunID,
		TimeoutMs:    time.Minute.Milliseconds(),
		AwaitIndex:   awaitIndex,
	}
}

// finishRunAs claims a queued run and drives it through the executor finish
// path (Finish + run.finished emission + cancel cascade).
func finishRunAs(t *testing.T, st *memstore.Store, runID string, status domain.DriverRunStatus) *domain.DriverRun {
	t.Helper()
	ctx := context.Background()
	claimed, err := st.DriverRuns().Claim(ctx, "WS", runID, "node-fin", "lease-fin")
	if err != nil {
		t.Fatalf("Claim %s: %v", runID, err)
	}
	final, err := (&Executor{Store: st, WorkspaceKey: "WS"}).finish(ctx, claimed,
		RunResult{Status: status, Summary: "finished as " + string(status), ErrorClass: errorClassFor(status)})
	if err != nil {
		t.Fatalf("finish %s: %v", runID, err)
	}
	return final
}

func errorClassFor(status domain.DriverRunStatus) string {
	if status == domain.DriverRunFailed {
		return "boom"
	}
	return ""
}

func countChildRuns(t *testing.T, st *memstore.Store, parentRunID string) int {
	t.Helper()
	runs, err := st.DriverRuns().List(context.Background(), "WS", store.DriverRunFilter{})
	if err != nil {
		t.Fatalf("List runs: %v", err)
	}
	n := 0
	for _, run := range runs {
		if run.ParentRunID == parentRunID {
			n++
		}
	}
	return n
}

func TestStartChildWorkflowDeterministicIdempotent(t *testing.T) {
	ctx := context.Background()
	st, parent := newCompositionRun(t)

	first, err := StartChildWorkflow(ctx, st, startOpts(parent.RunID, "deploy-staging", 0))
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	if first.RunID != ChildWorkflowRunID(parent.RunID, "deploy-staging") {
		t.Fatalf("child run id = %q, want deterministic id", first.RunID)
	}
	if first.ParentRunID != parent.RunID || first.SourceKind != ChildRunSourceKind ||
		first.SourceRef != parent.RunID || first.Status != domain.DriverRunQueued {
		t.Fatalf("child = %+v, want queued workflow child of %s", first, parent.RunID)
	}
	if first.EpicID != "" {
		t.Fatalf("child EpicID = %q, want empty (orthogonal to ParentRunID)", first.EpicID)
	}

	// Re-entry replay: the same key returns the SAME child, no duplicate.
	second, err := StartChildWorkflow(ctx, st, startOpts(parent.RunID, "deploy-staging", 0))
	if err != nil {
		t.Fatalf("replayed start: %v", err)
	}
	if second.RunID != first.RunID {
		t.Fatalf("replayed child = %q, want %q", second.RunID, first.RunID)
	}
	if got := countChildRuns(t, st, parent.RunID); got != 1 {
		t.Fatalf("children = %d, want 1 after replay", got)
	}

	// Distinct steps yield distinct children.
	byIndex1, err := StartChildWorkflow(ctx, st, startOpts(parent.RunID, "", 1))
	if err != nil {
		t.Fatalf("start by index 1: %v", err)
	}
	byIndex2, err := StartChildWorkflow(ctx, st, startOpts(parent.RunID, "", 2))
	if err != nil {
		t.Fatalf("start by index 2: %v", err)
	}
	ids := map[string]bool{first.RunID: true, byIndex1.RunID: true, byIndex2.RunID: true}
	if len(ids) != 3 {
		t.Fatalf("expected three distinct children, got %v", ids)
	}
	if got := countChildRuns(t, st, parent.RunID); got != 3 {
		t.Fatalf("children = %d, want 3", got)
	}
}

func TestStartChildWorkflowValidation(t *testing.T) {
	ctx := context.Background()
	st, parent := newCompositionRun(t)
	cases := []struct {
		name string
		opts StartChildWorkflowOptions
		want error
	}{
		{"missing key and index", startOpts(parent.RunID, "", 0), domain.ErrInvalid},
		{"negative index", startOpts(parent.RunID, "", -1), domain.ErrInvalid},
		{"missing workflow name", StartChildWorkflowOptions{WorkspaceKey: "WS", ParentRunID: parent.RunID, IdempotencyKey: "k"}, domain.ErrInvalid},
		{"unknown workflow", StartChildWorkflowOptions{WorkspaceKey: "WS", ParentRunID: parent.RunID, WorkflowName: "nope", IdempotencyKey: "k"}, domain.ErrNotFound},
		{"invalid input JSON", func() StartChildWorkflowOptions {
			o := startOpts(parent.RunID, "k", 0)
			o.Input = json.RawMessage(`{"broken"`)
			return o
		}(), domain.ErrInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := StartChildWorkflow(ctx, st, tc.opts); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestStartChildWorkflowDepthCap proves the composition depth guard: with a
// cap of 2, a depth-2 grandchild is admitted and its own child is refused.
func TestStartChildWorkflowDepthCap(t *testing.T) {
	ctx := context.Background()
	st, parent := newCompositionRun(t)
	withCap := func(o StartChildWorkflowOptions) StartChildWorkflowOptions {
		o.MaxDepth = 2
		return o
	}

	child, err := StartChildWorkflow(ctx, st, withCap(startOpts(parent.RunID, "c", 0)))
	if err != nil {
		t.Fatalf("start depth-1 child: %v", err)
	}
	grandchild, err := StartChildWorkflow(ctx, st, withCap(startOpts(child.RunID, "g", 0)))
	if err != nil {
		t.Fatalf("start depth-2 grandchild: %v", err)
	}
	_, err = StartChildWorkflow(ctx, st, withCap(startOpts(grandchild.RunID, "gg", 0)))
	if !errors.Is(err, domain.ErrCompositionDepthExceeded) || !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("depth-3 start err = %v, want ErrCompositionDepthExceeded wrapping ErrInvalid", err)
	}
}

func TestAwaitChildWorkflowRejectsNonChild(t *testing.T) {
	ctx := context.Background()
	st, parent := newCompositionRun(t)

	// A detached run (no ParentRunID) is not awaitable.
	if _, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: "WS", RunID: "run-detached", DriverID: "driver-1", DriverVersionID: "version-1",
	}); err != nil {
		t.Fatalf("Create detached run: %v", err)
	}
	if _, _, err := AwaitChildWorkflow(ctx, st, awaitChildOpts(parent, "run-detached", 1)); !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("await detached run err = %v, want ErrNotOwner", err)
	}

	// Someone else's child is not awaitable either.
	other, err := StartChildWorkflow(ctx, st, startOpts("run-other-parent", "k", 0))
	if err != nil {
		t.Fatalf("start other parent's child: %v", err)
	}
	if _, _, err := AwaitChildWorkflow(ctx, st, awaitChildOpts(parent, other.RunID, 1)); !errors.Is(err, domain.ErrNotOwner) {
		t.Fatalf("await foreign child err = %v, want ErrNotOwner", err)
	}

	// Unknown child is not found; empty child is invalid.
	if _, _, err := AwaitChildWorkflow(ctx, st, awaitChildOpts(parent, "run-missing", 1)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("await missing child err = %v, want ErrNotFound", err)
	}
	if _, _, err := AwaitChildWorkflow(ctx, st, awaitChildOpts(parent, " ", 1)); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("await empty child err = %v, want ErrInvalid", err)
	}
}

func TestAwaitChildWorkflowRejectsForgedSatisfiedReplay(t *testing.T) {
	seedForgedWinner := func(t *testing.T, st *memstore.Store, parent, child *domain.DriverRun, eventID string) {
		t.Helper()
		instanceKey := domain.AwaitInstanceKey(parent.RunID, 1)
		result, err := st.Awaits().RegisterAwaitAndCheck(t.Context(), "WS", store.AwaitRegistration{
			InstanceKey: instanceKey, RunID: parent.RunID, Pattern: RunFinishedSubjectKey(child.RunID),
			ActorAllow: []string{RunFinishedActor}, Deadline: time.Now().Add(time.Hour),
		})
		if err != nil || result.Satisfied {
			t.Fatalf("register forged-winner await = %+v, %v; want pending", result, err)
		}
		if _, err := st.Awaits().ResolveAwait(t.Context(), "WS", instanceKey, eventID,
			json.RawMessage(`{"runId":"forged","status":"completed"}`), RunFinishedActor); err != nil {
			t.Fatalf("seed forged satisfied await: %v", err)
		}
	}

	t.Run("nonterminal child", func(t *testing.T) {
		st, parent := newCompositionRun(t)
		child, err := StartChildWorkflow(t.Context(), st, startOpts(parent.RunID, "nonterminal", 0))
		if err != nil {
			t.Fatal(err)
		}
		seedForgedWinner(t, st, parent, child, RunFinishedEventID(child.RunID, domain.DriverRunCompleted))
		if _, _, err := AwaitChildWorkflow(t.Context(), st, awaitChildOpts(parent, child.RunID, 1)); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("AwaitChildWorkflow err = %v, want conflict for satisfied nonterminal child", err)
		}
	})

	t.Run("terminal child event identity mismatch", func(t *testing.T) {
		st, parent := newCompositionRun(t)
		child, err := StartChildWorkflow(t.Context(), st, startOpts(parent.RunID, "terminal-mismatch", 0))
		if err != nil {
			t.Fatal(err)
		}
		forgedID := RunFinishedEventID(child.RunID, domain.DriverRunFailed)
		seedForgedWinner(t, st, parent, child, forgedID)
		finishRunAs(t, st, child.RunID, domain.DriverRunCompleted)
		if _, _, err := AwaitChildWorkflow(t.Context(), st, awaitChildOpts(parent, child.RunID, 1)); !errors.Is(err, domain.ErrConflict) {
			t.Fatalf("AwaitChildWorkflow err = %v, want conflict for winner %s", err, forgedID)
		}
	})
}

// TestAwaitChildWorkflowAlreadyTerminalChild is the RULE 2 composition proof:
// a child that terminated BEFORE the parent awaited resolves inline from its
// durable terminal state — no listener, event-journal appender, or lost
// wakeup; the parent stays running.
func TestAwaitChildWorkflowAlreadyTerminalChild(t *testing.T) {
	ctx := context.Background()
	st, parent := newCompositionRun(t)

	child, err := StartChildWorkflow(ctx, st, startOpts(parent.RunID, "fast-child", 0))
	if err != nil {
		t.Fatalf("start child: %v", err)
	}
	finishRunAs(t, st, child.RunID, domain.DriverRunCompleted)

	outcome, gotChild, err := AwaitChildWorkflow(ctx, st, awaitChildOpts(parent, child.RunID, 1))
	if err != nil {
		t.Fatalf("AwaitChildWorkflow: %v", err)
	}
	if outcome.Status != string(domain.AwaitSatisfied) || gotChild.RunID != child.RunID {
		t.Fatalf("outcome = %s on %s, want satisfied on %s", outcome.Status, gotChild.RunID, child.RunID)
	}
	wantEvent := RunFinishedEventID(child.RunID, domain.DriverRunCompleted)
	if outcome.Instance.SatisfiedByEventID != wantEvent {
		t.Fatalf("satisfied by %q, want %q", outcome.Instance.SatisfiedByEventID, wantEvent)
	}
	run, err := st.DriverRuns().Get(ctx, "WS", parent.RunID)
	if err != nil || run.Status != domain.DriverRunRunning {
		t.Fatalf("parent = %+v, %v; want still running", run, err)
	}
}

// TestAwaitChildWorkflowConcurrentFinishNoLostWakeup exercises the boundary
// ordering the striped Execution outcome lock protects: registration and the
// child terminal transition begin together. Regardless of which wins, the
// deterministic outcome lands on the await and the parent cannot remain
// stranded suspended.
func TestAwaitChildWorkflowConcurrentFinishNoLostWakeup(t *testing.T) {
	for iteration := 0; iteration < 20; iteration++ {
		st, parent := newCompositionRun(t)
		child, err := StartChildWorkflow(t.Context(), st, startOpts(parent.RunID, "racing-child", 0))
		if err != nil {
			t.Fatal(err)
		}
		claimed, err := st.DriverRuns().Claim(t.Context(), "WS", child.RunID, "node-child", "lease-child")
		if err != nil {
			t.Fatal(err)
		}

		start := make(chan struct{})
		finishErr := make(chan error, 1)
		awaitResult := make(chan struct {
			outcome *AwaitEventOutcome
			err     error
		}, 1)
		go func() {
			<-start
			_, finishError := (&Executor{Store: st, WorkspaceKey: "WS"}).finish(t.Context(), claimed,
				RunResult{Status: domain.DriverRunCompleted, Summary: "done"})
			finishErr <- finishError
		}()
		go func() {
			<-start
			outcome, _, awaitErr := AwaitChildWorkflow(t.Context(), st, awaitChildOpts(parent, child.RunID, 1))
			awaitResult <- struct {
				outcome *AwaitEventOutcome
				err     error
			}{outcome: outcome, err: awaitErr}
		}()
		close(start)
		if err := <-finishErr; err != nil {
			t.Fatalf("iteration %d finish: %v", iteration, err)
		}
		awaited := <-awaitResult
		if awaited.err != nil || awaited.outcome == nil {
			t.Fatalf("iteration %d await = %+v, %v", iteration, awaited.outcome, awaited.err)
		}

		eventID := RunFinishedEventID(child.RunID, domain.DriverRunCompleted)
		instance, err := st.Awaits().GetSatisfiedAwait(t.Context(), "WS", domain.AwaitInstanceKey(parent.RunID, 1))
		if err != nil || instance.SatisfiedByEventID != eventID {
			t.Fatalf("iteration %d satisfied = %+v, %v; want %s", iteration, instance, err, eventID)
		}
		storedParent, err := st.DriverRuns().Get(t.Context(), "WS", parent.RunID)
		if err != nil || storedParent.Status == domain.DriverRunSuspendedAwaitingEvent {
			t.Fatalf("iteration %d parent = %+v, %v; lost wakeup", iteration, storedParent, err)
		}
	}
}

// TestAwaitChildWorkflowSuspendResumeReplay drives the full composition
// cycle: parent suspends on the child, the child's terminal transition resumes
// it with the lifecycle payload, and re-entry replays the satisfied await
// without re-starting the child.
func TestAwaitChildWorkflowSuspendResumeReplay(t *testing.T) {
	ctx := context.Background()
	st, parent := newCompositionRun(t)

	child, err := StartChildWorkflow(ctx, st, startOpts(parent.RunID, "slow-child", 0))
	if err != nil {
		t.Fatalf("start child: %v", err)
	}
	outcome, _, err := AwaitChildWorkflow(ctx, st, awaitChildOpts(parent, child.RunID, 1))
	if err != nil || outcome.Status != AwaitOutcomeSuspended {
		t.Fatalf("first await = %+v, %v; want suspended", outcome, err)
	}
	if outcome.Instance == nil || len(outcome.Instance.ActorAllow) != 1 ||
		outcome.Instance.ActorAllow[0] != RunFinishedActor {
		t.Fatalf("composition actor allow = %+v, want only %q", outcome.Instance, RunFinishedActor)
	}
	suspended, err := st.DriverRuns().Get(ctx, "WS", parent.RunID)
	if err != nil || suspended.Status != domain.DriverRunSuspendedAwaitingEvent {
		t.Fatalf("parent = %+v, %v; want suspended_awaiting_event", suspended, err)
	}

	// Child finishes while the parent is suspended: the dispatch-time matcher
	// resolves the await and re-queues the parent.
	finishRunAs(t, st, child.RunID, domain.DriverRunFailed)
	eventID := RunFinishedEventID(child.RunID, domain.DriverRunFailed)
	resumed, err := st.DriverRuns().Get(ctx, "WS", parent.RunID)
	if err != nil || resumed.Status != domain.DriverRunQueued || resumed.ResumeSourceEventID != eventID {
		t.Fatalf("parent = %+v, %v; want queued by %s", resumed, err, eventID)
	}

	// Re-entry: fresh claim, the same start replays the same child (no
	// duplicate), the same awaitIndex replays the terminal payload inline.
	reclaimed, err := st.DriverRuns().Claim(ctx, "WS", parent.RunID, "node-2", "lease-2")
	if err != nil {
		t.Fatalf("re-claim parent: %v", err)
	}
	replayedChild, err := StartChildWorkflow(ctx, st, startOpts(parent.RunID, "slow-child", 0))
	if err != nil || replayedChild.RunID != child.RunID {
		t.Fatalf("replayed start = %+v, %v; want existing child %s", replayedChild, err, child.RunID)
	}
	if got := countChildRuns(t, st, parent.RunID); got != 1 {
		t.Fatalf("children after re-entry = %d, want 1", got)
	}
	replay, _, err := AwaitChildWorkflow(ctx, st, awaitChildOpts(reclaimed, child.RunID, 1))
	if err != nil || replay.Status != string(domain.AwaitSatisfied) {
		t.Fatalf("replayed await = %+v, %v; want satisfied", replay, err)
	}
	var payload struct {
		RunID      string `json:"runId"`
		Status     string `json:"status"`
		ErrorClass string `json:"errorClass"`
	}
	if err := json.Unmarshal(replay.Instance.SatisfiedPayload, &payload); err != nil {
		t.Fatalf("decode payload %s: %v", replay.Instance.SatisfiedPayload, err)
	}
	if payload.RunID != child.RunID || payload.Status != string(domain.DriverRunFailed) || payload.ErrorClass != "boom" {
		t.Fatalf("payload = %+v, want child failure outcome", payload)
	}
}

// TestCascadeCancelChildren proves the locked cascade decision: queued
// children cancel (recursively, each with its own run.finished), running
// children get a cooperative cancel request, detached runs are untouched.
func TestCascadeCancelChildren(t *testing.T) {
	ctx := context.Background()
	st, parent := newCompositionRun(t)

	queuedChild, err := StartChildWorkflow(ctx, st, startOpts(parent.RunID, "queued-child", 0))
	if err != nil {
		t.Fatalf("start queued child: %v", err)
	}
	grandchild, err := StartChildWorkflow(ctx, st, startOpts(queuedChild.RunID, "grandchild", 0))
	if err != nil {
		t.Fatalf("start grandchild: %v", err)
	}
	runningChild, err := StartChildWorkflow(ctx, st, startOpts(parent.RunID, "running-child", 0))
	if err != nil {
		t.Fatalf("start running child: %v", err)
	}
	if _, err := st.DriverRuns().Claim(ctx, "WS", runningChild.RunID, "node-rc", "lease-rc"); err != nil {
		t.Fatalf("claim running child: %v", err)
	}
	if _, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: "WS", RunID: "run-detached", DriverID: "driver-1", DriverVersionID: "version-1",
	}); err != nil {
		t.Fatalf("create detached run: %v", err)
	}

	// Parent terminalizes through the executor finish path.
	publisher := &recordingRunOutcomePublisher{}
	final, err := (&Executor{Store: st, WorkspaceKey: "WS", RunOutcomes: publisher}).finish(ctx, parent,
		RunResult{Status: domain.DriverRunFailed, Summary: "parent failed"})
	if err != nil || final.Status != domain.DriverRunFailed {
		t.Fatalf("finish parent = %+v, %v", final, err)
	}

	assertRunState(t, st, queuedChild.RunID, domain.DriverRunCancelled, CancelErrorClassParentTerminal)
	assertRunState(t, st, grandchild.RunID, domain.DriverRunCancelled, CancelErrorClassParentTerminal)
	wantOutcomes := map[string]bool{
		RunFinishedEventID(parent.RunID, domain.DriverRunFailed):         false,
		RunFinishedEventID(queuedChild.RunID, domain.DriverRunCancelled): false,
		RunFinishedEventID(grandchild.RunID, domain.DriverRunCancelled):  false,
	}
	for _, outcome := range publisher.snapshot() {
		if _, wanted := wantOutcomes[outcome.EventID]; wanted {
			wantOutcomes[outcome.EventID] = true
		}
	}
	for eventID, published := range wantOutcomes {
		if !published {
			t.Fatalf("missing cascaded outcome %s; got %+v", eventID, publisher.snapshot())
		}
	}
	running, err := st.DriverRuns().Get(ctx, "WS", runningChild.RunID)
	if err != nil || running.Status != domain.DriverRunRunning {
		t.Fatalf("running child = %+v, %v; want still running", running, err)
	}
	if running.CancelRequestedAt == nil || running.CancelRequestedReason == "" {
		t.Fatalf("running child = %+v, want cancel requested", running)
	}
	detached, err := st.DriverRuns().Get(ctx, "WS", "run-detached")
	if err != nil || detached.Status != domain.DriverRunQueued || detached.CancelRequestedAt != nil {
		t.Fatalf("detached run = %+v, %v; want untouched queued", detached, err)
	}
}

func assertRunState(t *testing.T, st *memstore.Store, runID string, status domain.DriverRunStatus, errorClass string) {
	t.Helper()
	run, err := st.DriverRuns().Get(context.Background(), "WS", runID)
	if err != nil {
		t.Fatalf("Get %s: %v", runID, err)
	}
	if run.Status != status || run.ErrorClass != errorClass {
		t.Fatalf("run %s = %s/%s, want %s/%s", runID, run.Status, run.ErrorClass, status, errorClass)
	}
}

// blockingRunner suspends until its context is cancelled — the cooperative
// cancel-request observation target.
type blockingRunner struct {
	started chan struct{}
}

func (r *blockingRunner) Run(ctx context.Context, _ RunRequest) (RunResult, error) {
	close(r.started)
	<-ctx.Done()
	return RunResult{}, ctx.Err()
}

// TestExecutorObservesCancelRequest proves the running-children leg end to
// end: RequestCancel stamps the marker, the executor heartbeat observes it,
// cancels the runner, and the run terminalizes as cancelled.
func TestExecutorObservesCancelRequest(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st := newRunEventsStore(t)
	writeFlueDist(t, root, "epic-runner", "done")
	registered, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{
		WorkspaceKey: "TEST", WorkDir: root, DistPath: "dist",
		DriverName: "epic-runner", CreatedBy: "tester", Activate: true,
	})
	if err != nil {
		t.Fatalf("RegisterFlueDriver: %v", err)
	}
	if _, err := CreateDriverRun(ctx, st, RunOptions{WorkspaceKey: "TEST", DriverID: registered.Driver.DriverID, RunID: "run-cr"}); err != nil {
		t.Fatalf("CreateDriverRun: %v", err)
	}

	runner := &blockingRunner{started: make(chan struct{})}
	exec := &Executor{
		Store: st, WorkspaceKey: "TEST", WorkDir: root,
		NodeID: "node-1", LeaseID: "lease-1", Runner: runner,
		HeartbeatInterval: 5 * time.Millisecond,
	}
	done := make(chan error, 1)
	var result *ExecutionResult
	go func() {
		var runErr error
		result, runErr = exec.RunOnce(ctx)
		done <- runErr
	}()

	<-runner.started
	canceller := st.DriverRuns().(store.DriverRunCancelSupport)
	if _, err := canceller.RequestCancel(ctx, "TEST", "run-cr", "parent terminal"); err != nil {
		t.Fatalf("RequestCancel: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunOnce: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("executor did not observe the cancel request")
	}
	if result.Final == nil || result.Final.Status != domain.DriverRunCancelled || result.Final.ErrorClass != "driver_cancelled" {
		t.Fatalf("final = %+v, want cancelled/driver_cancelled", result.Final)
	}
}
