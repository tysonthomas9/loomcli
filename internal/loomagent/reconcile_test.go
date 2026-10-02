package loomagent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// restarted is a harness client in a Loom process that just started: like
// the OpenCode adapter (1.4), a session refuses Reply with ErrQuarantined
// until this client opened or resumed it. With move set, Resume returns the
// session under a new NativeID ("moved-" prefix) that maps back to the
// wrapped one, as a recovery that changes the native ref would. A history
// read waits for block, if set, and fails for session failID.
type restarted struct {
	loomharness.Harness
	move      bool
	block     chan struct{}
	failID    string
	mu        sync.Mutex
	installed map[loomharness.NativeRef]bool // by the wrapped ref
	resumes   int
	statuses  int // Status calls; a Reconcile pass ends with one per running turn
}

func newRestarted(h loomharness.Harness, move bool) *restarted {
	return &restarted{Harness: h, move: move, installed: map[loomharness.NativeRef]bool{}}
}

func (r *restarted) inner(ref loomharness.NativeRef) loomharness.NativeRef {
	ref.NativeID = strings.TrimPrefix(ref.NativeID, "moved-")
	return ref
}

func (r *restarted) Open(ctx context.Context, spec loomharness.OpenSpec) (loomharness.NativeRef, error) {
	ref, err := r.Harness.Open(ctx, spec)
	if err == nil {
		r.mu.Lock()
		r.installed[ref] = true
		r.mu.Unlock()
	}
	return ref, err
}

func (r *restarted) Session(ref loomharness.NativeRef) loomharness.Session {
	return restartedSession{Session: r.Harness.Session(r.inner(ref)), r: r, ref: ref}
}

func (r *restarted) resumeCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.resumes
}

type restartedSession struct {
	loomharness.Session
	r   *restarted
	ref loomharness.NativeRef
}

func (x restartedSession) Resume(ctx context.Context, l loomharness.Launch, rules []loomharness.PermissionRule) (loomharness.NativeRef, error) {
	got, err := x.Session.Resume(ctx, l, rules)
	if err != nil {
		return got, err
	}
	x.r.mu.Lock()
	x.r.resumes++
	x.r.installed[got] = true
	x.r.mu.Unlock()
	if x.r.move {
		got.NativeID = "moved-" + got.NativeID
	}
	return got, nil
}

func (x restartedSession) Status(ctx context.Context) (loomharness.Status, error) {
	x.r.mu.Lock()
	x.r.statuses++
	x.r.mu.Unlock()
	return x.Session.Status(ctx)
}

func (x restartedSession) Reply(ctx context.Context, askID string, r loomharness.Reply) error {
	x.r.mu.Lock()
	ok := x.r.installed[x.r.inner(x.ref)]
	x.r.mu.Unlock()
	if !ok {
		return fmt.Errorf("opencode: %w", loomharness.ErrQuarantined)
	}
	return x.Session.Reply(ctx, askID, r)
}

func (x restartedSession) Messages(ctx context.Context, after string, limit int) (loomharness.MessagePage, error) {
	if x.r.block != nil {
		<-x.r.block
	}
	if x.r.failID != "" && x.r.inner(x.ref).NativeID == x.r.failID {
		return loomharness.MessagePage{}, errors.New("history read failed")
	}
	page, err := x.Session.Messages(ctx, after, limit)
	for i := range page.Events {
		page.Events[i].Session = x.ref
	}
	return page, err
}

// waitingOnAsk makes a lead whose running turn waits on ask a1, then stops
// the serve that ran it.
func waitingOnAsk(t *testing.T, e *createEnv) loomstore.Agent {
	t.Helper()
	s := e.service(ServiceConfig{})
	stop := startFeed(s, e)
	defer stop()
	a, _ := newLead(t, e, s, "alpha")
	e.h.Harness.(*fake.Harness).Script(a.AgentID, fake.Turn{Steps: []fake.Step{{Ask: "a1"}, {Delta: "done"}}})
	mustSendMsg(t, s, sendReq(a.AgentID, "u1", "go", user))
	eventually(t, "a1 opens", func() bool { return s.get(t, a.AgentID).State == StateWaiting })
	return s.get(t, a.AgentID)
}

func reconcile(t *testing.T, s *Service) {
	t.Helper()
	if err := s.Reconcile(context.Background(), "opencode"); err != nil {
		t.Fatal(err)
	}
}

