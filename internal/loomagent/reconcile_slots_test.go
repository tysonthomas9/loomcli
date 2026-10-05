package loomagent

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// gatedStatus is a working copy whose Status counts its calls, fails while
// fail is set, and first waits on block when it is set.
type gatedStatus struct {
	headWorkspace
	calls atomic.Int32
	fail  atomic.Bool
	block chan struct{}
	enter sync.Once
	in    chan struct{} // closed when the first Status starts
}

func (w *gatedStatus) Status(ctx context.Context, s WorkspaceSpec) (WorkspaceStatus, error) {
	w.calls.Add(1)
	w.enter.Do(func() {
		if w.in != nil {
			close(w.in)
		}
	})
	if w.block != nil {
		<-w.block
	}
	if w.fail.Load() {
		return WorkspaceStatus{}, errors.New("status read failed")
	}
	return w.headWorkspace.Status(ctx, s)
}

// markerKid is a lead L with one running child c1 that has a working copy.
func markerKid(t *testing.T, ws *gatedStatus) (*Service, string) {
	t.Helper()
	path := dbPath(t)
	kid := childOf("c1", "L")
	kid.WorktreePath, kid.Branch = sp("/wt/c1"), sp("loom/agent/c1")
	return markerService(t, ServiceConfig{Workspace: ws}, path, busy("L", "persistent", StateActive), kid), path
}

// TestReconcileTwoWakesOneRun: a child's record fails to save, so it is
// queued with its backoff; the resync clock queues it again before that
// backoff ends. The two wakes run the child once until the backoff fires,
// and then the record is saved once.
func TestReconcileTwoWakesOneRun(t *testing.T) {
	ws := &gatedStatus{headWorkspace: headWorkspace{branch: "loom/agent/c1", head: "abc"}}
	s, path := markerKid(t, ws)
	clk := useTestClock(s)
	runDispatcher(t, s)
	settled(t, s)
	ws.fail.Store(true)
	finishTurn(t, s, "c1", "completed")
	settled(t, s)
	clk.tick(t)
	settled(t, s)
	if n := ws.calls.Load(); n != 1 {
		t.Fatalf("record attempts before the backoff = %d; want 1", n)
	}
	ws.fail.Store(false)
	if clk.fire() != 1 {
		t.Fatal("no retry pending")
	}
	settled(t, s)
	if got := completions(t, s, "L"); len(got) != 1 || ws.calls.Load() != 2 || markers(t, path) != 0 {
		t.Fatalf("records %d after %d attempts, markers %d; want one record after 2", len(got), ws.calls.Load(), markers(t, path))
	}
}

// TestReconcileFailedAppendRetries: the record's append fails in its
// transaction; the reconcile queue retries it after its first backoff, on
// the fake clock, and it is saved once.
func TestReconcileFailedAppendRetries(t *testing.T) {
	ws := &gatedStatus{headWorkspace: headWorkspace{branch: "loom/agent/c1", head: "abc"}}
	s, path := markerKid(t, ws)
	clk := useTestClock(s)
	runDispatcher(t, s)
	settled(t, s)
	lift := failRecords(t, path)
	finishTurn(t, s, "c1", "completed")
	settled(t, s)
	if got := completions(t, s, "L"); len(got) != 0 || markers(t, path) != 1 {
		t.Fatalf("setup: records %d markers %d", len(got), markers(t, path))
	}
	lift()
	if clk.fire() != 1 || !slices.Equal(clk.backoffs(), []time.Duration{reconcileBackoff}) {
		t.Fatalf("backoffs = %v; want one of %v", clk.backoffs(), reconcileBackoff)
	}
	settled(t, s)
	if got := completions(t, s, "L"); len(got) != 1 || got[0].Head != "abc" || markers(t, path) != 0 {
		t.Fatalf("records %+v markers %d; want one, with its head", got, markers(t, path))
	}
}

