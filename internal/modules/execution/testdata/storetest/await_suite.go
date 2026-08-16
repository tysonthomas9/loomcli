// Package storetest exports test-only owner-port conformance suites so every
// Execution persistence adapter (memstore in-process, FleetDB over HTTP) proves the
// same semantics with the same cases — the await suite here is shared by
// chunk AW4 (memstore) and the AW5 fleetdb client against AW2/AW3 storage.
package storetest

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/execution"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

// AwaitHarness wires one backend's await surfaces into the shared suite.
// Every subtest gets a fresh harness, so backends should return an isolated
// store (or workspace) per call.
type AwaitHarness struct {
	// Workspace is the workspace key the harness operates in.
	Workspace string
	// Awaits is the AwaitStore under test.
	Awaits execution.AwaitStore
	// AppendEvent journals one trigger event — the registration-scan source
	// (trigger-event journal + run.finished decision) — returning its
	// event ID.
	AppendEvent func(t testing.TB, eventType, subjectRef, actorRef string) string
	// Runs wires the suspended-run lifecycle; nil skips those subtests
	// (await-index-only backends).
	Runs *AwaitRunHarness
}

// AwaitRunHarness wires the DriverRun suspend/resume lifecycle. Suspend and
// Resume are funcs rather than an interface because the store-level methods
// land per backend ahead of the shared client interface (AW5).
type AwaitRunHarness struct {
	// Store serves Claim/Heartbeat/Get/RecoverStale for the guard tests.
	Store execution.DriverRunStore
	// NewRun creates a queued run, with parentRunID linking composition
	// children ("" = detached), and returns it.
	NewRun func(t testing.TB, runID, parentRunID string) *execution.DriverRunRecord
	// Suspend suspends a running run (owner-fenced running -> suspended).
	Suspend func(ctx context.Context, runID, nodeID, leaseID string, fencingToken int64) (*execution.DriverRunRecord, error)
	// Resume re-queues a suspended run with its resume-source event.
	Resume func(ctx context.Context, runID, resumeSourceEventID string) (*execution.DriverRunRecord, error)
}

// RunAwaitConformance runs the await semantics every backend must satisfy
// identically: atomic register-and-check (RULE 2, the lost-wakeup fix), exact
// pattern equality (RULE 1), replay determinism (RULE 3), actor filtering at
// scan (RULE 4), mandatory deadlines (RULE 5), resolve-ALL multi-waiter, and
// the suspended-run lifecycle guards.
func RunAwaitConformance(t *testing.T, newHarness func(t testing.TB) *AwaitHarness) {
	t.Helper()
	cases := []struct {
		name string
		run  func(t *testing.T, h *AwaitHarness)
	}{
		{"RegisterLeavesPendingWithoutMatch", testRegisterLeavesPendingWithoutMatch},
		{"EventBeforeRegistrationSatisfiesImmediately", testEventBeforeRegistrationSatisfies},
		{"EventAfterPendingRegistrationStaysPendingForDispatch", testEventAfterPendingRegistrationStaysPending},
		{"SatisfiedRegistrationReplaysSameEvent", testSatisfiedRegistrationReplays},
		{"PendingRegistrationIdempotent", testPendingRegistrationIdempotent},
		{"ActorFilterAtScan", testActorFilterAtScan},
		{"PatternExactEqualityNoGlob", testPatternExactEqualityNoGlob},
		{"RegistrationValidation", testRegistrationValidation},
		{"ResolveFirstWinnerPersistsPayload", testResolveFirstWinnerPersistsPayload},
		{"ResolveTimeoutEventLandsTimedOut", testResolveTimeoutLandsTimedOut},
		{"ResolveMissingNotFound", testResolveMissingNotFound},
		{"ResolvePayloadCapRejected", testResolvePayloadCapRejected},
		{"MultiWaiterListByPattern", testMultiWaiterListByPattern},
		{"DueDeadlinesOrderedAndCapped", testDueDeadlinesOrderedAndCapped},
		{"SuspendRequiresRunningOwner", testSuspendRequiresRunningOwner},
		{"SuspendReleasesSlotIdempotently", testSuspendReleasesSlotIdempotently},
		{"HeartbeatRejectsSuspended", testHeartbeatRejectsSuspended},
		{"ClaimRejectsSuspended", testClaimRejectsSuspended},
		{"StaleSweepSkipsSuspended", testStaleSweepSkipsSuspended},
		{"ResumeOnlyFromSuspendedFirstWinner", testResumeOnlyFromSuspendedFirstWinner},
		{"ParentRunIDRoundTrip", testParentRunIDRoundTrip},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) { tc.run(t, newHarness(t)) })
	}
}

