package memstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/store/testdata/storetest"
)

// TestMemstoreAwaitConformance runs the shared store-agnostic await suite
// (storetest.RunAwaitConformance) against memstore — the same cases the
// fleet-db backend runs through the AW5 client, so both stores prove
// identical semantics.
func TestMemstoreAwaitConformance(t *testing.T) {
	storetest.RunAwaitConformance(t, newMemstoreAwaitHarness)
}

func newMemstoreAwaitHarness(t testing.TB) *storetest.AwaitHarness {
	s := New()
	const ws = "WS"
	ctx := context.Background()
	createAwaitRunCatalog(t, ctx, s, ws)
	return &storetest.AwaitHarness{
		Workspace:   ws,
		Awaits:      s.Awaits(),
		AppendEvent: memstoreAwaitAppendEvent(s, ws),
		Runs: &storetest.AwaitRunHarness{
			Store: s.DriverRuns(),
			NewRun: func(t testing.TB, runID, parentRunID string) *domain.DriverRun {
				run, err := s.DriverRuns().Create(ctx, store.DriverRunCreate{
					WorkspaceKey:    ws,
					RunID:           runID,
					DriverID:        "driver-await",
					DriverVersionID: "version-await",
					ParentRunID:     parentRunID,
				})
				if err != nil {
					t.Fatalf("Create driver run %s: %v", runID, err)
				}
				return run
			},
			Suspend: func(ctx context.Context, runID, nodeID, leaseID string, fencingToken int64) (*domain.DriverRun, error) {
				return s.runs.Suspend(ctx, ws, runID, nodeID, leaseID, fencingToken, domain.AwaitInstanceKey(runID, 1))
			},
			Resume: func(ctx context.Context, runID, resumeSourceEventID string) (*domain.DriverRun, error) {
				return s.runs.ResumeAwaiting(ctx, ws, runID, domain.AwaitInstanceKey(runID, 1), resumeSourceEventID)
			},
		},
	}
}

// memstoreAwaitAppendEvent journals one trigger event the way the dispatch
// path does, returning the assigned event ID.
func memstoreAwaitAppendEvent(s *Store, ws string) func(t testing.TB, eventType, subjectRef, actorRef string) string {
	return func(t testing.TB, eventType, subjectRef, actorRef string) string {
		now := time.Now().UTC()
		event, deduped := s.events.create(&automation.Event{
			WorkspaceKey: ws,
			SourceKind:   "test",
			EventType:    eventType,
			SubjectRef:   subjectRef,
			ActorRef:     actorRef,
			Origin:       automation.EventOriginExternal,
			OccurredAt:   now,
			ReceivedAt:   now,
		})
		if deduped {
			t.Fatalf("test event %s:%s unexpectedly deduped", eventType, subjectRef)
		}
		return event.EventID
	}
}

func createAwaitRunCatalog(t testing.TB, ctx context.Context, s *Store, ws string) {
	t.Helper()
	if _, err := s.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: ws,
		DriverID:     "driver-await",
		Name:         "await-driver",
		OwnerType:    workflowcatalog.DriverOwnerSystem,
		Status:       workflowcatalog.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := s.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey:     ws,
		VersionID:        "version-await",
		DriverID:         "driver-await",
		Version:          1,
		SourceDigest:     "sha256:source-v1",
		BundleDigest:     "sha256:bundle-v1",
		ValidationStatus: workflowcatalog.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
}

