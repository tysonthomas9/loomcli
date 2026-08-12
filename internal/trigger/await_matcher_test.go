// Await-matcher tests live in the external trigger_test package so they can
// drive the real memstore await + driver-run stores (memstore imports trigger
// for the pattern engine, so an internal test would be an import cycle).
package trigger_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/driver/eventpolicy"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
)

const matcherWS = "WS"

// seedAwaitMatcherCatalog registers the driver + version every awaiting run
// in these tests targets.
func seedAwaitMatcherCatalog(t *testing.T, s *memstore.Store) {
	t.Helper()
	ctx := t.Context()
	if _, err := s.Drivers().Create(ctx, store.DriverCreate{
		WorkspaceKey: matcherWS, DriverID: "awaiter", Name: "awaiter",
		OwnerType: domain.DriverOwnerSystem, Status: domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	// VersionID is workspace-unique in memstore, so use a driver-scoped id —
	// the loopback/cron tests seed their own "v1" alongside this catalog.
	if _, err := s.DriverVersions().Create(ctx, store.DriverVersionCreate{
		WorkspaceKey: matcherWS, VersionID: "awaiter-v1", DriverID: "awaiter", Version: 1,
		SourceDigest: "sha256:s", BundleDigest: "sha256:b", ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
}

// createClaimedRun creates and claims one run so it can be suspended (or left
// running for the pending->suspend-window cases).
func createClaimedRun(t *testing.T, s *memstore.Store, runID string) *domain.DriverRun {
	t.Helper()
	ctx := t.Context()
	if _, err := s.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: matcherWS, RunID: runID, DriverID: "awaiter", DriverVersionID: "awaiter-v1", Entrypoint: "run",
	}); err != nil {
		t.Fatalf("Create run %s: %v", runID, err)
	}
	run, err := s.DriverRuns().Claim(ctx, matcherWS, runID, "node-1", "lease-"+runID)
	if err != nil {
		t.Fatalf("Claim run %s: %v", runID, err)
	}
	return run
}

// registerPendingAwait registers run's first await and asserts it pending.
func registerPendingAwait(t *testing.T, s *memstore.Store, runID, pattern string, actorAllow []string) string {
	t.Helper()
	key := domain.AwaitInstanceKey(runID, 1)
	res, err := s.Awaits().RegisterAwaitAndCheck(t.Context(), matcherWS, store.AwaitRegistration{
		InstanceKey: key, RunID: runID, Pattern: pattern, ActorAllow: actorAllow,
		Deadline: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("RegisterAwaitAndCheck(%s): %v", key, err)
	}
	if res.Satisfied {
		t.Fatalf("await %s satisfied at registration, want pending", key)
	}
	return key
}

func suspendRun(t *testing.T, s *memstore.Store, run *domain.DriverRun, instanceKey string) {
	t.Helper()
	if _, err := s.DriverRuns().Suspend(t.Context(), matcherWS, run.RunID,
		run.NodeID, run.LeaseID, run.FencingToken, instanceKey); err != nil {
		t.Fatalf("Suspend run %s: %v", run.RunID, err)
	}
}

// newSuspendedAwaitRun is the standard fixture: a claimed run, pending await,
// suspended run. Returns the await instance key.
func newSuspendedAwaitRun(t *testing.T, s *memstore.Store, runID, pattern string, actorAllow []string) string {
	t.Helper()
	run := createClaimedRun(t, s, runID)
	key := registerPendingAwait(t, s, runID, pattern, actorAllow)
	suspendRun(t, s, run, key)
	return key
}

func runStatus(t *testing.T, s *memstore.Store, runID string) *domain.DriverRun {
	t.Helper()
	run, err := s.DriverRuns().Get(t.Context(), matcherWS, runID)
	if err != nil {
		t.Fatalf("Get run %s: %v", runID, err)
	}
	return run
}

func pendingAwaits(t *testing.T, s *memstore.Store, pattern string) []*domain.AwaitInstance {
	t.Helper()
	awaits, err := s.Awaits().ListAwaitsByPattern(t.Context(), matcherWS, pattern)
	if err != nil {
		t.Fatalf("ListAwaitsByPattern(%s): %v", pattern, err)
	}
	return awaits
}

func newAwaitMatcher(t testing.TB, st store.Store) *trigger.AwaitMatcher {
	t.Helper()
	resolver, ok := st.Awaits().(store.AtomicAwaitStore)
	if !ok {
		t.Fatalf("await store %T does not implement store.AtomicAwaitStore", st.Awaits())
	}
	return trigger.NewAwaitMatcherWithResolver(st.Awaits(), st.DriverRuns(), resolver)
}

type atomicFailureStore struct {
	store.Store
	awaits *atomicFailureAwaitStore
}

func newAtomicFailureStore(inner store.Store, mode string) *atomicFailureStore {
	awaits := inner.Awaits()
	return &atomicFailureStore{
		Store: inner,
		awaits: &atomicFailureAwaitStore{
			AwaitStore: awaits,
			atomic:     awaits.(store.AtomicAwaitStore),
			mode:       mode,
		},
	}
}

func (s *atomicFailureStore) Awaits() store.AwaitStore { return s.awaits }

type atomicFailureAwaitStore struct {
	store.AwaitStore
	atomic store.AtomicAwaitStore
	mode   string
	mu     sync.Mutex
	failed bool
}

func (s *atomicFailureAwaitStore) ResolveAwaitAndResume(
	ctx context.Context,
	ws, instanceKey, eventID string,
	payload json.RawMessage,
	actor string,
) error {
	s.mu.Lock()
	first := !s.failed
	if first {
		s.failed = true
	}
	s.mu.Unlock()
	if first && s.mode == "pre_commit" {
		return errors.New("injected pre-commit atomic command failure")
	}
	err := s.atomic.ResolveAwaitAndResume(ctx, ws, instanceKey, eventID, payload, actor)
	if err != nil {
		return err
	}
	if first && s.mode == "post_commit" {
		return errors.New("injected lost response after atomic commit")
	}
	return nil
}

func TestAwaitMatcherDispatchResolvesAndResumes(t *testing.T) {
	s := memstore.New()
	seedAwaitMatcherCatalog(t, s)
	pattern := domain.AwaitEventKey("pull_request", "acme/widgets#7")
	key := newSuspendedAwaitRun(t, s, "run-a", pattern, nil)

	matcher := newAwaitMatcher(t, s)
	payload := json.RawMessage(`{"action":"opened"}`)
	result, err := matcher.Dispatch(t.Context(), matcherWS, trigger.AwaitDispatchEvent{
		EventID: "evt-1", EventType: "pull_request", SubjectRef: "acme/widgets#7",
		ActorRef: "octocat", Payload: payload,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.SubjectKey != pattern || result.Resolved() != 1 || len(result.Records) != 1 {
		t.Fatalf("result = %+v, want one resolved record on key %q", result, pattern)
	}
	rec := result.Records[0]
	if rec.Outcome != trigger.AwaitMatchResolved || rec.InstanceKey != key || rec.RunID != "run-a" {
		t.Fatalf("record = %+v, want resolved %s", rec, key)
	}

	run := runStatus(t, s, "run-a")
	if run.Status != domain.DriverRunQueued || run.ResumeSourceEventID != "evt-1" {
		t.Fatalf("run = %s/%s, want queued resumed by evt-1", run.Status, run.ResumeSourceEventID)
	}
	satisfied, err := s.Awaits().GetSatisfiedAwait(t.Context(), matcherWS, key)
	if err != nil {
		t.Fatalf("GetSatisfiedAwait: %v", err)
	}
	if satisfied.Status != domain.AwaitSatisfied || satisfied.SatisfiedByEventID != "evt-1" ||
		!bytes.Equal(satisfied.SatisfiedPayload, payload) {
		t.Fatalf("satisfied row = %+v, want evt-1 with payload inline", satisfied)
	}
}

func TestAwaitMatcherWithoutExplicitResolverFailsClosed(t *testing.T) {
	s := memstore.New()
	seedAwaitMatcherCatalog(t, s)
	pattern := domain.AwaitEventKey("approval.granted", "deploy-no-resolver")
	key := newSuspendedAwaitRun(t, s, "run-no-resolver", pattern, []string{"alice"})

	matcher := &trigger.AwaitMatcher{AwaitStore: s.Awaits(), DriverRunStore: s.DriverRuns()}
	result, err := matcher.Dispatch(t.Context(), matcherWS, trigger.AwaitDispatchEvent{
		EventID: "evt-no-resolver", EventType: "approval.granted",
		SubjectRef: "deploy-no-resolver", ActorRef: "alice",
	})
	if err == nil || result.Resolved() != 0 || len(result.Records) != 1 ||
		result.Records[0].Outcome != trigger.AwaitMatchFailed {
		t.Fatalf("Dispatch = %+v, %v; want one failed record", result, err)
	}
	if got := pendingAwaits(t, s, pattern); len(got) != 1 || got[0].InstanceKey != key {
		t.Fatalf("pending awaits = %+v, want %s untouched", got, key)
	}
	if run := runStatus(t, s, "run-no-resolver"); run.Status != domain.DriverRunSuspendedAwaitingEvent {
		t.Fatalf("run status = %s, want suspended", run.Status)
	}
}

func TestAwaitMatcherAtomicPreCommitFailureRedispatchConverges(t *testing.T) {
	s := memstore.New()
	seedAwaitMatcherCatalog(t, s)
	pattern := domain.AwaitEventKey("approval.granted", "deploy-atomic-pre")
	key := newSuspendedAwaitRun(t, s, "run-atomic-pre", pattern, []string{"alice"})
	matcher := newAwaitMatcher(t, newAtomicFailureStore(s, "pre_commit"))
	event := trigger.AwaitDispatchEvent{
		EventID: "evt-atomic-pre", EventType: "approval.granted", SubjectRef: "deploy-atomic-pre", ActorRef: "alice",
	}

	first, err := matcher.Dispatch(t.Context(), matcherWS, event)
	if err == nil || len(first.Records) != 1 || first.Records[0].Outcome != trigger.AwaitMatchFailed {
		t.Fatalf("first dispatch = %+v, %v; want injected failure", first, err)
	}
	if got := pendingAwaits(t, s, pattern); len(got) != 1 || got[0].InstanceKey != key {
		t.Fatalf("await after pre-commit failure = %+v, want %s pending/indexed", got, key)
	}
	if run := runStatus(t, s, "run-atomic-pre"); run.Status != domain.DriverRunSuspendedAwaitingEvent {
		t.Fatalf("run after pre-commit failure = %s, want suspended", run.Status)
	}

	second, err := matcher.Dispatch(t.Context(), matcherWS, event)
	if err != nil || second.Resolved() != 1 {
		t.Fatalf("exact redispatch = %+v, %v; want one resolved", second, err)
	}
	if run := runStatus(t, s, "run-atomic-pre"); run.Status != domain.DriverRunQueued || run.ResumeSourceEventID != event.EventID {
		t.Fatalf("run after exact redispatch = %s/%s, want queued by %s", run.Status, run.ResumeSourceEventID, event.EventID)
	}
}

func TestAwaitMatcherAtomicLostResponseIsAlreadyConverged(t *testing.T) {
	s := memstore.New()
	seedAwaitMatcherCatalog(t, s)
	pattern := domain.AwaitEventKey("approval.granted", "deploy-atomic-post")
	key := newSuspendedAwaitRun(t, s, "run-atomic-post", pattern, []string{"alice"})
	matcher := newAwaitMatcher(t, newAtomicFailureStore(s, "post_commit"))
	event := trigger.AwaitDispatchEvent{
		EventID: "evt-atomic-post", EventType: "approval.granted", SubjectRef: "deploy-atomic-post", ActorRef: "alice",
	}

	first, err := matcher.Dispatch(t.Context(), matcherWS, event)
	if err == nil || len(first.Records) != 1 || first.Records[0].Outcome != trigger.AwaitMatchFailed {
		t.Fatalf("first dispatch = %+v, %v; want injected lost response", first, err)
	}
	satisfied, readErr := s.Awaits().GetSatisfiedAwait(t.Context(), matcherWS, key)
	if readErr != nil || satisfied.SatisfiedByEventID != event.EventID {
		t.Fatalf("await after lost response = %+v, %v", satisfied, readErr)
	}
	if run := runStatus(t, s, "run-atomic-post"); run.Status != domain.DriverRunQueued || run.ResumeSourceEventID != event.EventID {
		t.Fatalf("run after lost response = %s/%s, want already queued by %s", run.Status, run.ResumeSourceEventID, event.EventID)
	}

	second, err := matcher.Dispatch(t.Context(), matcherWS, event)
	if err != nil || len(second.Records) != 0 {
		t.Fatalf("exact redispatch = %+v, %v; want stable no-op", second, err)
	}
	if run := runStatus(t, s, "run-atomic-post"); run.Status != domain.DriverRunQueued || run.ResumeSourceEventID != event.EventID {
		t.Fatalf("run after exact redispatch = %s/%s, want stable queued", run.Status, run.ResumeSourceEventID)
	}
}

// TestAwaitMatcherExactKeyNearMisses: RULE 1 — only the exact rendered key
// matches; same PR in another repo, another entity in the same repo, another
// event type, and glob-looking patterns never fire.
func TestAwaitMatcherExactKeyNearMisses(t *testing.T) {
	tests := []struct {
		name              string
		pattern           string
		eventType, subjct string
	}{
		{"same PR different repo", "pull_request:acme/widgets#7", "pull_request", "acme/gadgets#7"},
		{"same repo different PR", "pull_request:acme/widgets#7", "pull_request", "acme/widgets#8"},
		{"same subject different type", "pull_request:acme/widgets#7", "issue_comment", "acme/widgets#7"},
		{"sha-qualified pattern, different sha", "checks:acme/widgets#7@sha-a", "checks", "acme/widgets#7@sha-b"},
		{"glob is a literal, not a wildcard", "pull_request:acme/*", "pull_request", "acme/widgets#7"},
		{"prefix never matches", "pull_request:acme/widgets", "pull_request", "acme/widgets#7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := memstore.New()
			seedAwaitMatcherCatalog(t, s)
			key := newSuspendedAwaitRun(t, s, "run-miss", tt.pattern, nil)

			matcher := newAwaitMatcher(t, s)
			result, err := matcher.Dispatch(t.Context(), matcherWS, trigger.AwaitDispatchEvent{
				EventID: "evt-near", EventType: tt.eventType, SubjectRef: tt.subjct, ActorRef: "octocat",
			})
			if err != nil || len(result.Records) != 0 {
				t.Fatalf("Dispatch = %+v, %v; want zero matches", result, err)
			}
			if got := pendingAwaits(t, s, tt.pattern); len(got) != 1 || got[0].InstanceKey != key {
				t.Fatalf("await index = %+v, want %s still pending", got, key)
			}
			if run := runStatus(t, s, "run-miss"); run.Status != domain.DriverRunSuspendedAwaitingEvent {
				t.Fatalf("run status = %s, want still suspended", run.Status)
			}
		})
	}
}

// TestAwaitMatcherActorPredicate: RULE 4 — a denied actor is an audited
// no-op (no resolve, no resume, no payload); allowed and empty predicates
// resolve.
func TestAwaitMatcherActorPredicate(t *testing.T) {
	pattern := domain.AwaitEventKey("approval.granted", "deploy-42")
	tests := []struct {
		name       string
		actorAllow []string
		actor      string
		want       trigger.AwaitMatchOutcome
	}{
		{"allowed actor resolves", []string{"alice", "bob"}, "bob", trigger.AwaitMatchResolved},
		{"empty predicate allows any actor", nil, "mallory", trigger.AwaitMatchResolved},
		{"denied actor is rejected", []string{"alice", "bob"}, "mallory", trigger.AwaitMatchActorRejected},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := memstore.New()
			seedAwaitMatcherCatalog(t, s)
			key := newSuspendedAwaitRun(t, s, "run-actor", pattern, tt.actorAllow)

			matcher := newAwaitMatcher(t, s)
			result, err := matcher.Dispatch(t.Context(), matcherWS, trigger.AwaitDispatchEvent{
				EventID: "evt-act", EventType: "approval.granted", SubjectRef: "deploy-42",
				ActorRef: tt.actor, Payload: json.RawMessage(`{"approved":true}`),
			})
			if err != nil || len(result.Records) != 1 || result.Records[0].Outcome != tt.want {
				t.Fatalf("Dispatch = %+v, %v; want one %s record", result, err, tt.want)
			}
			run := runStatus(t, s, "run-actor")
			if tt.want == trigger.AwaitMatchResolved {
				if run.Status != domain.DriverRunQueued {
					t.Fatalf("run status = %s, want queued", run.Status)
				}
				return
			}
			// Denied: await still pending, run untouched, no payload recorded.
			if run.Status != domain.DriverRunSuspendedAwaitingEvent {
				t.Fatalf("run status = %s, want still suspended after actor rejection", run.Status)
			}
			if got := pendingAwaits(t, s, pattern); len(got) != 1 {
				t.Fatalf("pending awaits = %d, want 1 (rejection must not resolve)", len(got))
			}
			if _, err := s.Awaits().GetSatisfiedAwait(t.Context(), matcherWS, key); err == nil {
				t.Fatal("GetSatisfiedAwait succeeded after actor rejection, want not-found")
			}
			// A later eligible actor still resolves the same await.
			late, err := matcher.Dispatch(t.Context(), matcherWS, trigger.AwaitDispatchEvent{
				EventID: "evt-act-2", EventType: "approval.granted", SubjectRef: "deploy-42", ActorRef: "alice",
			})
			if err != nil || late.Resolved() != 1 {
				t.Fatalf("eligible re-dispatch = %+v, %v; want resolved", late, err)
			}
		})
	}
}

func TestAwaitMatcherReservedRunFinishedProvenanceIsNoOpAndLaterGenuineEventWins(t *testing.T) {
	s := memstore.New()
	seedAwaitMatcherCatalog(t, s)
	pattern := domain.AwaitEventKey(eventpolicy.RunFinishedEventType, "child-1")
	key := newSuspendedAwaitRun(t, s, "run-parent", pattern, []string{eventpolicy.RunFinishedActorRef})
	matcher := newAwaitMatcher(t, s)

	for _, forged := range []trigger.AwaitDispatchEvent{
		{
			EventID:   eventpolicy.RunFinishedSourceEventIDPrefix + "child-1:completed",
			EventType: eventpolicy.RunFinishedEventType, SourceKind: "github",
			Origin: domain.TriggerEventOriginExternal, SubjectRef: "child-1",
			ActorRef: eventpolicy.RunFinishedActorRef,
		},
		{
			EventID:   eventpolicy.RunFinishedSourceEventIDPrefix + "child-1:completed",
			EventType: eventpolicy.RunFinishedEventType, SourceKind: eventpolicy.SourceKindInternal,
			Origin: domain.TriggerEventOriginWorkflow, SubjectRef: "child-1",
			ActorRef: eventpolicy.RunFinishedActorRef,
		},
	} {
		result, err := matcher.Dispatch(t.Context(), matcherWS, forged)
		if err != nil || result.Resolved() != 0 || len(result.Records) != 0 {
			t.Fatalf("forged Dispatch = %+v, %v; want audited successful no-op", result, err)
		}
	}
	if got := pendingAwaits(t, s, pattern); len(got) != 1 || got[0].InstanceKey != key {
		t.Fatalf("await after forged events = %+v, want %s pending", got, key)
	}
	if run := runStatus(t, s, "run-parent"); run.Status != domain.DriverRunSuspendedAwaitingEvent {
		t.Fatalf("run after forged events = %s, want suspended", run.Status)
	}

	genuine := trigger.AwaitDispatchEvent{
		EventID:   eventpolicy.RunFinishedSourceEventIDPrefix + "child-1:completed",
		EventType: eventpolicy.RunFinishedEventType, SourceKind: eventpolicy.SourceKindExecution,
		Origin: domain.TriggerEventOriginSystem, SubjectRef: "child-1",
		ActorRef: eventpolicy.RunFinishedActorRef,
		Payload:  json.RawMessage(`{"runId":"child-1","status":"completed"}`),
	}
	result, err := matcher.Dispatch(t.Context(), matcherWS, genuine)
	if err != nil || result.Resolved() != 1 {
		t.Fatalf("genuine Dispatch = %+v, %v; want one resolution", result, err)
	}
	if run := runStatus(t, s, "run-parent"); run.Status != domain.DriverRunQueued || run.ResumeSourceEventID != genuine.EventID {
		t.Fatalf("run after genuine event = %s/%s, want queued by %s", run.Status, run.ResumeSourceEventID, genuine.EventID)
	}
}

func TestAwaitMatcherRejectsReservedActorOnOrdinaryNonSystemEvent(t *testing.T) {
	s := memstore.New()
	seedAwaitMatcherCatalog(t, s)
	pattern := domain.AwaitEventKey("approval.granted", "deploy-reserved")
	newSuspendedAwaitRun(t, s, "run-reserved-actor", pattern, []string{"system:approver"})
	result, err := newAwaitMatcher(t, s).Dispatch(t.Context(), matcherWS, trigger.AwaitDispatchEvent{
		EventID: "approval-1", EventType: "approval.granted", SourceKind: "github",
		Origin: domain.TriggerEventOriginExternal, SubjectRef: "deploy-reserved", ActorRef: "system:approver",
	})
	if err != nil || result.Resolved() != 0 || len(result.Records) != 0 {
		t.Fatalf("reserved actor Dispatch = %+v, %v; want audited successful no-op", result, err)
	}
	if got := pendingAwaits(t, s, pattern); len(got) != 1 {
		t.Fatalf("pending awaits = %+v, want one", got)
	}
}

// TestAwaitMatcherMultiWaiter: locked decision — one event resolves ALL
// pending instances on the same rendered key.
func TestAwaitMatcherMultiWaiter(t *testing.T) {
	s := memstore.New()
	seedAwaitMatcherCatalog(t, s)
	pattern := domain.AwaitEventKey("issue.closed", "issue#9")
	newSuspendedAwaitRun(t, s, "run-w1", pattern, nil)
	newSuspendedAwaitRun(t, s, "run-w2", pattern, nil)

	matcher := newAwaitMatcher(t, s)
	result, err := matcher.Dispatch(t.Context(), matcherWS, trigger.AwaitDispatchEvent{
		EventID: "evt-multi", EventType: "issue.closed", SubjectRef: "issue#9", ActorRef: "alice",
	})
	if err != nil || result.Resolved() != 2 {
		t.Fatalf("Dispatch = %+v, %v; want both waiters resolved", result, err)
	}
	for _, runID := range []string{"run-w1", "run-w2"} {
		if run := runStatus(t, s, runID); run.Status != domain.DriverRunQueued || run.ResumeSourceEventID != "evt-multi" {
			t.Fatalf("run %s = %s/%s, want queued by evt-multi", runID, run.Status, run.ResumeSourceEventID)
		}
	}
	if got := pendingAwaits(t, s, pattern); len(got) != 0 {
		t.Fatalf("pending awaits after multi-resolve = %d, want 0", len(got))
	}
}

// TestAwaitMatcherConcurrentEventRace: two racing events on the same key
// yield exactly one resolution; the loser is a recorded no-op.
func TestAwaitMatcherConcurrentEventRace(t *testing.T) {
	s := memstore.New()
	seedAwaitMatcherCatalog(t, s)
	pattern := domain.AwaitEventKey("pr.merged", "acme/widgets#3")
	key := newSuspendedAwaitRun(t, s, "run-race", pattern, nil)

	matcher := newAwaitMatcher(t, s)
	results := make([]*trigger.AwaitDispatchResult, 2)
	var wg sync.WaitGroup
	for i, eventID := range []string{"evt-race-a", "evt-race-b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := matcher.Dispatch(context.Background(), matcherWS, trigger.AwaitDispatchEvent{
				EventID: eventID, EventType: "pr.merged", SubjectRef: "acme/widgets#3", ActorRef: "alice",
			})
			if err != nil {
				t.Errorf("Dispatch(%s): %v", eventID, err)
			}
			results[i] = result
		}()
	}
	wg.Wait()

	resolved := results[0].Resolved() + results[1].Resolved()
	if resolved != 1 {
		t.Fatalf("resolved count across racers = %d, want exactly 1 (results: %+v / %+v)", resolved, results[0], results[1])
	}
	satisfied, err := s.Awaits().GetSatisfiedAwait(t.Context(), matcherWS, key)
	if err != nil {
		t.Fatalf("GetSatisfiedAwait: %v", err)
	}
	run := runStatus(t, s, "run-race")
	if run.Status != domain.DriverRunQueued || run.ResumeSourceEventID != satisfied.SatisfiedByEventID {
		t.Fatalf("run = %s/%s, want queued by winner %s", run.Status, run.ResumeSourceEventID, satisfied.SatisfiedByEventID)
	}
}