// awaitReg builds a valid registration for the n-th await of runID with an
// hour-long deadline.
func awaitReg(runID string, n int, pattern string) execution.AwaitRegistration {
	return execution.AwaitRegistration{
		InstanceKey: execution.AwaitInstanceKey(runID, n),
		RunID:       runID,
		Pattern:     pattern,
		Deadline:    time.Now().Add(time.Hour).UTC(),
	}
}

func mustRegister(t *testing.T, ctx context.Context, h *AwaitHarness, in execution.AwaitRegistration) *execution.AwaitRegistrationResult {
	t.Helper()
	res, err := h.Awaits.RegisterAwaitAndCheck(ctx, h.Workspace, in)
	if err != nil {
		t.Fatalf("RegisterAwaitAndCheck(%s): %v", in.InstanceKey, err)
	}
	return res
}

func mustListPattern(t *testing.T, ctx context.Context, h *AwaitHarness, pattern string) []*execution.AwaitInstance {
	t.Helper()
	out, err := h.Awaits.ListAwaitsByPattern(ctx, h.Workspace, pattern)
	if err != nil {
		t.Fatalf("ListAwaitsByPattern(%s): %v", pattern, err)
	}
	return out
}

func assertNoSatisfiedRow(t *testing.T, ctx context.Context, h *AwaitHarness, instanceKey string) {
	t.Helper()
	if _, err := h.Awaits.GetSatisfiedAwait(ctx, h.Workspace, instanceKey); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("GetSatisfiedAwait(%s) on non-satisfied await: want ErrNotFound, got %v", instanceKey, err)
	}
}

func testRegisterLeavesPendingWithoutMatch(t *testing.T, h *AwaitHarness) {
	ctx := t.Context()
	res := mustRegister(t, ctx, h, awaitReg("run-a", 1, "pr.approved:repo-1"))
	if res.Satisfied {
		t.Fatalf("register with empty journal: Satisfied=true, want pending")
	}
	if res.Instance.Status != execution.AwaitPending {
		t.Fatalf("pending status = %q, want pending", res.Instance.Status)
	}
	pending := mustListPattern(t, ctx, h, "pr.approved:repo-1")
	if len(pending) != 1 || pending[0].InstanceKey != res.Instance.InstanceKey {
		t.Fatalf("pattern index after registration = %+v, want the pending instance", pending)
	}
	assertNoSatisfiedRow(t, ctx, h, res.Instance.InstanceKey)
}

// The lost-wakeup case (vet A2): the event landed before the await was
// registered; register-and-check must resolve immediately, never suspend forever.
func testEventBeforeRegistrationSatisfies(t *testing.T, h *AwaitHarness) {
	ctx := t.Context()
	eventID := h.AppendEvent(t, "pr.approved", "repo-1", "alice")
	res := mustRegister(t, ctx, h, awaitReg("run-a", 1, "pr.approved:repo-1"))
	if !res.Satisfied || res.Instance.Status != execution.AwaitSatisfied {
		t.Fatalf("register after matching event: satisfied=%v status=%q, want immediate satisfaction",
			res.Satisfied, res.Instance.Status)
	}
	if res.Instance.SatisfiedByEventID != eventID {
		t.Fatalf("SatisfiedByEventID = %q, want %q", res.Instance.SatisfiedByEventID, eventID)
	}
	if pending := mustListPattern(t, ctx, h, "pr.approved:repo-1"); len(pending) != 0 {
		t.Fatalf("satisfied await still in pattern index: %+v", pending)
	}
	row, err := h.Awaits.GetSatisfiedAwait(ctx, h.Workspace, res.Instance.InstanceKey)
	if err != nil {
		t.Fatalf("GetSatisfiedAwait: %v", err)
	}
	if row.SatisfiedByEventID != eventID {
		t.Fatalf("replay row event = %q, want %q", row.SatisfiedByEventID, eventID)
	}
}

