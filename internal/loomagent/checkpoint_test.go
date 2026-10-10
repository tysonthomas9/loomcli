package loomagent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// sessionOf is a's recorded session.
func sessionOf(a loomstore.Agent) loomharness.NativeRef {
	return loomharness.NativeRef{Root: deref(a.HarnessSessionRoot), NativeID: deref(a.HarnessSessionID)}
}

// turnsStarted is how many turns the fake started on ref.
func turnsStarted(e *createEnv, ref loomharness.NativeRef) int {
	return len(e.h.Harness.(*fake.Harness).Turns(ref))
}

// wantCheckpoints requires id's checkpoint refs turn/0.. to hold trees, and no more.
func wantCheckpoints(t *testing.T, ws *fakeWorkspace, id string, trees ...string) {
	t.Helper()
	for n, want := range append(trees, "") {
		got, ok := ws.checkpoint(checkpointRef(id, n))
		if n == len(trees) {
			if ok {
				t.Fatalf("turn/%d exists (%q); want only %d refs", n, got, len(trees))
			}
			break
		}
		if !ok || got != want {
			t.Fatalf("turn/%d = %q (exists %v), want %q", n, got, ok, want)
		}
	}
}

// TestCheckpointBaselineBeforeFirstHandOver: Create with a first message
// captures turn/0, as the working copy is at Create, before the harness is
// prompted; the first message is then handed over once.
func TestCheckpointBaselineBeforeFirstHandOver(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	e.ws.setTree("at create")
	prompted := -1 // turns the agent's session ran when turn/0 was captured
	e.ws.capture = func(ref string) error {
		if id, ok := strings.CutSuffix(strings.TrimPrefix(ref, "refs/loom/checkpoints/"), "/turn/0"); ok {
			if a, err := e.st.GetAgent(ctx, id); err == nil && a.HarnessSessionID != nil {
				prompted = turnsStarted(e, sessionOf(a))
			} else {
				prompted = 0
			}
		}
		return nil
	}
	s := e.service(ServiceConfig{})
	req := leadReq("r1")
	req.FirstMessage = "hello"
	info, err := s.Create(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	e.ws.setTree("after the first turn started")
	a := s.get(t, info.AgentID)
	if prompted != 0 || turnsStarted(e, sessionOf(a)) != 1 {
		t.Fatalf("turns at the turn/0 capture = %d, turns now %d; want 0 then 1", prompted, turnsStarted(e, sessionOf(a)))
	}
	wantCheckpoints(t, e.ws, a.AgentID, "at create")
}

// TestCheckpointBaselineCrashBeforeRef: a Create that crashed after its
// worktree step, before turn/0, has no ref and nothing handed; after the
// restart the baseline is captured once, then turn 1 is handed over.
func TestCheckpointBaselineCrashBeforeRef(t *testing.T) {
	for _, point := range []string{"session", "done"} { // before and after create_step done
		t.Run(point, func(t *testing.T) {
			ctx := context.Background()
			e := newCreateEnv(t)
			e.ws.setTree("at create")
			var mu sync.Mutex
			captures := 0
			e.ws.capture = func(string) error { mu.Lock(); defer mu.Unlock(); captures++; return nil }
			req := leadReq("r1")
			req.FirstMessage = "hello"
			if !crashAt(t, point)(func() { _, _ = e.service(ServiceConfig{}).Create(ctx, req) }) {
				t.Fatal("Create did not crash before turn/0")
			}
			all, _, err := e.st.ListAgents(ctx, loomstore.AgentFilter{WorkspaceID: "ws", IncludeArchived: true})
			if err != nil || len(all) != 1 {
				t.Fatalf("agents = %v, %v", all, err)
			}
			id := all[0].AgentID
			if _, ok := e.ws.checkpoint(checkpointRef(id, 0)); ok || slotState(t, e.service(ServiceConfig{}), id, "create:r1") != loomstore.SlotWaiting {
				t.Fatal("turn/0 exists or the first message left its slot after the crash")
			}
			s := e.service(ServiceConfig{}) // the restart
			runDispatcher(t, s)
			drained(t, s, "turn 1 handed", func() bool { return len(handedReqs(t, s, id, "create:r1")) == 1 })
			mu.Lock()
			defer mu.Unlock()
			if a := s.get(t, id); captures != 1 || turnsStarted(e, sessionOf(a)) != 1 {
				t.Fatalf("captures = %d, turns = %d; want 1 each", captures, turnsStarted(e, sessionOf(a)))
			}
			wantCheckpoints(t, e.ws, id, "at create")
		})
	}
}

// TestCheckpointCaptureEachTurn: turns 1 and 2 end with refs turn/1 and
// turn/2 holding the working copy as each ended, the same on every harness.
func TestCheckpointCaptureEachTurn(t *testing.T) {
	for _, name := range Harnesses {
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			e := newCreateEnv(t)
			e.name = name
			fh := e.h.Harness.(*fake.Harness)
			s := e.service(ServiceConfig{})
			runFeed(t, s, name)
			e.ws.setTree("at create")
			req := leadReq("r1")
			req.Overrides.Harness = name
			info, err := s.Create(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			id, ref := info.AgentID, sessionOf(s.get(t, info.AgentID))
			fh.Script(id, fake.Turn{Steps: []fake.Step{{Ask: "t1"}}}, fake.Turn{Steps: []fake.Step{{Ask: "t2"}}})
			for i, turn := range []string{"t1", "t2"} {
				mustSendMsg(t, s, sendReq(id, "u"+turn, "go", user))
				drained(t, s, turn+" asked", func() bool { return len(s.openAsks(id)) == 1 })
				e.ws.setTree("end of " + turn)
				if err := fh.Session(ref).Reply(ctx, turn, loomharness.Reply{Allow: true}); err != nil {
					t.Fatal(err)
				}
				drained(t, s, turn+" captured", func() bool { _, ok := e.ws.checkpoint(checkpointRef(id, i+1)); return ok })
			}
			wantCheckpoints(t, e.ws, id, "at create", "end of t1", "end of t2")
		})
	}
}