// TestReconcileCreatingRecordsNativeRef: a serve that crashed after Open
// returned (before or after the ownership write) leaves the row creating;
// Reconcile adopts the same session, records its NativeRef, finishes Create
// with one agent.created, and a repeat changes nothing.
func TestReconcileCreatingRecordsNativeRef(t *testing.T) {
	ctx := context.Background()
	for _, point := range []string{"recorded", "session"} {
		e := newCreateEnv(t)
		run := crashAt(t, point)
		if !run(func() { _, _ = e.service(ServiceConfig{}).Create(ctx, leadReq("r1")) }) {
			t.Fatalf("%s: did not crash", point)
		}
		s := e.service(ServiceConfig{}) // the restart
		reconcile(t, s)
		row, err := e.st.FindCreated(ctx, "ws", "", "r1")
		if err != nil {
			t.Fatal(err)
		}
		owned, _ := e.st.NativeSessions(ctx, row.AgentID)
		if row.State != StateIdle || row.CreateStep != stepDone || len(owned) != 1 ||
			owned[0].NativeID != deref(row.HarnessSessionID) || owned[0].NativeRoot != deref(row.HarnessSessionRoot) {
			t.Fatalf("%s: state %s step %d, owned %+v, session %s", point, row.State, row.CreateStep, owned, deref(row.HarnessSessionID))
		}
		if len(e.h.specs) != 2 || e.h.specs[0].Key != e.h.specs[1].Key {
			t.Fatalf("%s: %d opens; want the crashed one and one replay with the same key", point, len(e.h.specs))
		}
		before := len(rows(t, s, row.AgentID, 0))
		reconcile(t, s)
		if n := len(rows(t, s, row.AgentID, 0)); n != before || len(e.h.specs) != 2 || e.events(t, row.AgentID, KindAgentCreated) != 1 {
			t.Fatalf("%s: a repeat added %d rows and %d opens", point, n-before, len(e.h.specs)-2)
		}
	}
}

// TestReconcileResumeRecordsNewNativeRef: after a restart, Reconcile
// resumes the session of a turn left running; the different NativeRef
// Resume returns is recorded as owned and becomes current, the old one
// stays owned, the open ask is kept, and a repeat resumes nothing again.
func TestReconcileResumeRecordsNewNativeRef(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	a := waitingOnAsk(t, e)
	old := deref(a.HarnessSessionID)
	s := e.service(ServiceConfig{})
	w := newRestarted(e.h, true)
	s.harnesses["opencode"] = w
	reconcile(t, s)
	row := s.get(t, a.AgentID)
	owned, _ := e.st.NativeSessions(ctx, a.AgentID)
	ids := []string{}
	for _, n := range owned {
		ids = append(ids, n.NativeID)
	}
	if deref(row.HarnessSessionID) != "moved-"+old || !slices.Contains(ids, old) || !slices.Contains(ids, "moved-"+old) {
		t.Fatalf("current %s, owned %v; want moved-%s current and both owned", deref(row.HarnessSessionID), ids, old)
	}
	if w.resumeCount() != 1 || row.State != StateWaiting || !slices.Equal(askIDs(t, s, a.AgentID), []string{"a1:approval"}) {
		t.Fatalf("resumes %d, state %s, asks %v", w.resumeCount(), row.State, askIDs(t, s, a.AgentID))
	}
	before := len(rows(t, s, a.AgentID, 0))
	reconcile(t, s)
	again, _ := e.st.NativeSessions(ctx, a.AgentID)
	if w.resumeCount() != 1 || len(again) != len(owned) || len(rows(t, s, a.AgentID, 0)) != before ||
		deref(s.get(t, a.AgentID).HarnessSessionID) != "moved-"+old {
		t.Fatalf("a repeat: resumes %d, owned %d -> %d, rows %d -> %d", w.resumeCount(), len(owned), len(again), before, len(rows(t, s, a.AgentID, 0)))
	}
}

