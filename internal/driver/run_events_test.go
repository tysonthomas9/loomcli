//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type recordingRunOutcomePublisher struct {
	mu       sync.Mutex
	outcomes []RunOutcome
	err      error
}

func (publisher *recordingRunOutcomePublisher) PublishRunOutcome(_ context.Context, outcome RunOutcome) error {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	outcome.Payload = append(json.RawMessage(nil), outcome.Payload...)
	publisher.outcomes = append(publisher.outcomes, outcome)
	return publisher.err
}

func (publisher *recordingRunOutcomePublisher) snapshot() []RunOutcome {
	publisher.mu.Lock()
	defer publisher.mu.Unlock()
	return append([]RunOutcome(nil), publisher.outcomes...)
}

func newRunEventsStore(t *testing.T) *memstore.Store {
	t.Helper()
	st := memstore.New()
	if _, err := st.Workspaces().Create(t.Context(), store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	return st
}

// storeWithoutRunOutcomeCapability models a legacy Store implementation. The
// concrete DriverRunStore is deliberately hidden behind a wrapper whose method
// set does not include DriverRunOutcomeStore, so direct publication remains
// covered without accidentally exercising the durable path on synthetic runs.
type storeWithoutRunOutcomeCapability struct {
	store.Store
	runs store.DriverRunStore
}

type driverRunsWithoutOutcomeCapability struct {
	store.DriverRunStore
}

func withoutRunOutcomeCapability(st store.Store) store.Store {
	return &storeWithoutRunOutcomeCapability{
		Store: st,
		runs:  &driverRunsWithoutOutcomeCapability{DriverRunStore: st.DriverRuns()},
	}
}

func (st *storeWithoutRunOutcomeCapability) DriverRuns() store.DriverRunStore {
	return st.runs
}

func seedRunFinishedBinding(t *testing.T, st *memstore.Store) {
	t.Helper()
	ctx := t.Context()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "TEST", DriverID: "composer", Name: "composer",
		OwnerType: domain.DriverOwnerSystem, Status: domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "TEST", VersionID: "v1", DriverID: "composer", Version: 1,
		SourceDigest: "sha256:s", BundleDigest: "sha256:b", ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	if _, err := st.TriggerBindings().Create(ctx, store.TriggerBindingCreate{
		WorkspaceKey: "TEST", BindingID: "b-run-finished", Name: "b-run-finished",
		SourceKind: "internal", RouteKey: "internal." + RunFinishedEventType,
		DriverID: "composer", DriverVersionID: "v1", TargetEntrypoint: "run",
		ConcurrencyPolicy: domain.TriggerBindingConcurrencyAllow, Enabled: true,
	}); err != nil {
		t.Fatalf("Create trigger binding: %v", err)
	}
}

func journalParentEvent(t *testing.T, st *memstore.Store, eventID string, hopDepth int) {
	t.Helper()
	appender := st.TriggerEvents().(store.TriggerEventAppender)
	now := time.Now().UTC()
	if _, err := appender.AppendTriggerEvent(t.Context(), &domain.TriggerEvent{
		WorkspaceKey: "TEST", EventID: eventID, SourceKind: "internal",
		EventType: "issue.created", SubjectRef: "issue#1",
		Origin: domain.TriggerEventOriginWorkflow, HopDepth: hopDepth,
		OccurredAt: now, ReceivedAt: now, IdempotencyKey: "idem-" + eventID,
	}); err != nil {
		t.Fatalf("AppendTriggerEvent(%s): %v", eventID, err)
	}
}

func terminalRun(runID string, status domain.DriverRunStatus) *domain.DriverRun {
	return &domain.DriverRun{
		WorkspaceKey: "TEST", RunID: runID, DriverID: "composer", Status: status,
		Summary: "summary for " + runID, EpicID: "TEST-1", ParentRunID: "run-parent",
	}
}

func TestEmitRunFinishedEventPublishesTerminalOutcome(t *testing.T) {
	for _, status := range []domain.DriverRunStatus{
		domain.DriverRunCompleted,
		domain.DriverRunFailed,
		domain.DriverRunNeedsReview,
		domain.DriverRunCancelled,
	} {
		t.Run(string(status), func(t *testing.T) {
			st := newRunEventsStore(t)
			publisher := &recordingRunOutcomePublisher{}
			emitRunFinishedEvent(t.Context(), withoutRunOutcomeCapability(st), publisher, terminalRun("run-child", status))

			got := publisher.snapshot()
			if len(got) != 1 {
				t.Fatalf("published outcomes = %d, want 1", len(got))
			}
			outcome := got[0]
			if outcome.WorkspaceKey != "TEST" || outcome.EventID != RunFinishedEventID("run-child", status) ||
				outcome.EventType != RunFinishedEventType || outcome.RunID != "run-child" ||
				outcome.Status != status || outcome.ActorRef != RunFinishedActor || outcome.EpicID != "TEST-1" {
				t.Fatalf("outcome = %+v", outcome)
			}
			var payload runFinishedPayload
			if err := json.Unmarshal(outcome.Payload, &payload); err != nil || payload.RunID != "run-child" || payload.Status != string(status) {
				t.Fatalf("payload = %+v, %v", payload, err)
			}
			if _, err := st.TriggerEvents().Get(t.Context(), "TEST", outcome.EventID); !errors.Is(err, domain.ErrNotFound) {
				t.Fatalf("driver wrote Automation event directly: %v", err)
			}
		})
	}
}

func TestEmitRunFinishedEventDeterministicReemission(t *testing.T) {
	st := newRunEventsStore(t)
	publisher := &recordingRunOutcomePublisher{}
	run := terminalRun("run-child", domain.DriverRunCompleted)
	legacy := withoutRunOutcomeCapability(st)
	emitRunFinishedEvent(t.Context(), legacy, publisher, run)
	emitRunFinishedEvent(t.Context(), legacy, publisher, run)
	got := publisher.snapshot()
	if len(got) != 2 || got[0].EventID != got[1].EventID || got[0].EventID != "run-finished:run-child:completed" {
		t.Fatalf("reemitted outcomes = %+v", got)
	}
}

func TestRunFinishedEventIDBoundsOpaqueRunIDs(t *testing.T) {
	runID := " run/1 " + strings.Repeat("x", 300)
	first := RunFinishedEventID(runID, domain.DriverRunCompleted)
	second := RunFinishedEventID(runID, domain.DriverRunCompleted)
	failed := RunFinishedEventID(runID, domain.DriverRunFailed)
	if first != second || first == failed || len(first) > maxRunFinishedEventIDLength {
		t.Fatalf("bounded IDs: first=%q second=%q failed=%q", first, second, failed)
	}
	if first == "run-finished:"+runID+":completed" || !strings.HasPrefix(first, "run-finished:h:") {
		t.Fatalf("long opaque ID was not hashed: %q", first)
	}
	if got := RunFinishedEventID("run-1", domain.DriverRunCompleted); got != "run-finished:run-1:completed" {
		t.Fatalf("short legacy ID = %q", got)
	}
	for _, unsafe := range []string{"run 1", "run\n1", "rún-1"} {
		if got := RunFinishedEventID(unsafe, domain.DriverRunCompleted); !strings.HasPrefix(got, "run-finished:h:") {
			t.Fatalf("unsafe opaque run ID %q produced %q", unsafe, got)
		}
	}
}

func TestEmitRunFinishedEventIgnoresNonTerminal(t *testing.T) {
	st := newRunEventsStore(t)
	publisher := &recordingRunOutcomePublisher{}
	for _, status := range []domain.DriverRunStatus{
		domain.DriverRunQueued, domain.DriverRunRunning, domain.DriverRunSuspendedAwaitingEvent,
	} {
		emitRunFinishedEvent(t.Context(), st, publisher, terminalRun("run-child", status))
	}
	if got := publisher.snapshot(); len(got) != 0 {
		t.Fatalf("published non-terminal outcomes = %+v", got)
	}
}

func TestEmitRunFinishedEventDerivesParentProvenance(t *testing.T) {
	for _, tc := range []struct {
		name, sourceRef, wantParent string
		journalParent               bool
	}{
		{name: "durable admitting event", sourceRef: "evt-parent", wantParent: "evt-parent", journalParent: true},
		{name: "unresolvable source is root", sourceRef: "loom driver run"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newRunEventsStore(t)
			if tc.journalParent {
				journalParentEvent(t, st, tc.sourceRef, 2)
			}
			publisher := &recordingRunOutcomePublisher{}
			run := terminalRun("run-child", domain.DriverRunCompleted)
			run.SourceRef = tc.sourceRef
			emitRunFinishedEvent(t.Context(), withoutRunOutcomeCapability(st), publisher, run)
			if got := publisher.snapshot(); len(got) != 1 || got[0].ParentEventID != tc.wantParent {
				t.Fatalf("outcomes = %+v, want parent %q", got, tc.wantParent)
			}
		})
	}
}