func testEventAfterPendingRegistrationStaysPending(t *testing.T, h *AwaitHarness) {
	ctx := t.Context()
	res := mustRegister(t, ctx, h, awaitReg("run-a", 1, "pr.approved:repo-1"))
	h.AppendEvent(t, "pr.approved", "repo-1", "alice")
	// The store never auto-resolves on append — the dispatch matcher (AW7)
	// finds the pending instance via ListAwaitsByPattern and resolves it.
	assertNoSatisfiedRow(t, ctx, h, res.Instance.InstanceKey)
	pending := mustListPattern(t, ctx, h, "pr.approved:repo-1")
	if len(pending) != 1 {
		t.Fatalf("pending await missing from dispatch index after append: %+v", pending)
	}
}

// RULE 3: re-registering a satisfied instance key replays the recorded
// event deterministically — same event ID every time, no double-consume.
func testSatisfiedRegistrationReplays(t *testing.T, h *AwaitHarness) {
	ctx := t.Context()
	h.AppendEvent(t, "pr.approved", "repo-1", "alice")
	h.AppendEvent(t, "pr.approved", "repo-1", "bob")
	first := mustRegister(t, ctx, h, awaitReg("run-a", 1, "pr.approved:repo-1"))
	if !first.Satisfied || first.Instance.SatisfiedByEventID == "" {
		t.Fatalf("first register not satisfied by a journaled event: %+v", first)
	}
	replay := mustRegister(t, ctx, h, awaitReg("run-a", 1, "pr.approved:repo-1"))
	if !replay.Satisfied {
		t.Fatalf("re-registration of satisfied key pending instead of replaying")
	}
	if replay.Instance.SatisfiedByEventID != first.Instance.SatisfiedByEventID {
		t.Fatalf("replay event %q != first event %q",
			replay.Instance.SatisfiedByEventID, first.Instance.SatisfiedByEventID)
	}
	if pending := mustListPattern(t, ctx, h, "pr.approved:repo-1"); len(pending) != 0 {
		t.Fatalf("replayed registration pending a duplicate: %+v", pending)
	}
}

func testPendingRegistrationIdempotent(t *testing.T, h *AwaitHarness) {
	ctx := t.Context()
	mustRegister(t, ctx, h, awaitReg("run-a", 1, "pr.approved:repo-1"))
	again := mustRegister(t, ctx, h, awaitReg("run-a", 1, "pr.approved:repo-1"))
	if again.Satisfied || again.Instance.Status != execution.AwaitPending {
		t.Fatalf("pending re-registration = %+v, want pending row back", again)
	}
	if pending := mustListPattern(t, ctx, h, "pr.approved:repo-1"); len(pending) != 1 {
		t.Fatalf("re-registration duplicated the pending await: %+v", pending)
	}
}

// RULE 4: the eligible-actor predicate filters journaled events during the
// registration scan.
func testActorFilterAtScan(t *testing.T, h *AwaitHarness) {
	ctx := t.Context()
	cases := []struct {
		name          string
		allow         []string
		actor         string
		wantSatisfied bool
	}{
		{"empty allow admits any actor", nil, "mallory", true},
		{"allowed actor satisfies", []string{"alice", "bob"}, "alice", true},
		{"mismatched actor suspends", []string{"alice"}, "mallory", false},
	}
	subjects := []string{"repo-a", "repo-b", "repo-c"}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subject := subjects[i]
			pattern := execution.AwaitEventKey("approval.recorded", subject)
			h.AppendEvent(t, "approval.recorded", subject, tc.actor)
			in := awaitReg("run-a", i+1, pattern)
			in.ActorAllow = tc.allow
			res := mustRegister(t, ctx, h, in)
			if res.Satisfied != tc.wantSatisfied {
				t.Fatalf("Satisfied = %v, want %v", res.Satisfied, tc.wantSatisfied)
			}
		})
	}
}