// TestNextHandOverWaitsForRef: a message waiting as turn 1 ends is not
// handed over while turn/1 is being captured; it is once the ref exists.
func TestNextHandOverWaitsForRef(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s := e.service(ServiceConfig{})
	runFeed(t, s, "opencode")
	a, ref := newLead(t, e, s, "alpha")
	fh.Script(a.AgentID, fake.Turn{Steps: []fake.Step{{Ask: "t1"}}}, fake.Turn{Steps: []fake.Step{{Ask: "t2"}}})
	entered, release := make(chan struct{}), make(chan struct{})
	free := sync.OnceFunc(func() { close(release) })
	defer free() // a failure must not leave the feed blocked
	e.ws.capture = func(r string) error {
		if r == checkpointRef(a.AgentID, 1) {
			close(entered)
			<-release
		}
		return nil
	}
	mustSendMsg(t, s, sendReq(a.AgentID, "u1", "first", user))
	drained(t, s, "t1 asked", func() bool { return len(s.openAsks(a.AgentID)) == 1 })
	mustSendMsg(t, s, sendReq(a.AgentID, "c1", "waits for turn 2", child))
	if err := fh.Session(ref).Reply(ctx, "t1", loomharness.Reply{Allow: true}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(drainGuard):
		t.Fatal("turn/1 was never captured")
	}
	if slotState(t, s, a.AgentID, "c1") != loomstore.SlotWaiting || turnsStarted(e, ref) != 1 {
		t.Fatalf("while turn/1 is captured: c1 %s, turns %d; want waiting, 1", slotState(t, s, a.AgentID, "c1"), turnsStarted(e, ref))
	}
	free()
	drained(t, s, "c1 handed after turn/1", func() bool { return len(handedReqs(t, s, a.AgentID, "c1")) == 1 })
	if _, ok := e.ws.checkpoint(checkpointRef(a.AgentID, 1)); !ok || turnsStarted(e, ref) != 2 {
		t.Fatalf("turn/1 exists %v, turns %d; want true, 2", ok, turnsStarted(e, ref))
	}
}