// TestReconcileRestartOpenAskRespond (from 1.4): after a Loom restart an
// OpenCode session refuses Reply until this process installs its rules.
// Reconcile resumes it, the backfill reopens the ask, and Respond succeeds;
// the turn then finishes and the agent goes idle.
func TestReconcileRestartOpenAskRespond(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	a := waitingOnAsk(t, e)
	s := e.service(ServiceConfig{})
	w := newRestarted(e.h, false)
	s.harnesses["opencode"] = w
	reconcile(t, s)
	if err := s.Respond(ctx, RespondRequest{AgentID: a.AgentID, AskID: "a1", Decision: "allow_once"}); err != nil {
		t.Fatalf("Respond after Reconcile = %v", err)
	}
	stop := startFeed(s, e)
	defer stop()
	eventually(t, "the turn ends", func() bool { return s.get(t, a.AgentID).State == StateIdle })
	if n := len(kinds(rows(t, s, a.AgentID, 0), KindAskLost)); n != 0 || w.resumeCount() != 1 {
		t.Fatalf("ask.lost rows %d, resumes %d; want 0 and 1", n, w.resumeCount())
	}
}

// TestReconcileInterruptedTurnsAndHandedMessages: three leads were cut off
// by a harness crash while Loom was down. A's turn crashed mid-way: Resume
// ends it and A goes idle with one turn end. B's input is unknown: B shows
// delivery_unknown and nothing is resent. C's input never landed: it goes
// back in line and is handed over once. A repeat changes nothing.
func TestReconcileInterruptedTurnsAndHandedMessages(t *testing.T) {
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s1 := e.service(ServiceConfig{})
	var leads []loomstore.Agent
	var refs []loomharness.NativeRef
	for _, name := range []string{"a", "b", "c"} {
		a, ref := newLead(t, e, s1, name)
		leads, refs = append(leads, a), append(refs, ref)
	}
	fh.Script(leads[0].AgentID, fake.Turn{Steps: []fake.Step{{Delta: "x"}, {Crash: true}}})
	fh.Script(leads[1].AgentID, fake.Turn{Delivery: loomharness.LandedUnknown})
	fh.Script(leads[2].AgentID, fake.Turn{Delivery: loomharness.LandedNotFound})
	for _, a := range leads {
		mustSendMsg(t, s1, sendReq(a.AgentID, "u1", "go", user))
		_ = fh.Restart(context.Background())
	}

	s := e.service(ServiceConfig{}) // the restart
	check := func(when string) {
		t.Helper()
		ag, bg, cg := s.get(t, leads[0].AgentID), s.get(t, leads[1].AgentID), s.get(t, leads[2].AgentID)
		if ag.State != StateIdle || ag.RunningTurnID != nil || len(kinds(rows(t, s, ag.AgentID, 0), EventTurnCompleted)) != 1 {
			t.Fatalf("%s: A %s running %v", when, ag.State, ag.RunningTurnID)
		}
		if deref(bg.AttentionReason) != AttentionDeliveryUnknown || slotState(t, s, bg.AgentID, "u1") != loomstore.SlotHanded || turnsRun(e, refs[1]) != 0 {
			t.Fatalf("%s: B Attention %q slot %s turns %d", when, deref(bg.AttentionReason), slotState(t, s, bg.AgentID, "u1"), turnsRun(e, refs[1]))
		}
		if turnsRun(e, refs[2]) != 1 || cg.AttentionReason != nil {
			t.Fatalf("%s: C turns %d Attention %q", when, turnsRun(e, refs[2]), deref(cg.AttentionReason))
		}
	}
	reconcile(t, s)
	reconcile(t, s) // C's turn, run by the first pass, is backfilled here
	check("after Reconcile")
	before := len(rows(t, s, leads[0].AgentID, 0)) + len(rows(t, s, leads[1].AgentID, 0)) + len(rows(t, s, leads[2].AgentID, 0))
	reconcile(t, s)
	check("after a repeat")
	if after := len(rows(t, s, leads[0].AgentID, 0)) + len(rows(t, s, leads[1].AgentID, 0)) + len(rows(t, s, leads[2].AgentID, 0)); after != before {
		t.Fatalf("a repeat added %d rows", after-before)
	}
}

// afterRead is a harness that calls then after each history read.
type afterRead struct {
	loomharness.Harness
	then func()
}

func (h afterRead) Session(ref loomharness.NativeRef) loomharness.Session {
	return afterReadSession{h.Harness.Session(ref), h.then}
}

type afterReadSession struct {
	loomharness.Session
	then func()
}

func (x afterReadSession) Messages(ctx context.Context, after string, limit int) (loomharness.MessagePage, error) {
	page, err := x.Session.Messages(ctx, after, limit)
	x.then()
	return page, err
}

