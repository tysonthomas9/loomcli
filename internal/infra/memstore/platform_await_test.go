package memstore

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/store/storetest"
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
		event, deduped := s.events.create(&domain.TriggerEvent{
			WorkspaceKey: ws,
			SourceKind:   "test",
			EventType:    eventType,
			SubjectRef:   subjectRef,
			ActorRef:     actorRef,
			Origin:       domain.TriggerEventOriginExternal,
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
		OwnerType:    domain.DriverOwnerSystem,
		Status:       domain.DriverStatusActive,
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
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
}

// TestMemstoreAwaitRegisterAppendRace races RegisterAwaitAndCheck against a
// concurrent event append followed by the dispatch matcher's
// list-and-resolve pass (what AW7 does after every journal append). The
// lost-wakeup invariant: whatever the interleaving, the await ends satisfied
// — either the registration scan saw the event (append before park) or the
// matcher's list saw the parked instance (park before append). Run with
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
			// Parked: the matcher pass MUST have resolved it — a still-pending
			// await here is exactly the lost wakeup the shared lock forbids.
			if _, err := s.Awaits().GetSatisfiedAwait(ctx, ws, instanceKey); err != nil {
				t.Fatalf("iteration %d: lost wakeup — parked await never resolved: %v", i, err)
			}
		}
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