// RULE 1 + locked decision: exact rendered subject-key equality only. '*' is
// a literal character, never a glob.
func testPatternExactEqualityNoGlob(t *testing.T, h *AwaitHarness) {
	ctx := t.Context()
	const pattern = "pr.approved:repo-*"
	h.AppendEvent(t, "pr.approved", "repo-123", "alice")
	pending := mustRegister(t, ctx, h, awaitReg("run-a", 1, pattern))
	if pending.Satisfied {
		t.Fatalf("pattern %q glob-matched subject repo-123; want literal comparison", pattern)
	}
	if pending := mustListPattern(t, ctx, h, pattern); len(pending) != 1 {
		t.Fatalf("literal pattern %q not indexed for dispatch: %+v", pattern, pending)
	}
	literalID := h.AppendEvent(t, "pr.approved", "repo-*", "alice")
	satisfied := mustRegister(t, ctx, h, awaitReg("run-a", 2, pattern))
	if !satisfied.Satisfied || satisfied.Instance.SatisfiedByEventID != literalID {
		t.Fatalf("literal subject %q did not satisfy pattern %q: %+v", "repo-*", pattern, satisfied)
	}
}

func testRegistrationValidation(t *testing.T, h *AwaitHarness) {
	ctx := t.Context()
	cases := []struct {
		name    string
		mutate  func(*execution.AwaitRegistration)
		wantErr error
	}{
		{"missing deadline", func(r *execution.AwaitRegistration) { r.Deadline = time.Time{} }, execution.ErrAwaitTimeoutRequired},
		{"past deadline", func(r *execution.AwaitRegistration) { r.Deadline = time.Now().Add(-time.Minute) }, execution.ErrAwaitTimeoutRequired},
		{"bare event type pattern", func(r *execution.AwaitRegistration) { r.Pattern = "pr.approved" }, execution.ErrAwaitPatternUnscoped},
		{"empty subject pattern", func(r *execution.AwaitRegistration) { r.Pattern = "pr.approved:" }, execution.ErrAwaitPatternUnscoped},
		{"malformed instance key", func(r *execution.AwaitRegistration) { r.InstanceKey = "run-a#await-0" }, execution.ErrAwaitInstanceKeyMalformed},
		{"key belongs to another run", func(r *execution.AwaitRegistration) { r.RunID = "run-b" }, execution.ErrAwaitInstanceKeyMalformed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := awaitReg("run-a", 1, "pr.approved:repo-1")
			tc.mutate(&in)
			_, err := h.Awaits.RegisterAwaitAndCheck(ctx, h.Workspace, in)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if !errors.Is(err, persistence.ErrInvalid) {
				t.Fatalf("err = %v, want it to wrap persistence.ErrInvalid", err)
			}
		})
	}
}

func testResolveFirstWinnerPersistsPayload(t *testing.T, h *AwaitHarness) {
	ctx := t.Context()
	res := mustRegister(t, ctx, h, awaitReg("run-a", 1, "pr.approved:repo-1"))
	key := res.Instance.InstanceKey
	winnerEvent := h.AppendEvent(t, "pr.approved", "repo-1", "alice")
	payload := json.RawMessage(`{"decision":"approved"}`)
	won, err := h.Awaits.ResolveAwait(ctx, h.Workspace, key, winnerEvent, payload, "alice")
	if err != nil || !won.Resume {
		t.Fatalf("first resolve: resume=%v err=%v, want winning resolve", won != nil && won.Resume, err)
	}
	if won.Instance.Status != execution.AwaitSatisfied || won.Instance.ResumedAt == nil {
		t.Fatalf("resolved row = %+v, want satisfied with ResumedAt", won.Instance)
	}
	lost, err := h.Awaits.ResolveAwait(ctx, h.Workspace, key, "event-late", nil, "bob")
	if err != nil || lost.Resume {
		t.Fatalf("second resolve: resume=%v err=%v, want idempotent no-resume", lost != nil && lost.Resume, err)
	}
	if lost.Instance.SatisfiedByEventID != winnerEvent {
		t.Fatalf("loser overwrote winner: event = %q, want %q", lost.Instance.SatisfiedByEventID, winnerEvent)
	}
	row, err := h.Awaits.GetSatisfiedAwait(ctx, h.Workspace, key)
	if err != nil {
		t.Fatalf("GetSatisfiedAwait: %v", err)
	}
	if !bytes.Equal(row.SatisfiedPayload, payload) {
		t.Fatalf("replayed payload = %s, want %s inline", row.SatisfiedPayload, payload)
	}
	if pending := mustListPattern(t, ctx, h, "pr.approved:repo-1"); len(pending) != 0 {
		t.Fatalf("resolved await still in dispatch index: %+v", pending)
	}
}