// TestCheckpointFailureRetried: a failed capture keeps a message sent to
// the idle agent waiting, the Send accepted, with no Attention; the reconcile queue retries it after its
// backoff, and then the message is handed over.
func TestCheckpointFailureRetried(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s := e.service(ServiceConfig{})
	clock := useTestClock(s)
	runDispatcher(t, s)
	runFeed(t, s, "opencode")
	a, ref := newLead(t, e, s, "alpha")
	fh.Script(a.AgentID, fake.Turn{Steps: []fake.Step{{Ask: "t1"}}}, fake.Turn{Steps: []fake.Step{{Ask: "t2"}}})
	var mu sync.Mutex
	failing := true
	e.ws.capture = func(string) error {
		mu.Lock()
		defer mu.Unlock()
		if failing {
			return errors.New("disk full")
		}
		return nil
	}
	mustSendMsg(t, s, sendReq(a.AgentID, "u1", "first", user))
	drained(t, s, "t1 asked", func() bool { return len(s.openAsks(a.AgentID)) == 1 })
	if err := fh.Session(ref).Reply(ctx, "t1", loomharness.Reply{Allow: true}); err != nil {
		t.Fatal(err)
	}
	drained(t, s, "turn 1 ended", func() bool { return s.get(t, a.AgentID).RunningTurnID == nil })
	mustSendMsg(t, s, sendReq(a.AgentID, "c1", "waits for turn/1", child)) // accepted, though it cannot be handed over
	if clock.fire() == 0 {                                                 // the queued retry, which fails too
		t.Fatal("no retry queued")
	}
	settled(t, s)
	if got := s.get(t, a.AgentID); slotState(t, s, a.AgentID, "c1") != loomstore.SlotWaiting || got.AttentionReason != nil {
		t.Fatalf("after a failed capture: c1 %s, Attention %v; want waiting, none", slotState(t, s, a.AgentID, "c1"), deref(got.AttentionReason))
	}
	mu.Lock()
	failing = false
	mu.Unlock()
	clock.fire()
	drained(t, s, "c1 handed after the retry", func() bool { return len(handedReqs(t, s, a.AgentID, "c1")) == 1 })
	if _, ok := e.ws.checkpoint(checkpointRef(a.AgentID, 1)); !ok {
		t.Fatal("turn/1 missing after the retry")
	}
}