// TestMemstoreAwaitRegisterAppendRace races RegisterAwaitAndCheck against a
// concurrent event append followed by the dispatch matcher's
// list-and-resolve pass (what AW7 does after every journal append). The
// lost-wakeup invariant: whatever the interleaving, the await ends satisfied
// — either the registration scan saw the event (append before registration) or the
// matcher's list saw the pending instance (registration before append). Run with
// -race.
func TestMemstoreAwaitRegisterAppendRace(t *testing.T) {
	s := New()
	ctx := context.Background()
	const ws = "WS"
	appendEvent := memstoreAwaitAppendEvent(s, ws)
	for i := range 200 {
		pattern := fmt.Sprintf("pr.approved:repo-%d", i)
		runID := fmt.Sprintf("run-%d", i)
		instanceKey := domain.AwaitInstanceKey(runID, 1)

		var wg sync.WaitGroup
		var result *store.AwaitResult
		var registerErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			eventID := appendEvent(t, "pr.approved", fmt.Sprintf("repo-%d", i), "alice")
			pending, err := s.Awaits().ListAwaitsByPattern(ctx, ws, pattern)
			if err != nil {
				t.Errorf("iteration %d: ListAwaitsByPattern: %v", i, err)
				return
			}
			for _, inst := range pending {
				if _, err := s.Awaits().ResolveAwait(ctx, ws, inst.InstanceKey, eventID, nil, "alice"); err != nil {
					t.Errorf("iteration %d: ResolveAwait(%s): %v", i, inst.InstanceKey, err)
				}
			}
		}()
		go func() {
			defer wg.Done()
			result, registerErr = s.Awaits().RegisterAwaitAndCheck(ctx, ws, store.AwaitRegistration{
				InstanceKey: instanceKey,
				RunID:       runID,
				Pattern:     pattern,
				Deadline:    time.Now().Add(time.Hour).UTC(),
			})
		}()
		wg.Wait()

		if registerErr != nil {
			t.Fatalf("iteration %d: RegisterAwaitAndCheck: %v", i, registerErr)
		}
		if !result.Satisfied {
			// Pending: the matcher pass MUST have resolved it — a still-pending
			// await here is exactly the lost wakeup the shared lock forbids.
			if _, err := s.Awaits().GetSatisfiedAwait(ctx, ws, instanceKey); err != nil {
				t.Fatalf("iteration %d: lost wakeup — pending await never resolved: %v", i, err)
			}
		}
	}
}

