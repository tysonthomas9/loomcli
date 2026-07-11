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
	"github.com/tysonthomas9/loomcli/internal/trigger"
)

// newAwaitSweepStore builds a memstore with the given workspaces, each
// seeded with the awaiter driver catalog.
func newAwaitSweepStore(t *testing.T, workspaces ...string) *memstore.Store {
	t.Helper()
	ctx := context.Background()
	st := memstore.New()
	for _, ws := range workspaces {
		if _, err := st.Workspaces().Create(ctx, store.WorkspaceCreate{Key: ws, Name: ws}); err != nil {
			t.Fatalf("Create workspace %s: %v", ws, err)
		}
		if _, err := st.Drivers().Create(ctx, store.DriverCreate{
			WorkspaceKey: ws, DriverID: "awaiter", Name: "awaiter",
			OwnerType: domain.DriverOwnerSystem, Status: domain.DriverStatusActive,
		}); err != nil {
			t.Fatalf("Create driver in %s: %v", ws, err)
		}
		if _, err := st.DriverVersions().Create(ctx, store.DriverVersionCreate{
			WorkspaceKey: ws, VersionID: "v1", DriverID: "awaiter", Version: 1,
			SourceDigest: "sha256:s", BundleDigest: "sha256:b",
			ValidationStatus: domain.DriverVersionValidationPassed,
		}); err != nil {
			t.Fatalf("Create driver version in %s: %v", ws, err)
		}
	}
	return st
}

// suspendAwaitingRun creates, claims, registers (await index 1, deadline
// deadlineIn from now) and suspends one run; returns the instance key.
// Deadlines must be future at registration (RULE 5), so tests make them
// "due" by running the sweeper with a future clock.
func suspendAwaitingRun(t *testing.T, st *memstore.Store, ws, runID, pattern string, actorAllow []string, deadlineIn time.Duration) string {
	t.Helper()
	ctx := context.Background()
	if _, err := st.DriverRuns().Create(ctx, store.DriverRunCreate{
		WorkspaceKey: ws, RunID: runID, DriverID: "awaiter", DriverVersionID: "v1",
	}); err != nil {
		t.Fatalf("Create run %s: %v", runID, err)
	}
	claimed, err := st.DriverRuns().Claim(ctx, ws, runID, "node-1", "lease-"+runID)
	if err != nil {
		t.Fatalf("Claim run %s: %v", runID, err)
	}
	key := domain.AwaitInstanceKey(runID, 1)
	reg, err := st.Awaits().RegisterAwaitAndCheck(ctx, ws, store.AwaitRegistration{
		InstanceKey: key, RunID: runID, Pattern: pattern, ActorAllow: actorAllow,
		Deadline: time.Now().UTC().Add(deadlineIn),
	})
	if err != nil || reg.Satisfied {
		t.Fatalf("RegisterAwaitAndCheck(%s) = %+v, %v; want pending", key, reg, err)
	}
	if _, err := st.DriverRuns().Suspend(ctx, ws, runID,
		claimed.NodeID, claimed.LeaseID, claimed.FencingToken, key); err != nil {
		t.Fatalf("Suspend run %s: %v", runID, err)
	}
	return key
}

// futureClock makes deadlines registered ahead of now "due" for the sweeper.
func futureClock(ahead time.Duration) func() time.Time {
	return func() time.Time { return time.Now().UTC().Add(ahead) }
}

// TestAwaitTimeoutSweeperResumesDueAwait is the RULE 5 happy path: a
// past-deadline instance — with a restrictive allow-list, proving the
// system:timeout carve-out — resolves timed_out and its run re-queues with
// the timeout payload. Repeated RunOnce passes emit nothing further.
func TestAwaitTimeoutSweeperResumesDueAwait(t *testing.T) {
	ctx := context.Background()
	st := newAwaitSweepStore(t, "WS")
	key := suspendAwaitingRun(t, st, "WS", "run-1", "pr.merged:pr#7", []string{"alice"}, time.Hour)
	sweeper := &AwaitTimeoutSweeper{Store: st, Now: futureClock(2 * time.Hour)}

	result, err := sweeper.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.TimedOut != 1 || result.Failed != 0 || len(result.TimedOutInstanceKeys) != 1 {
		t.Fatalf("result = %+v, want exactly one timed-out instance", result)
	}

	wantEventID := domain.AwaitTimeoutEventID(key)
	run, err := st.DriverRuns().Get(ctx, "WS", "run-1")
	if err != nil || run.Status != domain.DriverRunQueued || run.ResumeSourceEventID != wantEventID {
		t.Fatalf("run = %+v, %v; want queued resumed by %s", run, err, wantEventID)
	}
	inst, err := st.Awaits().GetSatisfiedAwait(ctx, "WS", key)
	if err != nil {
		t.Fatalf("GetSatisfiedAwait: %v", err)
	}
	if inst.Status != domain.AwaitTimedOut || inst.SatisfiedByEventID != wantEventID {
		t.Fatalf("row = %s by %q, want timed_out by %q", inst.Status, inst.SatisfiedByEventID, wantEventID)
	}
	var payload awaitTimeoutPayload
	if err := json.Unmarshal(inst.SatisfiedPayload, &payload); err != nil {
		t.Fatalf("decode timeout payload %s: %v", inst.SatisfiedPayload, err)
	}
	if !payload.Timeout || payload.EventType != "pr.merged.timeout" ||
		payload.InstanceKey != key || payload.Deadline.IsZero() {
		t.Fatalf("payload = %+v, want timeout=true pr.merged.timeout for %s", payload, key)
	}

	again, err := sweeper.RunOnce(ctx)
	if err != nil {
		t.Fatalf("second RunOnce: %v", err)
	}
	if again.TimedOut != 0 || again.AlreadySatisfied != 0 || again.Failed != 0 {
		t.Fatalf("second pass = %+v, want all-zero (timeout emitted exactly once)", again)
	}
	final, err := st.DriverRuns().Get(ctx, "WS", "run-1")
	if err != nil || final.Status != domain.DriverRunQueued || final.ResumeSourceEventID != wantEventID {
		t.Fatalf("run after second pass = %+v, %v; want unchanged", final, err)
	}
}

