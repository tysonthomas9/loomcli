package loomagent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// restarted is a harness client in a Loom process that just started: like
// the OpenCode adapter (1.4), a session refuses Reply with ErrQuarantined
// until this client opened or resumed it. With move set, Resume returns the
// session under a new NativeID ("moved-" prefix) that maps back to the
// wrapped one, as a recovery that changes the native ref would.
type restarted struct {
	loomharness.Harness
	move      bool
	mu        sync.Mutex
	installed map[loomharness.NativeRef]bool // by the wrapped ref
	resumes   int
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