// Timeout decision: the deadline sweeper resolves through the same path with
// a synthetic timeout event; the row lands in timed_out and replays.
func testResolveTimeoutLandsTimedOut(t *testing.T, h *AwaitHarness) {
	ctx := t.Context()
	res := mustRegister(t, ctx, h, awaitReg("run-a", 1, "pr.approved:repo-1"))
	key := res.Instance.InstanceKey
	timeoutEvent := execution.AwaitTimeoutEventIDPrefix + "deadline-1"
	out, err := h.Awaits.ResolveAwait(ctx, h.Workspace, key, timeoutEvent, nil, "system")
	if err != nil || !out.Resume {
		t.Fatalf("timeout resolve: resume=%v err=%v, want winning resolve", out != nil && out.Resume, err)
	}
	if out.Instance.Status != execution.AwaitTimedOut {
		t.Fatalf("timeout resolution status = %q, want timed_out", out.Instance.Status)
	}
	row, err := h.Awaits.GetSatisfiedAwait(ctx, h.Workspace, key)
	if err != nil || row.Status != execution.AwaitTimedOut || row.SatisfiedByEventID != timeoutEvent {
		t.Fatalf("timed_out replay row = %+v err=%v, want timeout event recorded", row, err)
	}
}

func testResolveMissingNotFound(t *testing.T, h *AwaitHarness) {
	ctx := t.Context()
	if _, err := h.Awaits.ResolveAwait(ctx, h.Workspace, "run-x#await-1", "event-1", nil, "alice"); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("resolve of unknown await: err = %v, want ErrNotFound", err)
	}
	assertNoSatisfiedRow(t, ctx, h, "run-x#await-1")
}

func testResolvePayloadCapRejected(t *testing.T, h *AwaitHarness) {
	ctx := t.Context()
	res := mustRegister(t, ctx, h, awaitReg("run-a", 1, "pr.approved:repo-1"))
	key := res.Instance.InstanceKey
	oversize := json.RawMessage(`"` + string(bytes.Repeat([]byte("x"), execution.DefaultAwaitResumePayloadCap)) + `"`)
	_, err := h.Awaits.ResolveAwait(ctx, h.Workspace, key, "event-1", oversize, "alice")
	if !errors.Is(err, persistence.ErrInvalid) {
		t.Fatalf("oversize payload resolve: err = %v, want ErrInvalid", err)
	}
	// The rejected resolve must leave the await pending and resolvable.
	assertNoSatisfiedRow(t, ctx, h, key)
	if pending := mustListPattern(t, ctx, h, "pr.approved:repo-1"); len(pending) != 1 {
		t.Fatalf("await dropped from index by rejected resolve: %+v", pending)
	}
}