func TestEmitRunFinishedEventDoesNotUseLegacyTriggerRoutes(t *testing.T) {
	st := newRunEventsStore(t)
	seedRunFinishedBinding(t, st)
	publisher := &recordingRunOutcomePublisher{}
	emitRunFinishedEvent(t.Context(), withoutRunOutcomeCapability(st), publisher, terminalRun("run-child", domain.DriverRunCompleted))
	runs, err := st.DriverRuns().List(t.Context(), "TEST", store.DriverRunFilter{})
	if err != nil || len(runs) != 0 {
		t.Fatalf("legacy TriggerRoutes spawned %d runs (err %v), want none", len(runs), err)
	}
	if len(publisher.snapshot()) != 1 {
		t.Fatal("outcome port did not receive run.finished")
	}
}

func TestEmitRunFinishedEventNeverBypassesDurableClaimOrBackoff(t *testing.T) {
	for _, state := range []string{"claimed", "backoff"} {
		t.Run(state, func(t *testing.T) {
			st := newRunEventsStore(t)
			seedRunFinishedBinding(t, st)
			ctx := t.Context()
			if _, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
				WorkspaceKey: "TEST", RunID: "run-durable", DriverID: "composer", DriverVersionID: "v1",
			}); err != nil {
				t.Fatal(err)
			}
			claimedRun, err := st.DriverRuns().Claim(ctx, "TEST", "run-durable", "node", "lease")
			if err != nil {
				t.Fatal(err)
			}
			final, err := st.DriverRuns().Finish(ctx, "TEST", "run-durable", store.DriverRunFinish{
				NodeID: "node", LeaseID: "lease", FencingToken: claimedRun.FencingToken,
				Status: domain.DriverRunFailed, Summary: "failed", ErrorClass: "driver_runtime",
			})
			if err != nil {
				t.Fatal(err)
			}

			outbox := st.DriverRuns().(store.DriverRunOutcomeStore)
			now := final.FinishedAt.Add(time.Millisecond)
			rows, err := outbox.ClaimDriverRunOutcomes(ctx, store.DriverRunOutcomeClaim{
				WorkspaceKey: "TEST", ClaimID: "existing-owner", Before: now,
				ClaimUntil: now.Add(time.Minute), Limit: 1,
			})
			if err != nil || len(rows) != 1 {
				t.Fatalf("existing claim = %+v, %v", rows, err)
			}
			if state == "backoff" {
				if err := outbox.RetryDriverRunOutcome(ctx, store.DriverRunOutcomeRetry{
					WorkspaceKey: "TEST", RunID: final.RunID, ClaimID: "existing-owner",
					AvailableAt: now.Add(time.Hour), Error: "temporary",
				}); err != nil {
					t.Fatal(err)
				}
			}

			publisher := &recordingRunOutcomePublisher{}
			emitRunFinishedEvent(ctx, st, publisher, final)
			if got := publisher.snapshot(); len(got) != 0 {
				t.Fatalf("durable %s row bypassed via direct publication: %+v", state, got)
			}
		})
	}
}