// TestAwaitMatcherTimeoutWinnerStands: an await the deadline sweeper already
// timed out is an idempotent no-op for a late event — the timed_out record
// (and the run's timeout resume source) stay intact.
func TestAwaitMatcherTimeoutWinnerStands(t *testing.T) {
	s := memstore.New()
	seedAwaitMatcherCatalog(t, s)
	pattern := domain.AwaitEventKey("review.submitted", "acme/widgets#5")
	key := newSuspendedAwaitRun(t, s, "run-to", pattern, nil)

	timeoutEvent := domain.AwaitTimeoutEventIDPrefix + "1"
	if _, err := s.Awaits().ResolveAwait(t.Context(), matcherWS, key, timeoutEvent, nil, "system"); err != nil {
		t.Fatalf("ResolveAwait(timeout): %v", err)
	}
	if _, err := s.DriverRuns().ResumeAwaiting(t.Context(), matcherWS, "run-to", key, timeoutEvent); err != nil {
		t.Fatalf("ResumeAwaiting(timeout): %v", err)
	}

	matcher := newAwaitMatcher(t, s)
	result, err := matcher.Dispatch(t.Context(), matcherWS, trigger.AwaitDispatchEvent{
		EventID: "evt-late", EventType: "review.submitted", SubjectRef: "acme/widgets#5", ActorRef: "alice",
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	// The resolved await left the pattern index, so the late event sees no
	// candidates (or, if it raced the index drop, a recorded no-op loser).
	if result.Resolved() != 0 {
		t.Fatalf("late event resolved %d awaits, want 0: %+v", result.Resolved(), result)
	}
	satisfied, err := s.Awaits().GetSatisfiedAwait(t.Context(), matcherWS, key)
	if err != nil {
		t.Fatalf("GetSatisfiedAwait: %v", err)
	}
	if satisfied.Status != domain.AwaitTimedOut || satisfied.SatisfiedByEventID != timeoutEvent {
		t.Fatalf("timeout record = %+v, want timed_out by %s intact", satisfied, timeoutEvent)
	}
	if run := runStatus(t, s, "run-to"); run.ResumeSourceEventID != timeoutEvent {
		t.Fatalf("run resume source = %s, want timeout winner %s", run.ResumeSourceEventID, timeoutEvent)
	}
}

// TestAwaitMatcherPendingSuspendWindowRetry: an event landing between
// registration and suspend atomically writes the pending-resume marker, so
// suspend refuses and execution continues inline.
func TestAwaitMatcherPendingSuspendWindowRetry(t *testing.T) {
	s := memstore.New()
	seedAwaitMatcherCatalog(t, s)
	pattern := domain.AwaitEventKey("build.finished", "build-7")
	run := createClaimedRun(t, s, "run-window")
	key := registerPendingAwait(t, s, "run-window", pattern, nil) // run still RUNNING

	matcher := newAwaitMatcher(t, s)
	result, err := matcher.Dispatch(t.Context(), matcherWS, trigger.AwaitDispatchEvent{
		EventID: "evt-window", EventType: "build.finished", SubjectRef: "build-7", ActorRef: "ci",
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if result.Resolved() != 1 {
		t.Fatalf("result = %+v, want atomic pending-resume", result)
	}
	if _, err := s.DriverRuns().Suspend(t.Context(), matcherWS, run.RunID,
		run.NodeID, run.LeaseID, run.FencingToken, key); !errors.Is(err, domain.ErrDriverRunAlreadyResumed) {
		t.Fatalf("Suspend after atomic window resolution = %v, want ErrDriverRunAlreadyResumed", err)
	}
	if got := runStatus(t, s, "run-window"); got.Status != domain.DriverRunRunning || got.ResumeSourceEventID != "evt-window" {
		t.Fatalf("run = %s/%s, want running with pending-resume marker", got.Status, got.ResumeSourceEventID)
	}
}

// TestAwaitMatcherResumeInvalidKeepsResolution: a resume blocked for good
// (here: the run finished before the event arrived) leaves the satisfied
// record standing — the re-entry replay contract.
func TestAwaitMatcherResumeInvalidKeepsResolution(t *testing.T) {
	s := memstore.New()
	seedAwaitMatcherCatalog(t, s)
	pattern := domain.AwaitEventKey("external.signal", "sig-1")
	run := createClaimedRun(t, s, "run-term")
	key := registerPendingAwait(t, s, "run-term", pattern, nil)
	if _, err := s.DriverRuns().Finish(t.Context(), matcherWS, "run-term", store.DriverRunFinish{
		NodeID: run.NodeID, LeaseID: run.LeaseID, FencingToken: run.FencingToken,
		Status: domain.DriverRunCompleted, Summary: "done before signal",
	}); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	matcher := newAwaitMatcher(t, s)
	result, err := matcher.Dispatch(t.Context(), matcherWS, trigger.AwaitDispatchEvent{
		EventID: "evt-term", EventType: "external.signal", SubjectRef: "sig-1", ActorRef: "alice",
	})
	if err != nil || len(result.Records) != 1 {
		t.Fatalf("Dispatch = %+v, %v; want one record", result, err)
	}
	rec := result.Records[0]
	if rec.Outcome != trigger.AwaitMatchResumeDeferred || rec.Reason != trigger.AwaitReasonRunTerminal {
		t.Fatalf("record = %+v, want resume_deferred/run_terminal", rec)
	}
	satisfied, err := s.Awaits().GetSatisfiedAwait(t.Context(), matcherWS, key)
	if err != nil || satisfied.SatisfiedByEventID != "evt-term" {
		t.Fatalf("satisfied = %+v, %v; want resolution standing by evt-term", satisfied, err)
	}
	if got := runStatus(t, s, "run-term"); got.Status != domain.DriverRunCompleted {
		t.Fatalf("run status = %s, want completed untouched", got.Status)
	}
}

// TestAwaitMatcherPayloadCap: an oversized event payload fails closed and
// leaves both the await and suspended parent unchanged.
func TestAwaitMatcherPayloadCap(t *testing.T) {
	s := memstore.New()
	seedAwaitMatcherCatalog(t, s)
	pattern := domain.AwaitEventKey("bulk.imported", "batch-1")
	key := newSuspendedAwaitRun(t, s, "run-big", pattern, nil)

	big, err := json.Marshal(map[string]string{"blob": strings.Repeat("x", domain.DefaultAwaitResumePayloadCap)})
	if err != nil {
		t.Fatalf("marshal oversized payload: %v", err)
	}
	matcher := newAwaitMatcher(t, s)
	result, err := matcher.Dispatch(t.Context(), matcherWS, trigger.AwaitDispatchEvent{
		EventID: "evt-big", EventType: "bulk.imported", SubjectRef: "batch-1", ActorRef: "alice", Payload: big,
	})
	if err == nil || result.Resolved() != 0 || len(result.Records) != 1 || result.Records[0].Outcome != trigger.AwaitMatchFailed {
		t.Fatalf("Dispatch = %+v, %v; want failed oversized resolution", result, err)
	}
	if satisfied, getErr := s.Awaits().GetSatisfiedAwait(t.Context(), matcherWS, key); !errors.Is(getErr, domain.ErrNotFound) {
		t.Fatalf("satisfied = %+v, %v; want pending/not found", satisfied, getErr)
	}
	if got := runStatus(t, s, "run-big"); got.Status != domain.DriverRunSuspendedAwaitingEvent {
		t.Fatalf("run status = %s, want suspended", got.Status)
	}
}

// TestAwaitMatcherSkipsSubjectlessEvents: RULE 1 — no subject, no rendered
// key, structurally unmatched.
func TestAwaitMatcherSkipsSubjectlessEvents(t *testing.T) {
	s := memstore.New()
	matcher := newAwaitMatcher(t, s)
	result, err := matcher.Dispatch(t.Context(), matcherWS, trigger.AwaitDispatchEvent{
		EventID: "evt-bare", EventType: "ping",
	})
	if err != nil || result.SubjectKey != "" || len(result.Records) != 0 {
		t.Fatalf("Dispatch = %+v, %v; want silent no-op", result, err)
	}
}

// TestInternalSourceEmitResolvesAwait: the loopback hook — a workflow-emitted
// internal event resumes a run awaiting its rendered key, with the emitter's
// payload (not the provenance envelope) on the satisfied row.
func TestInternalSourceEmitResolvesAwait(t *testing.T) {
	s := memstore.New()
	setupInternalBinding(t, s)
	seedAwaitMatcherCatalog(t, s)
	key := newSuspendedAwaitRun(t, s, "run-loop", domain.AwaitEventKey("issue.created", "issue#42"), nil)

	src := &trigger.InternalSource{Store: s, AwaitResolver: s.Awaits().(store.AtomicAwaitStore)}
	payload := json.RawMessage(`{"issueId":"42"}`)
	if _, err := src.Emit(t.Context(), matcherWS, trigger.InternalEvent{
		EventID: "emit-await-1", EventType: "issue.create", SubjectRef: "issue#42",
		ActorRef: "driver:run-emitter", EmittedByRunID: "run-emitter", Payload: payload,
	}); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	if run := runStatus(t, s, "run-loop"); run.Status != domain.DriverRunQueued || run.ResumeSourceEventID != "emit-await-1" {
		t.Fatalf("run = %s/%s, want queued by emit-await-1", run.Status, run.ResumeSourceEventID)
	}
	satisfied, err := s.Awaits().GetSatisfiedAwait(t.Context(), matcherWS, key)
	if err != nil || !bytes.Equal(satisfied.SatisfiedPayload, payload) {
		t.Fatalf("satisfied = %+v, %v; want emitter payload inline", satisfied, err)
	}
}

// TestCronTickResolvesAwait: the cron hook — an await pending on
// "cron.tick:{bindingID}" resumes on the binding's next due tick.
func TestCronTickResolvesAwait(t *testing.T) {
	s := memstore.New()
	setupCronBindings(t, s, []cronBinding{
		{bindingID: "cron-nightly", routeKey: "cron.report", schedule: "* * * * *"},
	})
	seedAwaitMatcherCatalog(t, s)
	key := newSuspendedAwaitRun(t, s, "run-cron", domain.AwaitEventKey(trigger.CronEventType, "cron-nightly"), nil)

	sched := &trigger.CronScheduler{
		Store: s, AwaitResolver: s.Awaits().(store.AtomicAwaitStore), WorkspaceKey: matcherWS,
	}
	base := time.Now().UTC().Truncate(time.Minute)
	if _, err := sched.RunOnce(t.Context(), base); err != nil { // primes the window
		t.Fatalf("RunOnce(prime): %v", err)
	}
	result, err := sched.RunOnce(t.Context(), base.Add(90*time.Second))
	if err != nil || result.Fired != 1 {
		t.Fatalf("RunOnce(fire) = %+v, %v; want one fired tick", result, err)
	}

	run := runStatus(t, s, "run-cron")
	if run.Status != domain.DriverRunQueued || !strings.HasPrefix(run.ResumeSourceEventID, "cron:cron-nightly:") {
		t.Fatalf("run = %s/%s, want queued by a cron-nightly tick", run.Status, run.ResumeSourceEventID)
	}
	if _, err := s.Awaits().GetSatisfiedAwait(t.Context(), matcherWS, key); err != nil {
		t.Fatalf("GetSatisfiedAwait: %v", err)
	}
}