// TestReconcileRestartDeliversMarker: the child's attempt ends and Loom
// crashes before the record is saved. After the restart the dispatcher's
// start saves it once, the lead's slot takes it, and a resync adds nothing.
func TestReconcileRestartDeliversMarker(t *testing.T) {
	ws := &gatedStatus{headWorkspace: headWorkspace{branch: "loom/agent/c1", head: "abc"}}
	s1, path := markerKid(t, ws)
	lift := failRecords(t, path)
	finishTurn(t, s1, "c1", "completed")
	lift()
	l := s1.get(t, "L")
	to := l.StateOf()
	to.RunningTurn, to.State = nil, StateIdle
	if _, err := s1.setState(context.Background(), l, to); err != nil {
		t.Fatal(err)
	}
	s := markerService(t, ServiceConfig{Workspace: ws}, path) // the restart
	clk := useTestClock(s)
	runDispatcher(t, s)
	settled(t, s)
	clk.tick(t)
	settled(t, s)
	if got := completions(t, s, "L"); len(got) != 1 || markers(t, path) != 0 {
		t.Fatalf("records %d markers %d after the restart; want 1 and 0", len(got), markers(t, path))
	}
	if got := waiting(t, s, "L"); len(got) != 1 {
		t.Fatalf("lead slots = %q; want c1's notice", got)
	}
}

// statusOnce is a harness whose sessions' Status fails while fail is above
// zero, once per call.
type statusOnce struct {
	loomharness.Harness
	fail *atomic.Int32
}

func (h statusOnce) Session(ref loomharness.NativeRef) loomharness.Session {
	return statusOnceSession{h.Harness.Session(ref), h.fail}
}

type statusOnceSession struct {
	loomharness.Session
	fail *atomic.Int32
}

func (x statusOnceSession) Status(ctx context.Context) (loomharness.Status, error) {
	if x.fail.Add(-1) >= 0 {
		return loomharness.Status{}, errors.New("status unavailable")
	}
	return x.Session.Status(ctx)
}

// lostTurn creates a lead whose input never landed while its harness
// crashed; the row still shows the turn running.
func lostTurn(t *testing.T, e *createEnv, delivery loomharness.Landed) (loomstore.Agent, loomharness.NativeRef) {
	t.Helper()
	fh := e.h.Harness.(*fake.Harness)
	s1 := e.service(ServiceConfig{})
	a, ref := newLead(t, e, s1, "alpha")
	fh.Script(a.AgentID, fake.Turn{Delivery: delivery})
	mustSendMsg(t, s1, sendReq(a.AgentID, "u1", "go", user))
	if err := fh.Restart(context.Background()); err != nil {
		t.Fatal(err)
	}
	return a, ref
}

// TestReconcileFeedGapSettlesSlots: on a feed gap the harness's Status
// fails once while a lead's lost turn is settled. The agent is queued and
// retried after its backoff: the turn ends and the message that never
// landed is handed over again, once.
func TestReconcileFeedGapSettlesSlots(t *testing.T) {
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	clk := useTestClock(s)
	runDispatcher(t, s)
	settled(t, s)
	a, ref := lostTurn(t, e, loomharness.LandedNotFound)
	fail := &atomic.Int32{}
	s.harnesses["opencode"] = statusOnce{e.h, fail}
	fail.Store(1)
	reconcile(t, s) // the feed gap
	settled(t, s)
	if turnsRun(e, ref) != 0 {
		t.Fatalf("setup: turns %d before the retry", turnsRun(e, ref))
	}
	if clk.fire() != 1 {
		t.Fatal("no retry pending")
	}
	settled(t, s)
	got := s.get(t, a.AgentID)
	if turnsRun(e, ref) != 1 || slotState(t, s, a.AgentID, "u1") == loomstore.SlotWaiting || got.AttentionReason != nil {
		t.Fatalf("turns %d slot %s Attention %q; want the message handed over once and no Attention",
			turnsRun(e, ref), slotState(t, s, a.AgentID, "u1"), deref(got.AttentionReason))
	}
}

// TestReconcileUnknownDeliveryNotResent: after a restart, the dispatcher's
// start settles a lead whose input's fate is unknown: it shows
// delivery_unknown and nothing is resent, by that start, a resync or a retry.
func TestReconcileUnknownDeliveryNotResent(t *testing.T) {
	e := newCreateEnv(t)
	a, ref := lostTurn(t, e, loomharness.LandedUnknown)
	s := e.service(ServiceConfig{}) // the restart
	clk := useTestClock(s)
	runDispatcher(t, s)
	settled(t, s)
	clk.tick(t)
	settled(t, s)
	clk.fire()
	settled(t, s)
	got := s.get(t, a.AgentID)
	if deref(got.AttentionReason) != AttentionDeliveryUnknown || slotState(t, s, a.AgentID, "u1") != loomstore.SlotHanded || turnsRun(e, ref) != 0 {
		t.Fatalf("Attention %q slot %s turns %d; want delivery_unknown, handed, none resent",
			deref(got.AttentionReason), slotState(t, s, a.AgentID, "u1"), turnsRun(e, ref))
	}
}

