//nolint:revive // Tests use the established driver package name to exercise unexported helpers.
package driver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func newRunEventsStore(t *testing.T) *memstore.Store {
	t.Helper()
	st := memstore.New()
	if _, err := st.Workspaces().Create(context.Background(), store.WorkspaceCreate{Key: "TEST", Name: "test"}); err != nil {
		t.Fatalf("Create workspace: %v", err)
	}
	return st
}

// seedRunFinishedBinding registers a driver, a passed version and one enabled
// binding listening on the internal run.finished loopback route.
func seedRunFinishedBinding(t *testing.T, st *memstore.Store) {
	t.Helper()
	ctx := context.Background()
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

// journalParentEvent appends an admitting trigger event at the given hop
// depth, standing in for the dispatch path that admitted a run.
func journalParentEvent(t *testing.T, st *memstore.Store, eventID string, hopDepth int) {
	t.Helper()
	appender, ok := st.TriggerEvents().(store.TriggerEventAppender)
	if !ok {
		t.Fatal("memstore must implement store.TriggerEventAppender")
	}
	now := time.Now().UTC()
	if _, err := appender.AppendTriggerEvent(context.Background(), &domain.TriggerEvent{
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
		WorkspaceKey: "TEST",
		RunID:        runID,
		DriverID:     "composer",
		Status:       status,
		Summary:      "summary for " + runID,
		ErrorClass:   "",
		EpicID:       "TEST-1",
		ParentRunID:  "run-parent",
	}
}

func TestEmitRunFinishedEventTerminalStatuses(t *testing.T) {
	ctx := context.Background()
	for _, status := range []domain.DriverRunStatus{
		domain.DriverRunCompleted,
		domain.DriverRunFailed,
		domain.DriverRunNeedsReview,
		domain.DriverRunCancelled,
	} {
		t.Run(string(status), func(t *testing.T) {
			st := newRunEventsStore(t)
			run := terminalRun("run-child", status)
			emitRunFinishedEvent(ctx, st, nil, run)

			eventID := RunFinishedEventID("run-child", status)
			event, err := st.TriggerEvents().Get(ctx, "TEST", eventID)
			if err != nil {
				t.Fatalf("journaled event %q: %v", eventID, err)
			}
			if event.EventType != RunFinishedEventType || event.SubjectRef != "run-child" {
				t.Fatalf("event = %+v, want run.finished about run-child", event)
			}
			if event.ActorRef != "system" || event.Origin != domain.TriggerEventOriginSystem || event.HopDepth != 0 {
				t.Fatalf("event provenance = actor %q origin %q depth %d, want system/system/0",
					event.ActorRef, event.Origin, event.HopDepth)
			}
			if got := domain.AwaitEventKey(event.EventType, event.SubjectRef); got != RunFinishedSubjectKey("run-child") {
				t.Fatalf("rendered key = %q, want %q", got, RunFinishedSubjectKey("run-child"))
			}
			all, err := st.TriggerEvents().List(ctx, "TEST", store.TriggerEventFilter{})
			if err != nil || len(all) != 1 {
				t.Fatalf("journal = %d events (err %v), want exactly 1", len(all), err)
			}
		})
	}
}

func TestEmitRunFinishedEventIdempotentReemission(t *testing.T) {
	ctx := context.Background()
	st := newRunEventsStore(t)
	run := terminalRun("run-child", domain.DriverRunCompleted)

	emitRunFinishedEvent(ctx, st, nil, run)
	emitRunFinishedEvent(ctx, st, nil, run)

	all, err := st.TriggerEvents().List(ctx, "TEST", store.TriggerEventFilter{})
	if err != nil {
		t.Fatalf("List events: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("journal = %d events after double finish, want 1 (idempotent append)", len(all))
	}
}

func TestEmitRunFinishedEventIgnoresNonTerminal(t *testing.T) {
	ctx := context.Background()
	st := newRunEventsStore(t)
	for _, status := range []domain.DriverRunStatus{
		domain.DriverRunQueued,
		domain.DriverRunRunning,
		domain.DriverRunSuspendedAwaitingEvent,
	} {
		emitRunFinishedEvent(ctx, st, nil, terminalRun("run-child", status))
	}
	all, err := st.TriggerEvents().List(ctx, "TEST", store.TriggerEventFilter{})
	if err != nil || len(all) != 0 {
		t.Fatalf("journal = %d events (err %v), want 0 for non-terminal statuses", len(all), err)
	}
}

func TestEmitRunFinishedEventHopDepthStamping(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name        string
		parentDepth int
		sourceRef   string
		wantDepth   int
	}{
		{name: "admitting event chains parent+1", parentDepth: 2, sourceRef: "evt-parent", wantDepth: 3},
		{name: "unresolvable source ref is a root", parentDepth: 0, sourceRef: "loom driver run", wantDepth: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := newRunEventsStore(t)
			if tc.sourceRef == "evt-parent" {
				journalParentEvent(t, st, "evt-parent", tc.parentDepth)
			}
			run := terminalRun("run-child", domain.DriverRunCompleted)
			run.SourceRef = tc.sourceRef
			emitRunFinishedEvent(ctx, st, nil, run)

			event, err := st.TriggerEvents().Get(ctx, "TEST", RunFinishedEventID("run-child", domain.DriverRunCompleted))
			if err != nil {
				t.Fatalf("journaled event: %v", err)
			}
			if event.HopDepth != tc.wantDepth {
				t.Fatalf("journaled hop depth = %d, want %d", event.HopDepth, tc.wantDepth)
			}
		})
	}
}

// TestEmitRunFinishedEventCapSuppressesBindingsNotJournal proves the locked
// split: past the hop-depth cap the loopback drops binding fan-out, but the
// journal append still lands so composition awaits can never be suppressed.
func TestEmitRunFinishedEventCapSuppressesBindingsNotJournal(t *testing.T) {
	ctx := context.Background()

	t.Run("below cap fans out to internal binding", func(t *testing.T) {
		st := newRunEventsStore(t)
		seedRunFinishedBinding(t, st)
		emitRunFinishedEvent(ctx, st, nil, terminalRun("run-child", domain.DriverRunCompleted))

		runs, err := st.DriverRuns().List(ctx, "TEST", store.DriverRunFilter{})
		if err != nil || len(runs) != 1 {
			t.Fatalf("runs = %d (err %v), want 1 binding-spawned run", len(runs), err)
		}
		var envelope struct {
			Origin   string `json:"origin"`
			HopDepth int    `json:"hopDepth"`
			Event    struct {
				RunID       string `json:"runId"`
				Status      string `json:"status"`
				ParentRunID string `json:"parentRunId"`
			} `json:"event"`
		}
		if err := json.Unmarshal(runs[0].Payload, &envelope); err != nil {
			t.Fatalf("decode spawned payload: %v", err)
		}
		if envelope.Origin != "system" || envelope.Event.RunID != "run-child" ||
			envelope.Event.Status != "completed" || envelope.Event.ParentRunID != "run-parent" {
			t.Fatalf("envelope = %+v, want system run.finished payload for run-child", envelope)
		}
	})

	t.Run("at cap journal lands and binding stays silent", func(t *testing.T) {
		st := newRunEventsStore(t)
		seedRunFinishedBinding(t, st)
		journalParentEvent(t, st, "evt-deep", 4) // default cap: child depth 5 exceeds

		run := terminalRun("run-child", domain.DriverRunCompleted)
		run.SourceRef = "evt-deep"
		emitRunFinishedEvent(ctx, st, nil, run)

		event, err := st.TriggerEvents().Get(ctx, "TEST", RunFinishedEventID("run-child", domain.DriverRunCompleted))
		if err != nil {
			t.Fatalf("journaled event past cap: %v", err)
		}
		if event.HopDepth != 5 {
			t.Fatalf("journaled hop depth = %d, want 5", event.HopDepth)
		}
		runs, err := st.DriverRuns().List(ctx, "TEST", store.DriverRunFilter{})
		if err != nil || len(runs) != 0 {
			t.Fatalf("runs = %d (err %v), want 0 (guard dropped binding fan-out)", len(runs), err)
		}
	})
}

// TestEmitRunFinishedEventSatisfiesAwaitRegistrationScan is the
// already-terminal child case: the journaled run.finished is found by the
// registration-time scan, so a parent registering after the child finished
// resolves immediately (no lost wakeup).
func TestEmitRunFinishedEventSatisfiesAwaitRegistrationScan(t *testing.T) {
	ctx := context.Background()
	st := newRunEventsStore(t)
	emitRunFinishedEvent(ctx, st, nil, terminalRun("run-child", domain.DriverRunCompleted))

	result, err := st.Awaits().RegisterAwaitAndCheck(ctx, "TEST", store.AwaitRegistration{
		InstanceKey: "run-parent#await-1",
		RunID:       "run-parent",
		Pattern:     RunFinishedSubjectKey("run-child"),
		Deadline:    time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("RegisterAwaitAndCheck: %v", err)
	}
	if !result.Satisfied || result.Instance.Status != domain.AwaitSatisfied {
		t.Fatalf("result = %+v, want immediately satisfied", result)
	}
	if want := RunFinishedEventID("run-child", domain.DriverRunCompleted); result.Instance.SatisfiedByEventID != want {
		t.Fatalf("satisfied by %q, want %q", result.Instance.SatisfiedByEventID, want)
	}
}

// suspendedCompositionParent seeds the composer catalog (no internal binding)
// and suspends run-parent suspended on the child's run.finished key.
func suspendedCompositionParent(t *testing.T, st *memstore.Store, childRunID string) string {
	t.Helper()
	ctx := context.Background()
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
	if _, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: "TEST", RunID: "run-parent", DriverID: "composer", DriverVersionID: "v1", Entrypoint: "run",
	}); err != nil {
		t.Fatalf("Create parent run: %v", err)
	}
	parent, err := st.DriverRuns().Claim(ctx, "TEST", "run-parent", "node-1", "lease-1")
	if err != nil {
		t.Fatalf("Claim parent run: %v", err)
	}
	key := domain.AwaitInstanceKey("run-parent", 1)
	reg, err := st.Awaits().RegisterAwaitAndCheck(ctx, "TEST", store.AwaitRegistration{
		InstanceKey: key, RunID: "run-parent", Pattern: RunFinishedSubjectKey(childRunID),
		Deadline: time.Now().UTC().Add(time.Hour),
	})
	if err != nil || reg.Satisfied {
		t.Fatalf("RegisterAwaitAndCheck = %+v, %v; want pending", reg, err)
	}
	if _, err := st.DriverRuns().Suspend(ctx, "TEST", "run-parent",
		parent.NodeID, parent.LeaseID, parent.FencingToken, key); err != nil {
		t.Fatalf("Suspend parent run: %v", err)
	}
	return key
}