// TestReconcileTurnEndedAfterBackfill: a turn handed over and ended natively
// after the backfill read the history, and before settle, is not ended as
// lost: its native end is applied by the next backfill, so agent.idle names
// the native turn and follows its agent.turn_completed.
func TestReconcileTurnEndedAfterBackfill(t *testing.T) {
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	a, _ := newLead(t, e, s, "alpha")
	var once sync.Once
	s.harnesses["opencode"] = afterRead{e.h, func() {
		once.Do(func() { mustSendMsg(t, s, sendReq(a.AgentID, "u1", "go", user)) }) // the fake runs the whole turn
	}}
	reconcile(t, s)
	if got := s.get(t, a.AgentID); got.RunningTurnID == nil || len(kinds(rows(t, s, a.AgentID, 0), EventIdle)) != 0 {
		t.Fatalf("Reconcile ended the natively ended turn as lost: state %s", got.State)
	}
	reconcile(t, s)
	all := rows(t, s, a.AgentID, 0)
	tc, idle := kinds(all, EventTurnCompleted), kinds(all, EventIdle)
	if len(tc) != 1 || len(idle) != 1 || idle[0].TurnID != tc[0].TurnID || idle[0].Seq < tc[0].Seq {
		t.Fatalf("turn_completed %v idle %v; want one each, idle after and naming the native turn", ids(tc), ids(idle))
	}
}

// TestReconcileFinishesDeleteAndFlagsMissingSession: a Delete left
// half done is finished; a session the harness lost shows session_missing.
func TestReconcileFinishesDeleteAndFlagsMissingSession(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s1 := e.service(ServiceConfig{})
	del, _ := newLead(t, e, s1, "del")
	gone, ref := newLead(t, e, s1, "gone")
	if err := e.st.MarkDeleteRequested(ctx, del.AgentID); err != nil {
		t.Fatal(err)
	}
	if err := e.h.Purge(ctx, []loomharness.NativeRef{ref}); err != nil {
		t.Fatal(err)
	}
	s := e.service(ServiceConfig{})
	reconcile(t, s)
	if row, _ := e.st.GetAgent(ctx, del.AgentID); row.DeletedAt == nil || e.events(t, del.AgentID, EventDeleted) != 1 {
		t.Fatalf("delete not finished: %+v", row.DeletedAt)
	}
	if r := deref(s.get(t, gone.AgentID).AttentionReason); r != AttentionSessionMissing {
		t.Fatalf("Attention %q; want session_missing", r)
	}
}