// Multi-waiter decision: one pattern, ALL pending instances listed for
// dispatch, ordered by registration time.
func testMultiWaiterListByPattern(t *testing.T, h *AwaitHarness) {
	ctx := t.Context()
	base := time.Now().UTC()
	first := awaitReg("run-a", 1, "pr.approved:repo-1")
	first.RegisteredAt = base
	second := awaitReg("run-b", 1, "pr.approved:repo-1")
	second.RegisteredAt = base.Add(time.Second)
	other := awaitReg("run-c", 1, "pr.approved:repo-2")
	mustRegister(t, ctx, h, second)
	mustRegister(t, ctx, h, first)
	mustRegister(t, ctx, h, other)
	pending := mustListPattern(t, ctx, h, "pr.approved:repo-1")
	if len(pending) != 2 {
		t.Fatalf("multi-waiter list returned %d instances, want both waiters", len(pending))
	}
	if pending[0].InstanceKey != first.InstanceKey || pending[1].InstanceKey != second.InstanceKey {
		t.Fatalf("list order = [%s %s], want RegisteredAt ascending [%s %s]",
			pending[0].InstanceKey, pending[1].InstanceKey, first.InstanceKey, second.InstanceKey)
	}
}

func testDueDeadlinesOrderedAndCapped(t *testing.T, h *AwaitHarness) {
	ctx := t.Context()
	now := time.Now().UTC()
	for i, offset := range []time.Duration{30 * time.Minute, 10 * time.Minute, 20 * time.Minute} {
		in := awaitReg("run-a", i+1, "pr.approved:repo-1")
		in.Deadline = now.Add(offset)
		mustRegister(t, ctx, h, in)
	}
	due, err := h.Awaits.ListDueAwaitDeadlines(ctx, h.Workspace, now.Add(25*time.Minute), 0)
	if err != nil {
		t.Fatalf("ListDueAwaitDeadlines: %v", err)
	}
	wantOrder := []string{execution.AwaitInstanceKey("run-a", 2), execution.AwaitInstanceKey("run-a", 3)}
	if len(due) != 2 {
		t.Fatalf("due awaits = %+v, want the 2 due instances %v", due, wantOrder)
	}
	for i, want := range wantOrder {
		if due[i].InstanceKey != want {
			t.Fatalf("due[%d] = %s, want %s deadline-ascending", i, due[i].InstanceKey, want)
		}
	}
	capped, err := h.Awaits.ListDueAwaitDeadlines(ctx, h.Workspace, now.Add(25*time.Minute), 1)
	if err != nil || len(capped) != 1 || capped[0].InstanceKey != wantOrder[0] {
		t.Fatalf("limit=1 due awaits = %+v err=%v, want only %s", capped, err, wantOrder[0])
	}
	if _, err := h.Awaits.ResolveAwait(ctx, h.Workspace, wantOrder[0], "event-1", nil, "alice"); err != nil {
		t.Fatalf("resolve due await: %v", err)
	}
	after, err := h.Awaits.ListDueAwaitDeadlines(ctx, h.Workspace, now.Add(25*time.Minute), 0)
	if err != nil || len(after) != 1 || after[0].InstanceKey != wantOrder[1] {
		t.Fatalf("due awaits after resolve = %+v err=%v, want resolved one gone", after, err)
	}
}

// runHarness skips the subtest for await-index-only backends.
func runHarness(t *testing.T, h *AwaitHarness) *AwaitRunHarness {
	t.Helper()
	if h.Runs == nil {
		t.Skip("harness has no run lifecycle wiring")
	}
	return h.Runs
}

// claimRun claims a queued run and returns it with its owner triple set.
func claimRun(t *testing.T, ctx context.Context, h *AwaitHarness, runID string) *execution.DriverRunRecord {
	t.Helper()
	run, err := h.Runs.Store.Claim(ctx, h.Workspace, runID, "node-1", "lease-1")
	if err != nil {
		t.Fatalf("Claim(%s): %v", runID, err)
	}
	return run
}