// TestDrainIncludesInFlight: a Drain asked while the reconcile queue is
// saving a child's record returns only after that save is done.
func TestDrainIncludesInFlight(t *testing.T) {
	ws := &gatedStatus{headWorkspace: headWorkspace{branch: "loom/agent/c1", head: "abc"}, block: make(chan struct{}), in: make(chan struct{})}
	s, _ := markerKid(t, ws)
	runDispatcher(t, s)
	settled(t, s)
	finishTurn(t, s, "c1", "completed")
	<-ws.in // the queue is saving the record
	var released atomic.Bool
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		settled(t, s)
		if !released.Load() {
			t.Error("Drain returned while the record was still being saved")
		}
	}()
	released.Store(true)
	close(ws.block)
	wg.Wait()
	if got := completions(t, s, "L"); len(got) != 1 {
		t.Fatalf("records = %d", len(got))
	}
}

// TestRecoveryClockCallSites: once the dispatcher starts and drains, the
// one clock it asked for is the resync interval.
func TestRecoveryClockCallSites(t *testing.T) {
	ws := &gatedStatus{headWorkspace: headWorkspace{branch: "loom/agent/c1", head: "abc"}}
	s, _ := markerKid(t, ws)
	var mu sync.Mutex
	var asked []time.Duration
	s.tick = func(d time.Duration) (<-chan time.Time, func()) {
		mu.Lock()
		defer mu.Unlock()
		asked = append(asked, d)
		return nil, func() {}
	}
	runDispatcher(t, s)
	settled(t, s)
	finishTurn(t, s, "c1", "completed")
	settled(t, s)
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(asked, []time.Duration{resyncInterval}) {
		t.Fatalf("clocks asked for = %v; want only the resync interval %v", asked, resyncInterval)
	}
}