// suspendedCompositionParent registers a pending composition await with no
// Automation binding. The outcome notification must still resume it.
func suspendedCompositionParent(t *testing.T, st *memstore.Store, childRunID string) string {
	t.Helper()
	ctx := t.Context()
	if _, err := st.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: "TEST", DriverID: "composer", Name: "composer",
		OwnerType: domain.DriverOwnerSystem, Status: domain.DriverStatusActive,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: "TEST", VersionID: "v1", DriverID: "composer", Version: 1,
		SourceDigest: "sha256:s", BundleDigest: "sha256:b", ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: "TEST", RunID: "run-parent", DriverID: "composer", DriverVersionID: "v1", Entrypoint: "run",
	}); err != nil {
		t.Fatal(err)
	}
	parent, err := st.DriverRuns().Claim(ctx, "TEST", "run-parent", "node-1", "lease-1")
	if err != nil {
		t.Fatal(err)
	}
	key := domain.AwaitInstanceKey("run-parent", 1)
	reg, err := st.Awaits().RegisterAwaitAndCheck(ctx, "TEST", store.AwaitRegistration{
		InstanceKey: key, RunID: "run-parent", Pattern: RunFinishedSubjectKey(childRunID),
		Deadline: time.Now().UTC().Add(time.Hour),
	})
	if err != nil || reg.Satisfied {
		t.Fatalf("register = %+v, %v", reg, err)
	}
	if _, err := st.DriverRuns().Suspend(ctx, "TEST", "run-parent", parent.NodeID, parent.LeaseID, parent.FencingToken, key); err != nil {
		t.Fatal(err)
	}
	return key
}