func testSuspendRequiresRunningOwner(t *testing.T, h *AwaitHarness) {
	runs := runHarness(t, h)
	ctx := t.Context()
	runs.NewRun(t, "run-a", "")
	// Queued run, no owner: the running-status requirement rejects it.
	if _, err := runs.Suspend(ctx, "run-a", "", "", 0); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("suspend of queued run: err = %v, want ErrInvalidTransition", err)
	}
	claimed := claimRun(t, ctx, h, "run-a")
	if _, err := runs.Suspend(ctx, "run-a", claimed.NodeID, "lease-stale", claimed.FencingToken); !errors.Is(err, persistence.ErrNotOwner) {
		t.Fatalf("suspend with wrong lease: err = %v, want ErrNotOwner", err)
	}
	if _, err := runs.Suspend(ctx, "run-a", claimed.NodeID, claimed.LeaseID, claimed.FencingToken+1); !errors.Is(err, persistence.ErrNotOwner) {
		t.Fatalf("suspend with wrong fencing token: err = %v, want ErrNotOwner", err)
	}
	if _, err := runs.Suspend(ctx, "run-missing", "node-1", "lease-1", 1); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("suspend of missing run: err = %v, want ErrNotFound", err)
	}
}

func testSuspendReleasesSlotIdempotently(t *testing.T, h *AwaitHarness) {
	runs := runHarness(t, h)
	ctx := t.Context()
	runs.NewRun(t, "run-a", "")
	claimed := claimRun(t, ctx, h, "run-a")
	suspended, err := runs.Suspend(ctx, "run-a", claimed.NodeID, claimed.LeaseID, claimed.FencingToken)
	if err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	if suspended.Status != execution.DriverRunSuspendedAwait || suspended.SuspendedAt == nil {
		t.Fatalf("suspended run = %+v, want suspended_awaiting_event with SuspendedAt", suspended)
	}
	if suspended.NodeID != "" || suspended.LeaseID != "" {
		t.Fatalf("suspend kept the slot: node=%q lease=%q, want both cleared", suspended.NodeID, suspended.LeaseID)
	}
	// The pending->suspend leg may be retried by the driver-op layer after the
	// owner fields were already cleared: idempotent no-op.
	again, err := runs.Suspend(ctx, "run-a", claimed.NodeID, claimed.LeaseID, claimed.FencingToken)
	if err != nil || again.Status != execution.DriverRunSuspendedAwait {
		t.Fatalf("idempotent re-suspend = %+v err=%v, want suspended row back", again, err)
	}
}

func testHeartbeatRejectsSuspended(t *testing.T, h *AwaitHarness) {
	runs := runHarness(t, h)
	ctx := t.Context()
	runs.NewRun(t, "run-a", "")
	claimed := claimRun(t, ctx, h, "run-a")
	if _, err := runs.Suspend(ctx, "run-a", claimed.NodeID, claimed.LeaseID, claimed.FencingToken); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	// The formerly-owning executor must not renew a suspended run: the
	// status guard fires before owner validation.
	_, err := runs.Store.Heartbeat(ctx, h.Workspace, "run-a", claimed.NodeID, claimed.LeaseID, claimed.FencingToken)
	if !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("heartbeat on suspended run: err = %v, want ErrInvalidTransition", err)
	}
}

func testClaimRejectsSuspended(t *testing.T, h *AwaitHarness) {
	runs := runHarness(t, h)
	ctx := t.Context()
	runs.NewRun(t, "run-b", "")
	claimed := claimRun(t, ctx, h, "run-b")
	if _, err := runs.Suspend(ctx, "run-b", claimed.NodeID, claimed.LeaseID, claimed.FencingToken); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	if _, err := runs.Store.Claim(ctx, h.Workspace, "run-b", "node-2", "lease-2"); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("claim of suspended run: err = %v, want ErrInvalidTransition", err)
	}
}

// RULE 5's corollary: only the await timeout sweeper terminates a suspended
// run — the stale-heartbeat sweeper must skip it no matter how old it is.
func testStaleSweepSkipsSuspended(t *testing.T, h *AwaitHarness) {
	runs := runHarness(t, h)
	ctx := t.Context()
	runs.NewRun(t, "run-a", "")
	claimed := claimRun(t, ctx, h, "run-a")
	if _, err := runs.Suspend(ctx, "run-a", claimed.NodeID, claimed.LeaseID, claimed.FencingToken); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	// StaleBefore far in the future makes every heartbeat "ancient".
	result, err := runs.Store.RecoverStale(ctx, h.Workspace, execution.StaleDriverRunRecovery{
		StaleBefore: time.Now().Add(24 * time.Hour).UTC(),
	})
	if err != nil {
		t.Fatalf("RecoverStale: %v", err)
	}
	if result.Recovered != 0 {
		t.Fatalf("stale sweep recovered %d runs, want suspended run skipped", result.Recovered)
	}
	run, err := runs.Store.Get(ctx, h.Workspace, "run-a")
	if err != nil || run.Status != execution.DriverRunSuspendedAwait {
		t.Fatalf("run after stale sweep = %+v err=%v, want still suspended", run, err)
	}
}