// TestEmitRunFinishedEventResolvesCompositionAwait is the dispatch-time twin
// (AW7's matcher pass right after the journal append): a parent suspended on
// "run.finished:{child}" re-queues when the child reaches a terminal status,
// with the lifecycle payload persisted on the satisfied row — independent of
// binding configuration (no internal.* binding is seeded, so the loopback
// fan-out is the not-found no-op).
func TestEmitRunFinishedEventResolvesCompositionAwait(t *testing.T) {
	ctx := context.Background()
	st := newRunEventsStore(t)
	key := suspendedCompositionParent(t, st, "run-child")

	emitRunFinishedEvent(ctx, st, nil, terminalRun("run-child", domain.DriverRunFailed))

	eventID := RunFinishedEventID("run-child", domain.DriverRunFailed)
	parent, err := st.DriverRuns().Get(ctx, "TEST", "run-parent")
	if err != nil {
		t.Fatalf("Get parent run: %v", err)
	}
	if parent.Status != domain.DriverRunQueued || parent.ResumeSourceEventID != eventID {
		t.Fatalf("parent = %s/%s, want queued by %s", parent.Status, parent.ResumeSourceEventID, eventID)
	}
	satisfied, err := st.Awaits().GetSatisfiedAwait(ctx, "TEST", key)
	if err != nil {
		t.Fatalf("GetSatisfiedAwait: %v", err)
	}
	var payload struct {
		RunID  string `json:"runId"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(satisfied.SatisfiedPayload, &payload); err != nil {
		t.Fatalf("decode satisfied payload %s: %v", satisfied.SatisfiedPayload, err)
	}
	if payload.RunID != "run-child" || payload.Status != string(domain.DriverRunFailed) {
		t.Fatalf("payload = %+v, want run-child failed", payload)
	}
}

func TestExecutorFinishEmitsRunFinished(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st := newRunEventsStore(t)
	writeFlueDist(t, root, "epic-runner", "done")
	registered, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{WorkspaceKey: "TEST", WorkDir: root, DistPath: "dist", DriverName: "epic-runner", CreatedBy: "tester", Activate: true})
	if err != nil {
		t.Fatalf("RegisterFlueDriver: %v", err)
	}
	if _, err := CreateDriverRun(ctx, st, RunOptions{WorkspaceKey: "TEST", DriverID: registered.Driver.DriverID, RunID: "run-1"}); err != nil {
		t.Fatalf("CreateDriverRun: %v", err)
	}

	runner := &recordingRunner{result: RunResult{Status: domain.DriverRunCancelled, Summary: "operator cancelled"}}
	if _, err := (&Executor{
		Store: st, WorkspaceKey: "TEST", WorkDir: root,
		NodeID: "node-1", LeaseID: "lease-1", Runner: runner, HeartbeatInterval: -1,
	}).RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	event, err := st.TriggerEvents().Get(ctx, "TEST", RunFinishedEventID("run-1", domain.DriverRunCancelled))
	if err != nil {
		t.Fatalf("journaled run.finished after finish: %v", err)
	}
	if event.SubjectRef != "run-1" || event.Origin != domain.TriggerEventOriginSystem {
		t.Fatalf("event = %+v, want system run.finished about run-1", event)
	}
}

func TestExecutorStaleRecoveryEmitsRunFinished(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st := newRunEventsStore(t)
	seedRunFinishedBinding(t, st)
	writeFlueDist(t, root, "epic-runner", "done")
	registered, err := RegisterFlueDriver(ctx, st, RegisterFlueOptions{WorkspaceKey: "TEST", WorkDir: root, DistPath: "dist", DriverName: "epic-runner", CreatedBy: "tester", Activate: true})
	if err != nil {
		t.Fatalf("RegisterFlueDriver: %v", err)
	}
	if _, err := CreateDriverRun(ctx, st, RunOptions{WorkspaceKey: "TEST", DriverID: registered.Driver.DriverID, RunID: "run-stale"}); err != nil {
		t.Fatalf("CreateDriverRun: %v", err)
	}
	if _, err := st.DriverRuns().Claim(ctx, "TEST", "run-stale", "node-1", "lease-1"); err != nil {
		t.Fatalf("Claim: %v", err)
	}

	exec := &Executor{Store: st, WorkspaceKey: "TEST"}
	result, err := exec.recoverStaleWorkspace(ctx, "TEST", store.StaleDriverRunRecovery{
		StaleBefore: time.Now().UTC().Add(time.Minute),
		Summary:     "driver executor heartbeat expired",
	})
	if err != nil {
		t.Fatalf("recoverStaleWorkspace: %v", err)
	}
	if result.Recovered != 1 {
		t.Fatalf("recovered = %d, want 1", result.Recovered)
	}

	if _, err := st.TriggerEvents().Get(ctx, "TEST", RunFinishedEventID("run-stale", domain.DriverRunFailed)); err != nil {
		t.Fatalf("journaled run.finished after stale recovery: %v", err)
	}
	assertStaleRunFinishedPayload(t, st)
}

// assertStaleRunFinishedPayload finds the binding-spawned run and checks the
// loopback envelope carries the stale-sweep terminal outcome.
func assertStaleRunFinishedPayload(t *testing.T, st *memstore.Store) {
	t.Helper()
	runs, err := st.DriverRuns().List(context.Background(), "TEST", store.DriverRunFilter{Status: domain.DriverRunQueued})
	if err != nil || len(runs) != 1 {
		t.Fatalf("queued binding-spawned runs = %d (err %v), want 1", len(runs), err)
	}
	var envelope struct {
		Event struct {
			RunID      string `json:"runId"`
			Status     string `json:"status"`
			ErrorClass string `json:"errorClass"`
		} `json:"event"`
	}
	if err := json.Unmarshal(runs[0].Payload, &envelope); err != nil {
		t.Fatalf("decode spawned payload: %v", err)
	}
	if envelope.Event.RunID != "run-stale" || envelope.Event.Status != string(domain.DriverRunFailed) ||
		envelope.Event.ErrorClass != "stale_driver_run" {
		t.Fatalf("envelope event = %+v, want failed run-stale with errorClass stale_driver_run", envelope.Event)
	}
}