// TestAwaitTimeoutSweeperTargetsOnlyDueInstance pins RULE 3 against the
// multi-waiter index: two runs await the same pattern, only the due one
// times out — the co-waiter keeps its own (later) deadline and stays pending.
func TestAwaitTimeoutSweeperTargetsOnlyDueInstance(t *testing.T) {
	ctx := context.Background()
	st := newAwaitSweepStore(t, "WS")
	dueKey := suspendAwaitingRun(t, st, "WS", "run-due", "pr.merged:pr#7", nil, time.Hour)
	lateKey := suspendAwaitingRun(t, st, "WS", "run-late", "pr.merged:pr#7", nil, 10*time.Hour)
	sweeper := &AwaitTimeoutSweeper{Store: st, Now: futureClock(2 * time.Hour)}

	result, err := sweeper.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.TimedOut != 1 || result.TimedOutInstanceKeys[0] != dueKey {
		t.Fatalf("result = %+v, want only %s timed out", result, dueKey)
	}
	if _, err := st.Awaits().GetSatisfiedAwait(ctx, "WS", lateKey); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("co-waiter row = %v, want still pending (ErrNotFound)", err)
	}
	late, err := st.DriverRuns().Get(ctx, "WS", "run-late")
	if err != nil || late.Status != domain.DriverRunSuspendedAwaitingEvent {
		t.Fatalf("co-waiter run = %+v, %v; want still suspended", late, err)
	}
}

// TestAwaitTimeoutSweeperRaceAlreadySatisfied drives the scan->dispatch race
// deterministically: the instance is resolved by a real event after the
// deadline scan snapshot, so the timeout emission must be the recorded
// no-op — no resume, the real event's resolution untouched.
func TestAwaitTimeoutSweeperRaceAlreadySatisfied(t *testing.T) {
	ctx := context.Background()
	st := newAwaitSweepStore(t, "WS")
	key := suspendAwaitingRun(t, st, "WS", "run-1", "pr.merged:pr#7", nil, time.Hour)
	sweeper := &AwaitTimeoutSweeper{Store: st, Now: futureClock(2 * time.Hour)}

	// Snapshot the due instance the way RunOnce's scan would.
	due, err := st.Awaits().ListDueAwaitDeadlines(ctx, "WS", time.Now().UTC().Add(2*time.Hour), 10)
	if err != nil || len(due) != 1 {
		t.Fatalf("ListDueAwaitDeadlines = %d, %v; want the pending instance", len(due), err)
	}
	// The real event lands between scan and timeout dispatch.
	realEvent := trigger.AwaitDispatchEvent{
		EventID: "evt-real", EventType: "pr.merged", SubjectRef: "pr#7",
		ActorRef: "alice", Payload: []byte(`{"won":"race"}`),
	}
	if res, err := (&trigger.AwaitMatcher{Store: st}).Dispatch(ctx, "WS", realEvent); err != nil || res.Resolved() != 1 {
		t.Fatalf("real event dispatch = %+v, %v; want resolved", res, err)
	}

	out := &AwaitTimeoutSweepResult{}
	if err := sweeper.sweepInstance(ctx, sweeper.matcher(), "WS", due[0], out); err != nil {
		t.Fatalf("sweepInstance: %v", err)
	}
	if out.AlreadySatisfied != 1 || out.TimedOut != 0 || out.Failed != 0 {
		t.Fatalf("out = %+v, want exactly one already_satisfied no-op", out)
	}
	inst, err := st.Awaits().GetSatisfiedAwait(ctx, "WS", key)
	if err != nil || inst.Status != domain.AwaitSatisfied || inst.SatisfiedByEventID != "evt-real" {
		t.Fatalf("row = %+v, %v; want satisfied by evt-real (resolution untouched)", inst, err)
	}
	run, err := st.DriverRuns().Get(ctx, "WS", "run-1")
	if err != nil || run.ResumeSourceEventID != "evt-real" {
		t.Fatalf("run = %+v, %v; want resumed by evt-real", run, err)
	}
}