func testResumeOnlyFromSuspendedFirstWinner(t *testing.T, h *AwaitHarness) {
	runs := runHarness(t, h)
	ctx := t.Context()
	runs.NewRun(t, "run-a", "")
	if _, err := runs.Resume(ctx, "run-a", "event-1"); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("resume of queued run: err = %v, want ErrInvalidTransition", err)
	}
	claimed := claimRun(t, ctx, h, "run-a")
	// Pending->suspend window: a resolve racing the suspend sees the run still
	// running; the store stays strict and the resume path retries (AW7).
	if _, err := runs.Resume(ctx, "run-a", "event-1"); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("resume of running run: err = %v, want ErrInvalidTransition", err)
	}
	if _, err := runs.Suspend(ctx, "run-a", claimed.NodeID, claimed.LeaseID, claimed.FencingToken); err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	if _, err := runs.Resume(ctx, "run-a", ""); !errors.Is(err, persistence.ErrInvalid) {
		t.Fatalf("resume without source event: err = %v, want ErrInvalid", err)
	}
	// Security gate: resume is granted by resolution, never by knowing the
	// run id + await key. An unregistered then a pending await both deny.
	if _, err := runs.Resume(ctx, "run-a", "event-9"); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("resume with no await registered: err = %v, want ErrInvalidTransition", err)
	}
	reg := mustRegister(t, ctx, h, awaitReg("run-a", 1, "pr.approved:repo-resume"))
	if reg.Satisfied {
		t.Fatalf("registration unexpectedly satisfied; test wants a pending await")
	}
	if _, err := runs.Resume(ctx, "run-a", "event-9"); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("resume with pending await: err = %v, want ErrInvalidTransition", err)
	}
	if _, err := h.Awaits.ResolveAwait(ctx, h.Workspace, reg.Instance.InstanceKey, "event-9", nil, "alice"); err != nil {
		t.Fatalf("ResolveAwait: %v", err)
	}
	resumed, err := runs.Resume(ctx, "run-a", "event-9")
	if err != nil || resumed.Status != execution.DriverRunQueued || resumed.ResumeSourceEventID != "event-9" {
		t.Fatalf("resume = %+v err=%v, want queued with resume source event-9", resumed, err)
	}
	if _, err := runs.Resume(ctx, "run-a", "event-10"); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("double resume: err = %v, want single winner", err)
	}
	reclaimed, err := runs.Store.Claim(ctx, h.Workspace, "run-a", "node-2", "lease-2")
	if err != nil || reclaimed.FencingToken == claimed.FencingToken {
		t.Fatalf("reclaim after resume = %+v err=%v, want fresh fenced claim", reclaimed, err)
	}
}

func testParentRunIDRoundTrip(t *testing.T, h *AwaitHarness) {
	runs := runHarness(t, h)
	ctx := t.Context()
	child := runs.NewRun(t, "run-child", "run-parent")
	if child.ParentRunID != "run-parent" {
		t.Fatalf("created child ParentRunID = %q, want run-parent", child.ParentRunID)
	}
	got, err := runs.Store.Get(ctx, h.Workspace, "run-child")
	if err != nil || got.ParentRunID != "run-parent" {
		t.Fatalf("ParentRunID round-trip = %+v err=%v, want run-parent persisted", got, err)
	}
	detached := runs.NewRun(t, "run-detached", "")
	if detached.ParentRunID != "" {
		t.Fatalf("detached run ParentRunID = %q, want empty (no cascade)", detached.ParentRunID)
	}
}