func TestEmitRunFinishedEventResolvesCompositionAwaitWithoutListener(t *testing.T) {
	st := newRunEventsStore(t)
	key := suspendedCompositionParent(t, st, "run-child")
	emitRunFinishedEvent(t.Context(), st, nil, terminalRun("run-child", domain.DriverRunFailed))

	eventID := RunFinishedEventID("run-child", domain.DriverRunFailed)
	parent, err := st.DriverRuns().Get(t.Context(), "TEST", "run-parent")
	if err != nil || parent.Status != domain.DriverRunQueued || parent.ResumeSourceEventID != eventID {
		t.Fatalf("parent = %+v, %v; want queued by %s", parent, err, eventID)
	}
	satisfied, err := st.Awaits().GetSatisfiedAwait(t.Context(), "TEST", key)
	if err != nil || satisfied.SatisfiedByEventID != eventID {
		t.Fatalf("satisfied = %+v, %v", satisfied, err)
	}
}

func TestExecutorFinishPublishesOutcomeAndIgnoresPublisherFailure(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	st := newRunEventsStore(t)
	writeFlueDist(t, root, "epic-runner", "done")
	registered, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{
		WorkspaceKey: "TEST", WorkDir: root, DistPath: "dist", DriverName: "epic-runner", CreatedBy: "tester", Activate: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateDriverRun(ctx, st, RunOptions{WorkspaceKey: "TEST", DriverID: registered.Driver.DriverID, RunID: "run-1"}); err != nil {
		t.Fatal(err)
	}
	publisher := &recordingRunOutcomePublisher{err: errors.New("automation unavailable")}
	result, err := testExecutor(st, Executor{
		Store: st, WorkspaceKey: "TEST", WorkDir: root, NodeID: "node-1", LeaseID: "lease-1",
		Runner:            &recordingRunner{result: RunResult{Status: domain.DriverRunCancelled, Summary: "operator cancelled"}},
		HeartbeatInterval: -1, RunOutcomes: publisher,
	}).RunOnce(ctx)
	if err != nil || result.Final == nil || result.Final.Status != domain.DriverRunCancelled {
		t.Fatalf("RunOnce = %+v, %v", result, err)
	}
	if got := publisher.snapshot(); len(got) != 1 || got[0].EventID != RunFinishedEventID("run-1", domain.DriverRunCancelled) {
		t.Fatalf("outcomes = %+v", got)
	}
}

func TestExecutorStaleRecoveryPublishesOutcome(t *testing.T) {
	ctx := t.Context()
	root := t.TempDir()
	st := newRunEventsStore(t)
	writeFlueDist(t, root, "epic-runner", "done")
	registered, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{
		WorkspaceKey: "TEST", WorkDir: root, DistPath: "dist", DriverName: "epic-runner", CreatedBy: "tester", Activate: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CreateDriverRun(ctx, st, RunOptions{WorkspaceKey: "TEST", DriverID: registered.Driver.DriverID, RunID: "run-stale"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DriverRuns().Claim(ctx, "TEST", "run-stale", "node-1", "lease-1"); err != nil {
		t.Fatal(err)
	}
	publisher := &recordingRunOutcomePublisher{}
	executor := testExecutor(st, Executor{Store: st, WorkspaceKey: "TEST", RunOutcomes: publisher})
	result, err := executor.recoverStaleWorkspace(ctx, "TEST", store.StaleDriverRunRecovery{
		StaleBefore: time.Now().UTC().Add(time.Minute), Summary: "driver executor heartbeat expired",
	})
	if err != nil || result.Recovered != 1 {
		t.Fatalf("recover = %+v, %v", result, err)
	}
	got := publisher.snapshot()
	if len(got) != 1 || got[0].EventID != RunFinishedEventID("run-stale", domain.DriverRunFailed) {
		t.Fatalf("outcomes = %+v", got)
	}
	var payload runFinishedPayload
	if err := json.Unmarshal(got[0].Payload, &payload); err != nil || payload.ErrorClass != "stale_driver_run" {
		t.Fatalf("payload = %+v, %v", payload, err)
	}
}