func TestMemstoreAwaitEventBeforeRegistrationPersistsPayload(t *testing.T) {
	s := New()
	ctx := t.Context()
	const ws = "WS"
	payload := json.RawMessage(`{"approved":true}`)
	now := time.Now().UTC()
	event, deduped := s.events.create(&automation.Event{
		WorkspaceKey:  ws,
		SourceKind:    "test",
		SourceEventID: "approval-delivery-123",
		EventType:     "approval.granted",
		SubjectRef:    "pr/123",
		ActorRef:      "alice",
		Payload:       payload,
		Origin:        automation.EventOriginExternal,
		OccurredAt:    now,
		ReceivedAt:    now,
	})
	if deduped {
		t.Fatal("event unexpectedly deduped")
	}

	result, err := s.Awaits().RegisterAwaitAndCheck(ctx, ws, store.AwaitRegistration{
		InstanceKey: domain.AwaitInstanceKey("run-1", 1),
		RunID:       "run-1",
		Pattern:     "approval.granted:pr/123",
		ActorAllow:  []string{"alice"},
		Deadline:    time.Now().Add(time.Hour).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Satisfied || result.Instance.SatisfiedByEventID != "approval-delivery-123" ||
		result.Instance.SatisfiedActor != "alice" ||
		string(result.Instance.SatisfiedPayload) != string(payload) {
		t.Fatalf("immediate result = %+v, want source event identity and payload %s (stored event %q)",
			result.Instance, payload, event.EventID)
	}
}

func TestMemstoreAwaitRegistrationSkipsHistoricalForgedRunFinished(t *testing.T) {
	s := New()
	ctx := t.Context()
	const ws = "WS"
	now := time.Now().UTC()

	appendEvent := func(eventID, sourceEventID, subject, sourceKind string, origin automation.EventOrigin, receivedAt time.Time) {
		t.Helper()
		if _, deduped := s.events.create(&automation.Event{
			WorkspaceKey: ws, EventID: eventID, SourceEventID: sourceEventID,
			SourceKind: sourceKind, EventType: execution.RunFinishedEventType,
			SubjectRef: subject, ActorRef: execution.RunFinishedActorRef, Origin: origin,
			OccurredAt: receivedAt, ReceivedAt: receivedAt,
			Payload: json.RawMessage(`{"runId":"` + subject + `","status":"completed"}`),
		}); deduped {
			t.Fatalf("event %s unexpectedly deduped", eventID)
		}
	}

	appendEvent("stored-forged-only", execution.RunFinishedSourceEventIDPrefix+"child-forged:completed",
		"child-forged", "github", automation.EventOriginExternal, now)
	forgedOnly, err := s.Awaits().RegisterAwaitAndCheck(ctx, ws, store.AwaitRegistration{
		InstanceKey: domain.AwaitInstanceKey("parent-forged", 1), RunID: "parent-forged",
		Pattern:    domain.AwaitEventKey(execution.RunFinishedEventType, "child-forged"),
		ActorAllow: []string{execution.RunFinishedActorRef}, Deadline: now.Add(time.Hour),
	})
	if err != nil || forgedOnly.Satisfied || forgedOnly.Instance.Status != domain.AwaitPending {
		t.Fatalf("forged-only registration = %+v, %v; want pending", forgedOnly, err)
	}

	appendEvent("stored-forged-first", execution.RunFinishedSourceEventIDPrefix+"child-valid:failed",
		"child-valid", "github", automation.EventOriginExternal, now.Add(time.Second))
	validID := execution.RunFinishedSourceEventIDPrefix + "child-valid:completed"
	appendEvent("stored-genuine-later", validID, "child-valid", execution.RunFinishedSourceKind,
		automation.EventOriginSystem, now.Add(2*time.Second))
	genuine, err := s.Awaits().RegisterAwaitAndCheck(ctx, ws, store.AwaitRegistration{
		InstanceKey: domain.AwaitInstanceKey("parent-valid", 1), RunID: "parent-valid",
		Pattern:    domain.AwaitEventKey(execution.RunFinishedEventType, "child-valid"),
		ActorAllow: []string{execution.RunFinishedActorRef}, Deadline: now.Add(time.Hour),
	})
	if err != nil || !genuine.Satisfied || genuine.Instance.SatisfiedByEventID != validID {
		t.Fatalf("registration after forged then genuine = %+v, %v; want %s", genuine, err, validID)
	}
}

func TestMemstoreAwaitRegistrationRejectsHistoricalReservedActorOnOrdinaryEvent(t *testing.T) {
	s := New()
	now := time.Now().UTC()
	if _, deduped := s.events.create(&automation.Event{
		WorkspaceKey: "WS", EventID: "stored-reserved-actor", SourceEventID: "approval-reserved-1",
		SourceKind: "github", EventType: "approval.granted", SubjectRef: "deploy-1",
		ActorRef: "system:approver", Origin: automation.EventOriginExternal,
		OccurredAt: now, ReceivedAt: now,
	}); deduped {
		t.Fatal("reserved actor event unexpectedly deduped")
	}
	result, err := s.Awaits().RegisterAwaitAndCheck(t.Context(), "WS", store.AwaitRegistration{
		InstanceKey: domain.AwaitInstanceKey("parent-reserved", 1), RunID: "parent-reserved",
		Pattern: "approval.granted:deploy-1", ActorAllow: []string{"system:approver"},
		Deadline: now.Add(time.Hour),
	})
	if err != nil || result.Satisfied || result.Instance.Status != domain.AwaitPending {
		t.Fatalf("reserved actor registration = %+v, %v; want pending", result, err)
	}
}

func TestMemstoreAwaitEventBeforeRegistrationRejectsReservedSourceEventID(t *testing.T) {
	s := New()
	ctx := t.Context()
	const ws = "WS"
	instanceKey := domain.AwaitInstanceKey("run-1", 1)
	now := time.Now().UTC()
	if _, deduped := s.events.create(&automation.Event{
		WorkspaceKey: ws, SourceKind: "test", SourceEventID: domain.AwaitTimeoutEventID(instanceKey),
		EventType: "approval.granted", SubjectRef: "pr/123", ActorRef: "alice",
		Origin: automation.EventOriginExternal, OccurredAt: now, ReceivedAt: now,
	}); deduped {
		t.Fatal("event unexpectedly deduped")
	}
	result, err := s.Awaits().RegisterAwaitAndCheck(ctx, ws, store.AwaitRegistration{
		InstanceKey: instanceKey, RunID: "run-1", Pattern: "approval.granted:pr/123",
		Deadline: time.Now().Add(time.Hour).UTC(),
	})
	if err != nil || result.Satisfied || result.Instance.Status != domain.AwaitPending {
		t.Fatalf("registration = %+v, %v; want pending after reserved source event id", result, err)
	}
}

func TestMemstoreAwaitEventBeforeRegistrationSkipsOversizedPayload(t *testing.T) {
	s := New()
	ctx := t.Context()
	const ws = "WS"
	now := time.Now().UTC()
	if _, deduped := s.events.create(&automation.Event{
		WorkspaceKey: ws,
		SourceKind:   "test",
		EventType:    "approval.granted",
		SubjectRef:   "pr/123",
		ActorRef:     "alice",
		Payload:      json.RawMessage(strings.Repeat("x", domain.DefaultAwaitResumePayloadCap+1)),
		Origin:       automation.EventOriginExternal,
		OccurredAt:   now,
		ReceivedAt:   now,
	}); deduped {
		t.Fatal("event unexpectedly deduped")
	}
	validPayload := json.RawMessage(`{"approved":true}`)
	if _, deduped := s.events.create(&automation.Event{
		WorkspaceKey: ws, SourceKind: "test", SourceEventID: "valid-after-oversized",
		EventType: "approval.granted", SubjectRef: "pr/123", ActorRef: "alice", Payload: validPayload,
		Origin: automation.EventOriginExternal, OccurredAt: now.Add(time.Second), ReceivedAt: now.Add(time.Second),
	}); deduped {
		t.Fatal("valid event unexpectedly deduped")
	}
	instanceKey := domain.AwaitInstanceKey("run-1", 1)
	result, err := s.Awaits().RegisterAwaitAndCheck(ctx, ws, store.AwaitRegistration{
		InstanceKey: instanceKey,
		RunID:       "run-1",
		Pattern:     "approval.granted:pr/123",
		Deadline:    time.Now().Add(time.Hour).UTC(),
	})
	if err != nil || !result.Satisfied || result.Instance.SatisfiedByEventID != "valid-after-oversized" ||
		string(result.Instance.SatisfiedPayload) != string(validPayload) {
		t.Fatalf("registration = %+v, %v; want later valid event after oversized candidate", result, err)
	}
}

// TestMemstoreAwaitResolveTimeoutRace races a real-event resolution against
// the deadline sweeper's synthetic timeout resolution for the same await:
// exactly one wins (Resume=true) and the persisted row matches the winner.
// Run with -race.
func TestMemstoreAwaitResolveTimeoutRace(t *testing.T) {
	ctx := context.Background()
	const ws = "WS"
	for i := range 200 {
		s := New()
		runID := fmt.Sprintf("run-%d", i)
		instanceKey := domain.AwaitInstanceKey(runID, 1)
		if _, err := s.Awaits().RegisterAwaitAndCheck(ctx, ws, store.AwaitRegistration{
			InstanceKey: instanceKey,
			RunID:       runID,
			Pattern:     "pr.approved:repo-1",
			Deadline:    time.Now().Add(time.Hour).UTC(),
		}); err != nil {
			t.Fatalf("iteration %d: register: %v", i, err)
		}

		eventResolve := awaitRaceResolver(t, s, ws, instanceKey, "event-real", json.RawMessage(`{"ok":true}`))
		timeoutResolve := awaitRaceResolver(t, s, ws, instanceKey, domain.AwaitTimeoutEventIDPrefix+"deadline", nil)
		var wg sync.WaitGroup
		results := make([]*store.AwaitResolution, 2)
		wg.Add(2)
		go func() { defer wg.Done(); results[0] = eventResolve() }()
		go func() { defer wg.Done(); results[1] = timeoutResolve() }()
		wg.Wait()

		assertSingleAwaitWinner(t, ctx, s, ws, instanceKey, i, results)
	}
}

// awaitRaceResolver returns a closure resolving the await with the given
// event, failing the test (via t.Errorf, goroutine-safe) on store errors.
func awaitRaceResolver(t *testing.T, s *Store, ws, instanceKey, eventID string, payload json.RawMessage) func() *store.AwaitResolution {
	return func() *store.AwaitResolution {
		out, err := s.Awaits().ResolveAwait(context.Background(), ws, instanceKey, eventID, payload, "alice")
		if err != nil {
			t.Errorf("ResolveAwait(%s, %s): %v", instanceKey, eventID, err)
		}
		return out
	}
}

func assertSingleAwaitWinner(t *testing.T, ctx context.Context, s *Store, ws, instanceKey string, iteration int, results []*store.AwaitResolution) {
	t.Helper()
	winners := 0
	var winner *store.AwaitResolution
	for _, res := range results {
		if res != nil && res.Resume {
			winners++
			winner = res
		}
	}
	if winners != 1 {
		t.Fatalf("iteration %d: %d resume winners, want exactly 1", iteration, winners)
	}
	row, err := s.Awaits().GetSatisfiedAwait(ctx, ws, instanceKey)
	if err != nil {
		t.Fatalf("iteration %d: GetSatisfiedAwait: %v", iteration, err)
	}
	if row.SatisfiedByEventID != winner.Instance.SatisfiedByEventID {
		t.Fatalf("iteration %d: persisted event %q != winner event %q",
			iteration, row.SatisfiedByEventID, winner.Instance.SatisfiedByEventID)
	}
	wantStatus := domain.AwaitSatisfied
	if domain.IsAwaitTimeoutEventID(row.SatisfiedByEventID) {
		wantStatus = domain.AwaitTimedOut
	}
	if row.Status != wantStatus {
		t.Fatalf("iteration %d: status %q for winner event %q, want %q",
			iteration, row.Status, row.SatisfiedByEventID, wantStatus)
	}
}

func TestResolveRunOutcomeAwaitAndResumeAtomicAndReplay(t *testing.T) {
	ctx := t.Context()
	const ws = "WS"
	s := New()
	createAwaitRunCatalog(t, ctx, s, ws)
	run, instanceKey := createPendingAwaitRun(t, ctx, s, ws, "parent", nil)
	if _, err := s.runs.Suspend(ctx, ws, run.RunID, "node", "lease", run.FencingToken, instanceKey); err != nil {
		t.Fatal(err)
	}
	resolver := s.Awaits().(store.RunOutcomeAwaitStore)
	eventID := "run-finished:child:completed"
	payload := json.RawMessage(`{"runId":"child","status":"completed"}`)
	if err := resolver.ResolveRunOutcomeAwaitAndResume(ctx, ws, instanceKey, eventID, payload); err != nil {
		t.Fatal(err)
	}
	assertAtomicAwaitResume(t, ctx, s, ws, run.RunID, instanceKey, eventID, payload)
	if err := resolver.ResolveRunOutcomeAwaitAndResume(ctx, ws, instanceKey, eventID, payload); err != nil {
		t.Fatalf("same-event replay: %v", err)
	}
	assertAtomicAwaitResume(t, ctx, s, ws, run.RunID, instanceKey, eventID, payload)
}

func TestResolveAwaitAndResumeDelayedOldReplayPreservesNewerMarker(t *testing.T) {
	ctx := t.Context()
	const ws = "WS"
	s := New()
	createAwaitRunCatalog(t, ctx, s, ws)
	run, firstKey := createPendingAwaitRun(t, ctx, s, ws, "parent", nil)
	if _, err := s.runs.Suspend(ctx, ws, run.RunID, "node", "lease", run.FencingToken, firstKey); err != nil {
		t.Fatal(err)
	}
	firstEvent := "run-finished:first:completed"
	outcomes := s.Awaits().(store.RunOutcomeAwaitStore)
	if err := outcomes.ResolveRunOutcomeAwaitAndResume(ctx, ws, firstKey, firstEvent, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.runs.Claim(ctx, ws, run.RunID, "node-2", "lease-2"); err != nil {
		t.Fatal(err)
	}
	secondKey := domain.AwaitInstanceKey(run.RunID, 2)
	if _, err := s.Awaits().RegisterAwaitAndCheck(ctx, ws, store.AwaitRegistration{
		InstanceKey: secondKey, RunID: run.RunID, Pattern: "approval.granted:second",
		Deadline: time.Now().Add(time.Hour).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	atomic := s.Awaits().(store.AtomicAwaitStore)
	if err := atomic.ResolveAwaitAndResume(ctx, ws, secondKey, "event-second", nil, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := outcomes.ResolveRunOutcomeAwaitAndResume(ctx, ws, firstKey, firstEvent, nil); err != nil {
		t.Fatalf("delayed first replay: %v", err)
	}
	progressed, err := s.DriverRuns().Get(ctx, ws, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if progressed.Status != domain.DriverRunRunning || progressed.AwaitInstanceKey != secondKey ||
		progressed.ResumeSourceEventID != "event-second" {
		t.Fatalf("run after delayed old replay = %+v", progressed)
	}
}

func TestResolveAwaitAndResumeDoesNotDeadlockConcurrentFinish(t *testing.T) {
	const ws = "WS"
	for i := range 50 {
		s := New()
		ctx := context.Background()
		createAwaitRunCatalog(t, ctx, s, ws)
		sourceEventID := fmt.Sprintf("source-%d", i)
		now := time.Now().UTC()
		if _, deduped := s.events.create(&automation.Event{
			WorkspaceKey: ws,
			EventID:      sourceEventID,
			SourceKind:   "test",
			EventType:    "source.created",
			SubjectRef:   sourceEventID,
			ActorRef:     "system",
			Origin:       automation.EventOriginExternal,
			OccurredAt:   now,
			ReceivedAt:   now,
		}); deduped {
			t.Fatalf("iteration %d: source event deduped", i)
		}
		runID := fmt.Sprintf("finish-race-%d", i)
		created, err := s.DriverRuns().Create(ctx, store.DriverRunCreate{
			WorkspaceKey: ws, RunID: runID, DriverID: "driver-await", DriverVersionID: "version-await",
			SourceRef: sourceEventID,
		})
		if err != nil {
			t.Fatal(err)
		}
		claimed, err := s.DriverRuns().Claim(ctx, ws, created.RunID, "node", "lease")
		if err != nil {
			t.Fatal(err)
		}
		instanceKey := domain.AwaitInstanceKey(runID, 1)
		if _, err := s.Awaits().RegisterAwaitAndCheck(ctx, ws, store.AwaitRegistration{
			InstanceKey: instanceKey,
			RunID:       runID,
			Pattern:     "approval.granted:finish-race",
			Deadline:    time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatal(err)
		}

		start := make(chan struct{})
		errCh := make(chan error, 2)
		go func() {
			<-start
			resolver := s.Awaits().(store.AtomicAwaitStore)
			errCh <- resolver.ResolveAwaitAndResume(ctx, ws, instanceKey, "approval-race", nil, "alice")
		}()
		go func() {
			<-start
			_, finishErr := s.DriverRuns().Finish(ctx, ws, runID, store.DriverRunFinish{
				NodeID: claimed.NodeID, LeaseID: claimed.LeaseID, FencingToken: claimed.FencingToken,
				Status: domain.DriverRunCompleted,
			})
			errCh <- finishErr
		}()
		close(start)
		for n := 0; n < 2; n++ {
			select {
			case err := <-errCh:
				if err != nil {
					t.Fatalf("iteration %d: concurrent operation: %v", i, err)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("iteration %d: concurrent Finish/ResolveAwaitAndResume deadlocked", i)
			}
		}
	}
}

func TestResolveRunOutcomeAwaitAndResumeWinsPendingSuspendWindow(t *testing.T) {
	ctx := t.Context()
	const ws = "WS"
	s := New()
	createAwaitRunCatalog(t, ctx, s, ws)
	run, instanceKey := createPendingAwaitRun(t, ctx, s, ws, "parent", nil)
	eventID := "run-finished:child:completed"
	resolver := s.Awaits().(store.RunOutcomeAwaitStore)
	if err := resolver.ResolveRunOutcomeAwaitAndResume(ctx, ws, instanceKey, eventID, nil); err != nil {
		t.Fatal(err)
	}
	marked, err := s.DriverRuns().Get(ctx, ws, run.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if marked.Status != domain.DriverRunRunning || marked.AwaitInstanceKey != instanceKey || marked.ResumeSourceEventID != eventID {
		t.Fatalf("pending-resume marker = %+v", marked)
	}
	if _, err := s.runs.Suspend(ctx, ws, run.RunID, "node", "lease", run.FencingToken, instanceKey); !errors.Is(err, domain.ErrDriverRunAlreadyResumed) {
		t.Fatalf("Suspend error = %v, want %v", err, domain.ErrDriverRunAlreadyResumed)
	}
}

func TestSuspendClearsPreviousAwaitCycleResumeMarker(t *testing.T) {
	ctx := t.Context()
	const ws = "WS"
	s := New()
	createAwaitRunCatalog(t, ctx, s, ws)
	run, firstKey := createPendingAwaitRun(t, ctx, s, ws, "parent", nil)
	if _, err := s.runs.Suspend(ctx, ws, run.RunID, "node", "lease", run.FencingToken, firstKey); err != nil {
		t.Fatal(err)
	}
	resolver := s.Awaits().(store.RunOutcomeAwaitStore)
	if err := resolver.ResolveRunOutcomeAwaitAndResume(ctx, ws, firstKey, "run-finished:child:completed", nil); err != nil {
		t.Fatal(err)
	}
	reclaimed, err := s.runs.Claim(ctx, ws, run.RunID, "node-2", "lease-2")
	if err != nil {
		t.Fatal(err)
	}
	secondKey := domain.AwaitInstanceKey(run.RunID, 2)
	suspended, err := s.runs.Suspend(ctx, ws, run.RunID, "node-2", "lease-2", reclaimed.FencingToken, secondKey)
	if err != nil {
		t.Fatalf("second await cycle suspend: %v", err)
	}
	if suspended.Status != domain.DriverRunSuspendedAwaitingEvent || suspended.AwaitInstanceKey != secondKey {
		t.Fatalf("second await cycle run = %+v", suspended)
	}
	if suspended.ResumeSourceEventID != "" {
		t.Fatalf("second await cycle resume source event = %q, want cleared", suspended.ResumeSourceEventID)
	}
}

func TestResolveRunOutcomeAwaitAndResumeSkipsActorRejectedMixedWaiters(t *testing.T) {
	ctx := t.Context()
	const ws = "WS"
	s := New()
	createAwaitRunCatalog(t, ctx, s, ws)
	rejected, rejectedKey := createPendingAwaitRun(t, ctx, s, ws, "rejected", []string{"operator"})
	allowed, allowedKey := createPendingAwaitRun(t, ctx, s, ws, "allowed", []string{"system"})
	for _, item := range []struct {
		run *domain.DriverRun
		key string
	}{{rejected, rejectedKey}, {allowed, allowedKey}} {
		if _, err := s.runs.Suspend(ctx, ws, item.run.RunID, "node", "lease", item.run.FencingToken, item.key); err != nil {
			t.Fatal(err)
		}
	}
	resolver := s.Awaits().(store.RunOutcomeAwaitStore)
	rows, err := s.Awaits().ListAwaitsByPattern(ctx, ws, "run.finished:child")
	if err != nil {
		t.Fatal(err)
	}
	eventID := "run-finished:child:completed"
	for _, row := range rows {
		if err := resolver.ResolveRunOutcomeAwaitAndResume(ctx, ws, row.InstanceKey, eventID, nil); err != nil {
			t.Fatalf("resolve %s: %v", row.InstanceKey, err)
		}
	}
	rejectedRun, _ := s.DriverRuns().Get(ctx, ws, rejected.RunID)
	allowedRun, _ := s.DriverRuns().Get(ctx, ws, allowed.RunID)
	if rejectedRun.Status != domain.DriverRunSuspendedAwaitingEvent {
		t.Fatalf("actor-rejected run status = %s, want suspended", rejectedRun.Status)
	}
	if _, err := s.Awaits().GetSatisfiedAwait(ctx, ws, rejectedKey); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("actor-rejected await error = %v, want pending/not found", err)
	}
	if allowedRun.Status != domain.DriverRunQueued || allowedRun.ResumeSourceEventID != eventID {
		t.Fatalf("allowed run = %+v", allowedRun)
	}
}

func createPendingAwaitRun(
	t *testing.T,
	ctx context.Context,
	s *Store,
	ws, runID string,
	actorAllow []string,
) (*domain.DriverRun, string) {
	t.Helper()
	created, err := s.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: ws, RunID: runID, DriverID: "driver-await", DriverVersionID: "version-await",
	})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.DriverRuns().Claim(ctx, ws, created.RunID, "node", "lease")
	if err != nil {
		t.Fatal(err)
	}
	instanceKey := domain.AwaitInstanceKey(runID, 1)
	if _, err := s.Awaits().RegisterAwaitAndCheck(ctx, ws, store.AwaitRegistration{
		InstanceKey: instanceKey, RunID: runID, Pattern: "run.finished:child",
		ActorAllow: actorAllow, Deadline: time.Now().Add(time.Hour).UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	return claimed, instanceKey
}

func assertAtomicAwaitResume(
	t *testing.T,
	ctx context.Context,
	s *Store,
	ws, runID, instanceKey, eventID string,
	payload json.RawMessage,
) {
	t.Helper()
	run, err := s.DriverRuns().Get(ctx, ws, runID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != domain.DriverRunQueued || run.ResumeSourceEventID != eventID || run.AwaitInstanceKey != instanceKey {
		t.Fatalf("resumed run = %+v", run)
	}
	await, err := s.Awaits().GetSatisfiedAwait(ctx, ws, instanceKey)
	if err != nil {
		t.Fatal(err)
	}
	if await.SatisfiedByEventID != eventID || await.SatisfiedActor == "" ||
		string(await.SatisfiedPayload) != string(payload) {
		t.Fatalf("satisfied await = %+v", await)
	}
}