// TestReconcileDeleteBeforeMarker: a finished child's record is owed and
// its Delete was requested before a crash. Reconcile finishes the Delete
// first, so the parent's record says the child is deleted and has no head.
func TestReconcileDeleteBeforeMarker(t *testing.T) {
	ctx := context.Background()
	ws := &gatedStatus{headWorkspace: headWorkspace{branch: "loom/agent/c1", head: "abc"}}
	s, path := markerKid(t, ws)
	lift := failRecords(t, path)
	finishTurn(t, s, "c1", "completed")
	lift()
	if err := s.store.MarkDeleteRequested(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	if err := s.reconcileAgent(ctx, "c1"); err != nil {
		t.Fatal(err)
	}
	got := completions(t, s, "L")
	if len(got) != 1 || !got[0].ChildDeleted || got[0].Head != "" {
		t.Fatalf("records = %+v; want one, child deleted and no head", got)
	}
}

// TestReconcileRestartDeliversSavedRecord: a child's record was saved but
// Loom crashed before the idle parent, which has no harness session, took
// it. After the restart the dispatcher's start puts it in the parent's slot.
func TestReconcileRestartDeliversSavedRecord(t *testing.T) {
	ws := &gatedStatus{headWorkspace: headWorkspace{branch: "loom/agent/c1", head: "abc"}}
	s1, path := markerKid(t, ws)
	l := s1.get(t, "L")
	to := l.StateOf()
	to.RunningTurn, to.State = nil, StateIdle
	if _, err := s1.setState(context.Background(), l, to); err != nil {
		t.Fatal(err)
	}
	endAttempt(t, s1, "c1", "completed")
	if got := completions(t, s1, "L"); len(got) != 1 || len(waiting(t, s1, "L")) != 0 {
		t.Fatalf("setup: records %d slots %q", len(got), waiting(t, s1, "L"))
	}
	s := markerService(t, ServiceConfig{Workspace: ws}, path) // the restart
	runDispatcher(t, s)
	settled(t, s)
	if got := waiting(t, s, "L"); len(got) != 1 {
		t.Fatalf("lead slots = %q; want c1's notice", got)
	}
}

// TestReconcileUnwiredHarnessNotRetried: a lead's message waits behind a
// running turn on a harness this Loom has not wired. Settling it shows
// harness_unavailable once and queues no retry: none can wire it.
func TestReconcileUnwiredHarnessNotRetried(t *testing.T) {
	ctx := context.Background()
	l := busy("L", "persistent", StateActive)
	l.Harness, l.HarnessSessionID = "codex", sp("ses_1")
	s := markerService(t, ServiceConfig{}, dbPath(t), l)
	clk := useTestClock(s)
	if _, _, err := s.store.Send(ctx, loomstore.SlotSend{AgentID: "L", Sender: "user:u", RequestID: "u1", Body: "go",
		Source: "user_chat", Result: func(bool) (string, error) { return "{}", nil }}); err != nil {
		t.Fatal(err)
	}
	runDispatcher(t, s)
	settled(t, s)
	if r := deref(s.get(t, "L").AttentionReason); r != AttentionHarnessUnavailable || len(clk.backoffs()) != 0 {
		t.Fatalf("Attention %q backoffs %v; want harness_unavailable and no retry", r, clk.backoffs())
	}
}

// waitingLead creates an idle lead with message u1 waiting: break runs
// before the send, so its hand-over fails, and Loom then restarts.
func waitingLead(t *testing.T, e *createEnv, broken func(loomstore.Agent)) (loomstore.Agent, loomharness.NativeRef) {
	t.Helper()
	s1 := e.service(ServiceConfig{})
	a, ref := newLead(t, e, s1, "alpha")
	broken(a)
	_, _ = s1.Send(context.Background(), sendReq(a.AgentID, "u1", "go", user))
	if st := slotState(t, s1, a.AgentID, "u1"); st != loomstore.SlotWaiting {
		t.Fatalf("setup: u1 %s", st)
	}
	return a, ref
}

// TestReconcileFailedHandOffRetries: after a restart the hand-over of a
// waiting message fails while the harness cannot install its rules. The
// queue retries it after its backoff and hands it over once.
func TestReconcileFailedHandOffRetries(t *testing.T) {
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	a, ref := waitingLead(t, e, func(loomstore.Agent) { fh.FailInstall(errors.New("install failed")) })
	s := e.service(ServiceConfig{}) // the restart
	clk := useTestClock(s)
	runDispatcher(t, s)
	settled(t, s)
	fh.FailInstall(nil)
	if clk.fire() != 1 {
		t.Fatal("no retry pending")
	}
	settled(t, s)
	if turnsRun(e, ref) != 1 || slotState(t, s, a.AgentID, "u1") == loomstore.SlotWaiting {
		t.Fatalf("turns %d slot %s; want u1 handed over once", turnsRun(e, ref), slotState(t, s, a.AgentID, "u1"))
	}
}

// TestReconcileUnrecordedSessionNotRetried: an idle lead's message waits
// but its current session is not recorded as its own. Settling it shows
// harness_unavailable and queues no retry: none can record it.
func TestReconcileUnrecordedSessionNotRetried(t *testing.T) {
	e := newCreateEnv(t)
	a, _ := waitingLead(t, e, func(a loomstore.Agent) {
		execSQL(t, e, `UPDATE agents SET harness_session_id = 'ses_unrecorded' WHERE agent_id = ?`, a.AgentID)
	})
	s := e.service(ServiceConfig{}) // the restart
	clk := useTestClock(s)
	runDispatcher(t, s)
	settled(t, s)
	if r := deref(s.get(t, a.AgentID).AttentionReason); r != AttentionHarnessUnavailable || len(clk.backoffs()) != 0 {
		t.Fatalf("Attention %q backoffs %v; want harness_unavailable and no retry", r, clk.backoffs())
	}
}

// TestReconcileRetryShowsDeliveryUnknown: a feed gap's settle fails once on
// Status and shows harness_unavailable; the retry finds the lost turn's
// input unknown, and the lead shows delivery_unknown instead, resending
// nothing.
func TestReconcileRetryShowsDeliveryUnknown(t *testing.T) {
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	clk := useTestClock(s)
	runDispatcher(t, s)
	settled(t, s)
	a, ref := lostTurn(t, e, loomharness.LandedUnknown)
	fail := &atomic.Int32{}
	s.harnesses["opencode"] = statusOnce{e.h, fail}
	fail.Store(1)
	reconcile(t, s)
	settled(t, s)
	clk.fire()
	settled(t, s)
	if r := deref(s.get(t, a.AgentID).AttentionReason); r != AttentionDeliveryUnknown || turnsRun(e, ref) != 0 {
		t.Fatalf("Attention %q turns %d; want delivery_unknown, nothing resent", r, turnsRun(e, ref))
	}
}