// TestReconcileGatesWritesUntilRecovered: with RecoverFirst, a Respond that
// arrives while start-up recovery is still reading history waits for it,
// then finds the rebuilt ask and succeeds; an idle session is not resumed
// at boot (§4.15: lazily, before its next Prompt or Respond).
func TestReconcileGatesWritesUntilRecovered(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := newCreateEnv(t)
	a := waitingOnAsk(t, e)
	idle, _ := newLead(t, e, e.service(ServiceConfig{}), "idle")
	s := e.service(ServiceConfig{RecoverFirst: true})
	w := newRestarted(e.h, false)
	w.block = make(chan struct{})
	s.harnesses["opencode"] = w
	go s.RunDispatcher(ctx)
	done := make(chan error, 1)
	go func() {
		done <- s.Respond(ctx, RespondRequest{AgentID: a.AgentID, AskID: "a1", Decision: "allow_once"})
	}()
	select {
	case err := <-done:
		t.Fatalf("Respond returned %v before recovery", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(w.block)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Respond after recovery = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Respond never returned")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	ref := loomharness.NativeRef{Root: deref(idle.HarnessSessionRoot), NativeID: deref(idle.HarnessSessionID)}
	if w.resumes != 1 || w.installed[ref] {
		t.Fatalf("resumes %d, idle session installed %v; want only the session with the open ask resumed", w.resumes, w.installed[ref])
	}
}

// TestRespondResumesLazily: after a restart the backfill rebuilds an open
// ask without a Resume; Respond resumes the session first, so the Reply is
// not refused as quarantined, and a second resume is not needed.
func TestRespondResumesLazily(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	a := waitingOnAsk(t, e)
	s := e.service(ServiceConfig{})
	w := newRestarted(e.h, false)
	s.harnesses["opencode"] = w
	if _, err := s.backfill(ctx, "opencode"); err != nil {
		t.Fatal(err)
	}
	if w.resumeCount() != 0 {
		t.Fatalf("resumes %d before Respond", w.resumeCount())
	}
	if err := s.Respond(ctx, RespondRequest{AgentID: a.AgentID, AskID: "a1", Decision: "allow_once"}); err != nil {
		t.Fatalf("Respond = %v", err)
	}
	if n := w.resumeCount(); n != 1 {
		t.Fatalf("resumes %d; want Respond's one", n)
	}
}

// TestReconcileOneAgentFailureDoesNotBlockOthers: A's history read fails;
// B's turn, which ended while Loom was down, is still backfilled (its native
// turn end is saved once) and B goes idle. Reconcile reports A's failure
// and leaves A as it was.
func TestReconcileOneAgentFailureDoesNotBlockOthers(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	a, _ := newLead(t, e, e.service(ServiceConfig{}), "a")
	b := waitingOnAsk(t, e)
	ref := loomharness.NativeRef{Root: deref(b.HarnessSessionRoot), NativeID: deref(b.HarnessSessionID)}
	if err := e.h.Session(ref).Reply(ctx, "a1", loomharness.Reply{Allow: true}); err != nil {
		t.Fatal(err) // B's turn ends while Loom is down
	}
	s := e.service(ServiceConfig{})
	w := newRestarted(e.h, false)
	w.failID = deref(a.HarnessSessionID)
	s.harnesses["opencode"] = w
	if err := s.Reconcile(ctx, "opencode"); err == nil || !strings.Contains(err.Error(), a.AgentID) {
		t.Fatalf("Reconcile = %v; want A's failure", err)
	}
	if st := s.get(t, b.AgentID).State; st != StateIdle {
		t.Fatalf("B is %s; want idle", st)
	}
	if n := e.events(t, b.AgentID, EventTurnCompleted); n != 1 {
		t.Fatalf("B has %d saved turn ends; want 1 backfilled from its native history", n)
	}
	if ag := s.get(t, a.AgentID); ag.State != StateIdle || ag.AttentionReason != nil {
		t.Fatalf("A is %s with Attention %q; want it unchanged", ag.State, deref(ag.AttentionReason))
	}
}

// TestReconcileResumesAgainAfterHarnessRestart: each harness restart under
// one serve resumes the session of a turn waiting on an ask again (its
// process and rules are new; the fake continues the turn to its next ask),
// while a feed.gap Reconcile resumes nothing.
func TestReconcileResumesAgainAfterHarnessRestart(t *testing.T) {
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s1 := e.service(ServiceConfig{})
	stop1 := startFeed(s1, e)
	a, _ := newLead(t, e, s1, "alpha")
	fh.Script(a.AgentID, fake.Turn{ResumeContinues: true,
		Steps: []fake.Step{{Ask: "a1"}, {Ask: "a2"}, {Ask: "a3"}, {Delta: "x", Gap: true}}})
	mustSendMsg(t, s1, sendReq(a.AgentID, "u1", "go", user))
	eventually(t, "a1 opens", func() bool { return s1.get(t, a.AgentID).State == StateWaiting })
	stop1()

	s := e.service(ServiceConfig{})
	w := newRestarted(e.h, false)
	s.harnesses["opencode"] = w
	stop := startFeed(s, e)
	defer stop()
	passDone := func(n int) func() bool { // pass n resumed, then checked the turn's status
		return func() bool {
			w.mu.Lock()
			defer w.mu.Unlock()
			return w.resumes == n && w.statuses >= n
		}
	}
	eventually(t, "the start-up pass", passDone(1))
	for n := 2; n <= 3; n++ {
		_ = fh.Restart(context.Background())
		eventually(t, fmt.Sprintf("pass %d", n), passDone(n))
	}
	eventually(t, "a3 opens", func() bool { return slices.Equal(askIDs(t, s, a.AgentID), []string{"a3:approval"}) })
	if err := s.Respond(context.Background(), RespondRequest{AgentID: a.AgentID, AskID: "a3", Decision: "allow_once"}); err != nil {
		t.Fatal(err) // the rest of the turn misses the live feed: a feed.gap
	}
	eventually(t, "the turn ends", func() bool { return s.get(t, a.AgentID).State == StateIdle })
	if n := w.resumeCount(); n != 3 {
		t.Fatalf("resumes %d; want 3: the gap or Respond resumed again", n)
	}
}