// TestBaselineFailureHoldsFirstPrompt: when the turn/0 capture fails as
// Create ends, the first message is not prompted; the reconcile retry
// captures turn/0 and only then hands it over.
func TestBaselineFailureHoldsFirstPrompt(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	e.ws.setTree("at create")
	var mu sync.Mutex
	failing := true
	e.ws.capture = func(string) error {
		mu.Lock()
		defer mu.Unlock()
		if failing {
			return errors.New("disk full")
		}
		return nil
	}
	s := e.service(ServiceConfig{})
	clock := useTestClock(s)
	runDispatcher(t, s)
	req := leadReq("r1")
	req.FirstMessage = "hello"
	info, err := s.Create(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	id := info.AgentID
	settled(t, s)
	if a := s.get(t, id); turnsStarted(e, sessionOf(a)) != 0 || slotState(t, s, id, "create:r1") != loomstore.SlotWaiting {
		t.Fatalf("turn/0 failed, yet turns = %d, first message %s; want 0, waiting", turnsStarted(e, sessionOf(a)), slotState(t, s, id, "create:r1"))
	}
	if _, ok := e.ws.checkpoint(checkpointRef(id, 0)); ok {
		t.Fatal("turn/0 exists after a failed capture")
	}
	mu.Lock()
	failing = false
	mu.Unlock()
	if clock.fire() == 0 {
		t.Fatal("no retry queued")
	}
	drained(t, s, "first message handed after turn/0", func() bool { return len(handedReqs(t, s, id, "create:r1")) == 1 })
	if a := s.get(t, id); turnsStarted(e, sessionOf(a)) != 1 {
		t.Fatalf("turns = %d after the retry; want 1", turnsStarted(e, sessionOf(a)))
	}
	wantCheckpoints(t, e.ws, id, "at create")
}

// TestCheckpointSwitchMidTurn: a harness switch that stops a running turn
// counts that turn's end before the new session's first hand-over, so turn/1
// is captured while the next message still waits, whenever the old
// session's own end arrives.
func TestCheckpointSwitchMidTurn(t *testing.T) {
	ctx := context.Background()
	e := newSwitchEnv(t, StateActive)
	e.startTurn(t)
	to := e.s.get(t, "a1").StateOf()
	to.RunningTurn = sp("turn_0")
	if _, err := e.s.store.CommitState(ctx, "a1", e.s.get(t, "a1").StateOf(), to, e.s.get(t, "a1").Revision, nil); err != nil {
		t.Fatal(err)
	}
	waitingAtTurn1 := ""
	e.ws.capture = func(ref string) error {
		if ref == checkpointRef("a1", 1) {
			waitingAtTurn1 = slotState(t, e.s, "a1", "r-next")
		}
		return nil
	}
	runDispatcher(t, e.s)
	settled(t, e.s)
	mustSendMsg(t, e.s, sendReq("a1", "r-next", "next", user))
	if _, err := e.s.Update(ctx, switchReq("r1", 1, "fb")); err != nil {
		t.Fatal(err)
	}
	drained(t, e.s, "the hand-over", func() bool { return len(handedReqs(t, e.s, "a1", "r-next")) == 1 })
	if waitingAtTurn1 != loomstore.SlotWaiting {
		t.Fatalf("r-next at the turn/1 capture = %q; want it captured while r-next waited", waitingAtTurn1)
	}
	late := loomharness.Event{Type: loomharness.EventTurnCompleted, TurnID: "turn_0", StopReason: "cancelled", Session: e.old}
	if _, err := e.s.events.Append(ctx, nativeRow("a1", EventTurnCompleted, late)); err != nil { // the old session's own end, late
		t.Fatal(err)
	}
	if n, err := e.s.store.CountEvents(ctx, "a1", EventTurnCompleted); err != nil || n != 1 {
		t.Fatalf("saved turn ends = %d, %v; want the stopped turn's one", n, err)
	}
}

// TestCheckpointOwedAfterArchive: a ref still owed when the agent is
// archived (its working copy stays) is captured by the reconcile retry.
func TestCheckpointOwedAfterArchive(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s := e.service(ServiceConfig{})
	clock := useTestClock(s)
	runDispatcher(t, s)
	runFeed(t, s, "opencode")
	a, ref := newLead(t, e, s, "alpha")
	fh.Script(a.AgentID, fake.Turn{Steps: []fake.Step{{Ask: "t1"}}})
	var mu sync.Mutex
	failing := true
	e.ws.capture = func(string) error {
		mu.Lock()
		defer mu.Unlock()
		if failing {
			return errors.New("disk full")
		}
		return nil
	}
	mustSendMsg(t, s, sendReq(a.AgentID, "u1", "first", user))
	drained(t, s, "t1 asked", func() bool { return len(s.openAsks(a.AgentID)) == 1 })
	if err := fh.Session(ref).Reply(ctx, "t1", loomharness.Reply{Allow: true}); err != nil {
		t.Fatal(err)
	}
	drained(t, s, "turn 1 ended", func() bool { return s.get(t, a.AgentID).State == StateIdle })
	if err := s.Archive(ctx, ArchiveRequest{AgentID: a.AgentID, Reason: ArchiveDone}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	failing = false
	mu.Unlock()
	if clock.fire() == 0 {
		t.Fatal("no retry queued")
	}
	drained(t, s, "turn/1 captured after the archive", func() bool { _, ok := e.ws.checkpoint(checkpointRef(a.AgentID, 1)); return ok })
}

// TestCheckpointFailureRetriedWithoutSession: a failed capture is retried
// even when the agent's harness is not wired; it is never permanent.
func TestCheckpointFailureRetriedWithoutSession(t *testing.T) {
	a := svcAgent("a1", "persistent", StateIdle)
	a.WorktreePath = sp("/wt/a1")
	ws := &fakeWorkspace{capture: func(string) error { return errors.New("disk full") }}
	s := newService(t, ServiceConfig{Workspace: ws}, a)
	if err := s.reconcileAgent(context.Background(), "a1"); !isCheckpointFailed(err) {
		t.Fatalf("reconcile = %v; want the capture failure, for the queue to retry", err)
	}
}

// TestCheckpointHandedEndedWhileDown: a message handed just before a crash
// ran its whole turn while Loom was down, and no backfill saved its end
// (it failed). The recovery saves that end, so turn/1 is captured before the
// next message is handed over.
func TestCheckpointHandedEndedWhileDown(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s := e.service(ServiceConfig{})
	a, _ := newLead(t, e, s, "alpha")
	fh.Script(a.AgentID, fake.Turn{}, fake.Turn{Steps: []fake.Step{{Ask: "t2"}}})
	if !crashDispatchAt(t, "prompted")(func() { _, _ = s.Send(ctx, sendReq(a.AgentID, "u1", "go", user)) }) {
		t.Fatal("did not crash")
	}
	s = e.service(ServiceConfig{}) // Loom restarts; its backfill failed, so no feed or reconcile runs
	waiting := ""
	e.ws.capture = func(ref string) error {
		if ref == checkpointRef(a.AgentID, 1) {
			waiting = slotState(t, s, a.AgentID, "c1")
		}
		return nil
	}
	mustSendMsg(t, s, sendReq(a.AgentID, "c1", "next", child))
	if waiting != loomstore.SlotWaiting || len(handedReqs(t, s, a.AgentID, "c1")) != 1 {
		t.Fatalf("c1 at the turn/1 capture = %q, handed %v; want turn/1 captured while c1 waited, then c1 handed",
			waiting, handedReqs(t, s, a.AgentID, "c1"))
	}
}

// TestCheckpointArchiveCancelledSavesEnd: Archive(cancelled) saves the end of
// the turn it stops before the agent is archived, so its ref stays owed with
// no feed: the native end when the history has it (its own stop reason),
// else a cancelled one.
func TestCheckpointArchiveCancelledSavesEnd(t *testing.T) {
	for _, c := range []struct {
		name string
		turn fake.Turn
		stop func(string) bool
	}{
		{"running", fake.Turn{Steps: []fake.Step{{Ask: "t1"}}}, func(r string) bool { return r != "" }},
		{"ended natively", fake.Turn{}, func(r string) bool { return r != "cancelled" && r != "" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			e := newCreateEnv(t)
			fh := e.h.Harness.(*fake.Harness)
			s := e.service(ServiceConfig{})
			a, _ := newLead(t, e, s, "alpha")
			fh.Script(a.AgentID, c.turn)
			mustSendMsg(t, s, sendReq(a.AgentID, "u1", "go", user)) // no feed: the row's turn still runs
			if err := s.Archive(ctx, ArchiveRequest{AgentID: a.AgentID, Reason: ArchiveCancelled}); err != nil {
				t.Fatal(err)
			}
			ends := kinds(rows(t, s, a.AgentID, 0), EventTurnCompleted)
			var p struct{ StopReason string }
			if len(ends) == 1 {
				_ = json.Unmarshal(ends[0].Payload, &p)
			}
			if len(ends) != 1 || !c.stop(p.StopReason) {
				t.Fatalf("saved ends = %d, stop reason %q", len(ends), p.StopReason)
			}
			if err := s.reconcileAgent(ctx, a.AgentID); err != nil {
				t.Fatal(err)
			}
			if _, ok := e.ws.checkpoint(checkpointRef(a.AgentID, 1)); !ok {
				t.Fatal("turn/1 missing for the archived agent")
			}
		})
	}
}

// TestCheckpointRecoveredEndSaveFails: when saving a recovered turn's end
// fails, the handed message stays handed, so the retry saves the end and
// captures turn/1 before the next message is handed over.
func TestCheckpointRecoveredEndSaveFails(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s := e.service(ServiceConfig{})
	a, _ := newLead(t, e, s, "alpha")
	fh.Script(a.AgentID, fake.Turn{}, fake.Turn{Steps: []fake.Step{{Ask: "t2"}}})
	if !crashDispatchAt(t, "prompted")(func() { _, _ = s.Send(ctx, sendReq(a.AgentID, "u1", "go", user)) }) {
		t.Fatal("did not crash")
	}
	s = e.service(ServiceConfig{}) // Loom restarts
	lift := failSaving(t, e, EventTurnCompleted)
	if _, err := s.Send(ctx, sendReq(a.AgentID, "c1", "next", child)); err == nil {
		t.Fatal("the recovery saved the end despite the failing store")
	}
	lift()
	if err := s.Dispatch(ctx, a.AgentID); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.ws.checkpoint(checkpointRef(a.AgentID, 1)); !ok || len(handedReqs(t, s, a.AgentID, "c1")) != 1 {
		t.Fatalf("turn/1 exists %v, c1 handed %v; want turn/1 before c1", ok, handedReqs(t, s, a.AgentID, "c1"))
	}
}

// TestCheckpointStopUnwiredHarnessSavesEnd: Archive(cancelled) of an agent
// whose harness is not wired still saves the stopped turn's end, under its
// recorded session, so its ref stays owed.
func TestCheckpointStopUnwiredHarnessSavesEnd(t *testing.T) {
	ctx := context.Background()
	a := svcAgent("a1", "persistent", StateActive)
	a.WorktreePath, a.RunningTurnID = sp("/wt/a1"), sp("turn_1")
	a.HarnessSessionRoot, a.HarnessSessionID = sp("/root/fake"), sp("ses_1")
	s := newService(t, ServiceConfig{Workspace: &fakeWorkspace{}, Interrupt: func(context.Context, loomstore.Agent) error { return nil }}, a)
	if err := s.Archive(ctx, ArchiveRequest{AgentID: "a1", Reason: ArchiveCancelled}); err != nil {
		t.Fatal(err)
	}
	ends := kinds(rows(t, s, "a1", 0), EventTurnCompleted)
	if len(ends) != 1 || ends[0].EventID != EventTurnCompleted+":/root/fake:ses_1:turn_1" {
		t.Fatalf("saved ends = %+v; want turn_1's under its recorded session", ends)
	}
}