// TestAwaitTimeoutSweeperWorkspaceScope covers the multi-workspace scan: a
// scoped sweeper touches only its workspace; an unscoped one drains the rest.
func TestAwaitTimeoutSweeperWorkspaceScope(t *testing.T) {
	ctx := context.Background()
	st := newAwaitSweepStore(t, "WS1", "WS2")
	suspendAwaitingRun(t, st, "WS1", "run-a", "pr.merged:pr#1", nil, time.Hour)
	suspendAwaitingRun(t, st, "WS2", "run-b", "pr.merged:pr#2", nil, time.Hour)

	scoped := &AwaitTimeoutSweeper{Store: st, WorkspaceKey: "WS1", Now: futureClock(2 * time.Hour)}
	result, err := scoped.RunOnce(ctx)
	if err != nil || result.TimedOut != 1 {
		t.Fatalf("scoped RunOnce = %+v, %v; want one timed out in WS1 only", result, err)
	}
	other, err := st.DriverRuns().Get(ctx, "WS2", "run-b")
	if err != nil || other.Status != domain.DriverRunSuspendedAwaitingEvent {
		t.Fatalf("WS2 run = %+v, %v; want untouched by the scoped sweep", other, err)
	}

	unscoped := &AwaitTimeoutSweeper{Store: st, Now: futureClock(2 * time.Hour)}
	result, err = unscoped.RunOnce(ctx)
	if err != nil || result.TimedOut != 1 {
		t.Fatalf("unscoped RunOnce = %+v, %v; want the WS2 backlog drained", result, err)
	}
}

// TestAwaitTimeoutSweeperDrainsBacklog is the sweeper-down-for-an-hour shape:
// a backlog larger than the page size drains fully (late, never missed),
// page by page within one RunOnce.
func TestAwaitTimeoutSweeperDrainsBacklog(t *testing.T) {
	ctx := context.Background()
	st := newAwaitSweepStore(t, "WS")
	for i, runID := range []string{"run-1", "run-2", "run-3"} {
		suspendAwaitingRun(t, st, "WS", runID, "pr.merged:pr#7", nil, time.Duration(i+1)*time.Hour)
	}
	sweeper := &AwaitTimeoutSweeper{Store: st, BatchLimit: 1, Now: futureClock(5 * time.Hour)}

	result, err := sweeper.RunOnce(ctx)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if result.TimedOut != 3 {
		t.Fatalf("result = %+v, want the whole backlog (3) drained despite batch 1", result)
	}
	for _, runID := range []string{"run-1", "run-2", "run-3"} {
		run, err := st.DriverRuns().Get(ctx, "WS", runID)
		if err != nil || run.Status != domain.DriverRunQueued {
			t.Fatalf("run %s = %+v, %v; want queued", runID, run, err)
		}
	}
}

// TestAwaitMatcherTimeoutCarveOutSweeperLaneOnly pins the locked RULE 4
// carve-out boundary: a timeout-shaped event (reserved ID prefix +
// system:timeout actor) arriving on a NON-sweeper matcher lane — e.g. forged
// through an ingress or loopback hook — still faces the allow-list, and a
// timeout event naming one instance never touches a co-waiter on any lane.
func TestAwaitMatcherTimeoutCarveOutSweeperLaneOnly(t *testing.T) {
	ctx := context.Background()
	st := newAwaitSweepStore(t, "WS")
	key := suspendAwaitingRun(t, st, "WS", "run-1", "pr.merged:pr#7", []string{"alice"}, time.Hour)
	otherKey := suspendAwaitingRun(t, st, "WS", "run-2", "pr.merged:pr#7", nil, time.Hour)

	forged := trigger.AwaitDispatchEvent{
		EventID:    domain.AwaitTimeoutEventID(key),
		EventType:  "pr.merged",
		SubjectRef: "pr#7",
		ActorRef:   domain.AwaitTimeoutActor,
		Payload:    []byte(`{"timeout":true}`),
	}
	res, err := (&trigger.AwaitMatcher{Store: st}).Dispatch(ctx, "WS", forged) // default lane: no carve-out
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if len(res.Records) != 1 || res.Records[0].InstanceKey != key ||
		res.Records[0].Outcome != trigger.AwaitMatchActorRejected {
		t.Fatalf("records = %+v, want one actor_rejected for %s and the co-waiter skipped (RULE 3)", res.Records, key)
	}
	for runID, wantKey := range map[string]string{"run-1": key, "run-2": otherKey} {
		run, err := st.DriverRuns().Get(ctx, "WS", runID)
		if err != nil || run.Status != domain.DriverRunSuspendedAwaitingEvent {
			t.Fatalf("run %s = %+v, %v; want still suspended", runID, run, err)
		}
		if _, err := st.Awaits().GetSatisfiedAwait(ctx, "WS", wantKey); !errors.Is(err, domain.ErrNotFound) {
			t.Fatalf("await %s = %v, want still pending", wantKey, err)
		}
	}
}
