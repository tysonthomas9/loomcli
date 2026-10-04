package loomagent

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// drainGuard bounds a Drain in a test. It never makes a test pass: a
// converted test settles by draining, and only a hang reaches the guard.
const drainGuard = 30 * time.Second

// settled waits until s's background loops have handled everything queued.
func settled(t *testing.T, s *Service) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), drainGuard)
	defer cancel()
	if err := s.Drain(ctx); err != nil {
		t.Fatalf("drain: %v", err)
	}
}

// drained drains s, then requires ok: what s's loops owed is done, so ok
// holds now or never will. A test that needs a timeout to pass is wrong.
func drained(t *testing.T, s *Service, what string, ok func() bool) {
	t.Helper()
	settled(t, s)
	if !ok() {
		t.Fatalf("after draining: not %s", what)
	}
}

// testClock is a hand-driven clock for a Service's completion-retry ticker
// and feed backoff: nothing fires until the test fires it.
type testClock struct {
	ticks  chan time.Time
	mu     sync.Mutex
	timers []chan time.Time
	waits  []time.Duration // every backoff asked for, in order
}

// useTestClock puts s on a new testClock; call it before s's loops start.
func useTestClock(s *Service) *testClock {
	c := &testClock{ticks: make(chan time.Time)}
	s.tick = func(time.Duration) (<-chan time.Time, func()) { return c.ticks, func() {} }
	s.after = func(d time.Duration) <-chan time.Time {
		c.mu.Lock()
		defer c.mu.Unlock()
		ch := make(chan time.Time, 1)
		c.timers, c.waits = append(c.timers, ch), append(c.waits, d)
		return ch
	}
	return c
}

// tick hands the dispatcher one completion-retry tick; it returns once the
// dispatcher took it, and a drain then waits for the retry it runs.
func (c *testClock) tick(t *testing.T) {
	t.Helper()
	select {
	case c.ticks <- time.Now():
	case <-time.After(drainGuard):
		t.Fatal("the dispatcher took no tick")
	}
}

// fire fires every pending backoff timer and returns how many it fired.
func (c *testClock) fire() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ch := range c.timers {
		ch <- time.Now()
	}
	n := len(c.timers)
	c.timers = nil
	return n
}

// backoffs is every backoff the feed asked for, in order.
func (c *testClock) backoffs() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.waits)
}

// runDispatcher runs s's dispatcher until the test ends. It is registered
// for Drain before it starts, so a drain waits for its start-up sweep.
func runDispatcher(t *testing.T, s *Service) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	l, done := s.startLoop(), make(chan struct{})
	go func() { defer close(done); defer s.stopLoop(l); s.runDispatcher(ctx, l) }()
	t.Cleanup(func() { cancel(); <-done })
}

// runFeed runs s's feed of harness, registered for Drain before it starts;
// stop ends it (the test's end does too).
func runFeed(t *testing.T, s *Service, harness string) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	l, done := s.startLoop(), make(chan struct{})
	go func() { defer close(done); defer s.stopLoop(l); s.runFeed(ctx, harness, l) }()
	stop = sync.OnceFunc(func() { cancel(); <-done })
	t.Cleanup(stop)
	return stop
}

// pump applies the harness feed to s as the 1.6d ingestion will: each event
// goes to the agent that owns its session. It is one of s's loops, so a
// drain waits for the events the harness already sent.
func pump(t *testing.T, s *Service, h loomharness.Harness, st *loomstore.Store) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	feed, err := h.Feed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	l, done := s.startLoop(), make(chan struct{})
	t.Cleanup(func() { cancel(); _ = feed.Close(); <-done })
	apply := func(e loomharness.Event) bool {
		id, err := st.NativeSessionOwner(ctx, "opencode", e.Session.Root, e.Session.NativeID)
		if err == nil {
			_ = s.HarnessEvent(ctx, id, e)
		}
		return true
	}
	go func() {
		defer close(done)
		defer s.stopLoop(l)
		for {
			select {
			case req := <-l.drain:
				if !settle(req, feed.Events(), apply) {
					return
				}
			case e, ok := <-feed.Events():
				if !ok {
					return
				}
				apply(e)
			}
		}
	}()
}

// newLead creates a lead on e's fake harness and returns it with its session.
func newLead(t *testing.T, e *createEnv, s *Service, name string) (loomstore.Agent, loomharness.NativeRef) {
	t.Helper()
	info, err := s.Create(context.Background(), CreateRequest{Envelope: Envelope{RequestID: name}, Preset: "lead",
		Name: name, Repo: "/repo", BaseRef: "main", Overrides: Overrides{Harness: "opencode"}})
	if err != nil {
		t.Fatal(err)
	}
	a := s.get(t, info.AgentID)
	return a, loomharness.NativeRef{Root: *a.HarnessSessionRoot, NativeID: *a.HarnessSessionID}
}

func slotState(t *testing.T, s *Service, agentID, requestID string) string {
	t.Helper()
	slots, err := s.store.Slots(context.Background(), agentID)
	if err != nil {
		t.Fatal(err)
	}
	for _, sl := range slots {
		if sl.RequestID == requestID {
			return sl.State
		}
	}
	return ""
}

// turnsRun is how many turns the fake ran on ref.
func turnsRun(e *createEnv, ref loomharness.NativeRef) int {
	_, turns := e.h.Harness.(*fake.Harness).Rules(ref)
	return len(turns)
}

// handedReqs lists which of reqs were handed over, by their receipts (a
// sender's slot row is reused by its next message).
func handedReqs(t *testing.T, s *Service, agentID string, reqs ...string) []string {
	t.Helper()
	var out []string
	for _, req := range reqs {
		rec, err := s.store.GetReceipt(context.Background(), agentID, req)
		if err != nil {
			t.Fatal(err)
		}
		if r, _ := decodeResult(rec); r.State == loomstore.SlotHanded {
			out = append(out, req)
		}
	}
	return out
}

// crashDispatchAt makes the dispatcher crash at point; the returned func runs
// f and reports whether it crashed there.
func crashDispatchAt(t *testing.T, point string) func(func()) bool {
	t.Helper()
	type crash struct{}
	dispatchCrash = func(p string) {
		if p == point {
			panic(crash{})
		}
	}
	t.Cleanup(func() { dispatchCrash = func(string) {} })
	return func(f func()) (crashed bool) {
		defer func() {
			if r := recover(); r != nil {
				if _, ok := r.(crash); !ok {
					panic(r)
				}
				crashed = true
				dispatchCrash = func(string) {}
			}
		}()
		f()
		return false
	}
}

// TestHarnessSwitchIdleDispatchUnderAgentLock drives the 1.5→1.6 hand-off: a
// harness switch stops a running turn and publishes agent.idle while it holds
// the agent lock, with one message waiting. Nothing else dispatches: only the
// real dispatcher's Bus wake on agent.idle can hand the message over. The
// switch returns, and the message is handed over once, on the new session.
func TestHarnessSwitchIdleDispatchUnderAgentLock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := newSwitchEnv(t, StateActive)
	e.startTurn(t)
	to := e.s.get(t, "a1").StateOf()
	to.RunningTurn = sp("turn_0")
	if err := e.s.store.CompareAndSetState(ctx, "a1", e.s.get(t, "a1").StateOf(), to); err != nil {
		t.Fatal(err)
	}
	runDispatcher(t, e.s)
	settled(t, e.s)                                            // its start-up sweep is done: it follows the Bus
	mustSendMsg(t, e.s, sendReq("a1", "r-next", "next", user)) // waits: a turn runs
	if got := slotState(t, e.s, "a1", "r-next"); got != loomstore.SlotWaiting {
		t.Fatalf("slot = %s; want waiting", got)
	}
	switched := make(chan error, 1)
	go func() { _, err := e.s.Update(ctx, switchReq("r1", 1, "fb")); switched <- err }()
	select {
	case err := <-switched:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("deadlock: the switch never returned")
	}
	a := e.s.get(t, "a1")
	newRef := loomharness.NativeRef{Root: "/root/fb", NativeID: *a.HarnessSessionID}
	key := defaultInputKey("fb", "a1", "r-next")
	drained(t, e.s, "the hand-over", func() bool { return slotState(t, e.s, "a1", "r-next") == loomstore.SlotHanded })
	if l, _ := e.fb.Session(newRef).HasInput(ctx, key); l != loomharness.LandedFound {
		t.Fatalf("message on the new session: %s", l)
	}
	if l, _ := e.fa.Session(e.old).HasInput(ctx, key); l != loomharness.LandedNotFound {
		t.Fatalf("message on the old session: %s", l)
	}
	// No second dispatch: more wakes find the turn running.
	_ = e.s.Dispatch(ctx, "a1")
	settled(t, e.s)
	if _, turns := e.fb.Rules(newRef); len(turns) != 1 {
		t.Fatalf("turns on the new session = %d; want 1", len(turns))
	}
	if a := e.s.get(t, "a1"); a.State != StateActive || deref(a.RunningTurnID) == "" {
		t.Fatalf("after the hand-over: %s turn %v", a.State, a.RunningTurnID)
	}
}

// TestDispatchCreateFirstMessageOnceAcrossRestart: the creator's first
// message is handed over when Create finishes, exactly once; a restart and a
// replayed Create hand nothing again.
func TestDispatchCreateFirstMessageOnceAcrossRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e := newCreateEnv(t)
	req := leadReq("r1")
	req.FirstMessage = "hello"
	info, err := e.service(ServiceConfig{}).Create(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	s := e.service(ServiceConfig{}) // restart
	runDispatcher(t, s)             // its start-up sweep
	if _, err := s.Create(ctx, req); err != nil {
		t.Fatal(err)
	}
	if err := s.Dispatch(ctx, info.AgentID); err != nil {
		t.Fatal(err)
	}
	settled(t, s)
	a := s.get(t, info.AgentID)
	ref := loomharness.NativeRef{Root: *a.HarnessSessionRoot, NativeID: *a.HarnessSessionID}
	if n := turnsRun(e, ref); n != 1 || slotState(t, s, a.AgentID, "create:r1") != loomstore.SlotHanded {
		t.Fatalf("first message turns = %d, slot %s", n, slotState(t, s, a.AgentID, "create:r1"))
	}
	if l, _ := e.h.Session(ref).HasInput(ctx, defaultInputKey("opencode", a.AgentID, "create:r1")); l != loomharness.LandedFound {
		t.Fatalf("first message landed = %s", l)
	}
}

// TestDispatchHandedCrashRecovery covers a crash between the hand-over and
// the state change: on restart the dispatcher asks the harness whether the
// input landed. Not found: it goes back in line and is prompted once with
// the same key. Found: it is marked delivered and never resent. Unknown:
// Attention delivery_unknown and nothing is sent.
func TestDispatchHandedCrashRecovery(t *testing.T) {
	for _, c := range []struct {
		name      string
		delivery  loomharness.Landed
		ask       bool
		wantSlot  string
		wantTurns int
		wantState string
		attention string
	}{
		{"not_found", loomharness.LandedNotFound, false, loomstore.SlotHanded, 1, StateActive, ""},
		{"found_running", loomharness.LandedFound, true, loomstore.SlotDelivered, 1, StateActive, ""},
		{"found_ended", loomharness.LandedFound, false, loomstore.SlotDelivered, 1, StateIdle, ""},
		{"unknown", loomharness.LandedUnknown, false, loomstore.SlotHanded, 0, StateIdle, AttentionDeliveryUnknown},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			e := newCreateEnv(t)
			fh := e.h.Harness.(*fake.Harness)
			s := e.service(ServiceConfig{})
			a, ref := newLead(t, e, s, "alpha")
			turn := fake.Turn{Delivery: c.delivery}
			if c.ask {
				turn.Steps = []fake.Step{{Ask: "ask1"}}
			}
			fh.Script(a.AgentID, turn)
			run := crashDispatchAt(t, "prompted")
			if !run(func() { _, _ = s.Send(ctx, sendReq(a.AgentID, "s1", "go", user)) }) {
				t.Fatal("did not crash")
			}
			if c.delivery != loomharness.LandedFound {
				if err := fh.Restart(ctx); err != nil { // the scripted delivery killed the harness
					t.Fatal(err)
				}
			}
			s = e.service(ServiceConfig{}) // Loom restarts
			if err := s.Dispatch(ctx, a.AgentID); err != nil {
				t.Fatal(err)
			}
			if err := s.Dispatch(ctx, a.AgentID); err != nil { // repeating changes nothing
				t.Fatal(err)
			}
			got := s.get(t, a.AgentID)
			landed := 0
			if l, _ := e.h.Session(ref).HasInput(ctx, defaultInputKey("opencode", a.AgentID, "s1")); l == loomharness.LandedFound {
				landed = 1
			}
			if st := slotState(t, s, a.AgentID, "s1"); st != c.wantSlot || landed != c.wantTurns ||
				got.State != c.wantState || deref(got.AttentionReason) != c.attention {
				t.Fatalf("slot %s landed %d state %s attention %q", st, landed, got.State, deref(got.AttentionReason))
			}
		})
	}
}

// TestDispatchOldestFirstOneTurnAtATime: messages from three senders wait
// while a turn runs; each turn end hands over exactly the next one, oldest
// first, after staging skills and installing the current policy; nothing is
// handed while a turn runs and a delivered message is never resent.
func TestDispatchOldestFirstOneTurnAtATime(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	var mu sync.Mutex
	caps, prepared := BridgeCaps{}, 0
	s := e.service(ServiceConfig{
		Bridge: func(context.Context, Preset) (BridgeCaps, error) { mu.Lock(); defer mu.Unlock(); return caps, nil },
		PrepareWorktree: func(context.Context, Target, loomstore.Agent) error {
			mu.Lock()
			defer mu.Unlock()
			prepared++
			return nil
		},
	})
	pump(t, s, e.h, e.st)
	a, ref := newLead(t, e, s, "alpha")
	mu.Lock()
	prepared = 0 // Create stages skills when it opens the session; count hand-overs only
	mu.Unlock()
	ask := func(id string) fake.Turn { return fake.Turn{Steps: []fake.Step{{Delta: id}, {Ask: id}}} }
	fh.Script(a.AgentID, ask("t1"), ask("t2"), ask("t3"), ask("t4"))

	if r := mustSendMsg(t, s, sendReq(a.AgentID, "u1", "first", user)); r.State != "handed" {
		t.Fatalf("Send to an idle agent = %+v; want handed", r)
	}
	reqs := []string{"u1", "c1", "x1", "u2"}
	mustSendMsg(t, s, sendReq(a.AgentID, "c1", "child done", child))
	mustSendMsg(t, s, sendReq(a.AgentID, "x1", "from the system", ActorRef{Kind: "system", ID: "x"}))
	drained(t, s, "u1 delivered", func() bool { return slotState(t, s, a.AgentID, "u1") == loomstore.SlotDelivered })
	mustSendMsg(t, s, sendReq(a.AgentID, "u2", "second", user)) // the user's slot is free again
	if err := s.Dispatch(ctx, a.AgentID); err != nil {          // a turn runs: nothing more
		t.Fatal(err)
	}
	if got := handedReqs(t, s, a.AgentID, reqs...); !slices.Equal(got, []string{"u1"}) {
		t.Fatalf("handed = %v while a turn runs", got)
	}
	mu.Lock()
	caps = BridgeCaps{HasGitHubRead: true, HasPublish: true} // the bridge registers before the next turn
	mu.Unlock()
	for i, ask := range []string{"t1", "t2", "t3"} {
		if err := fh.Session(ref).Reply(ctx, ask, loomharness.Reply{Allow: true}); err != nil {
			t.Fatal(err)
		}
		// Oldest first, one per turn: c1 and x1 waited before u2 was sent.
		drained(t, s, "the next hand-over", func() bool { return len(handedReqs(t, s, a.AgentID, reqs...)) == i+2 })
		if got := handedReqs(t, s, a.AgentID, reqs...); !slices.Equal(got, reqs[:i+2]) {
			t.Fatalf("after turn %d handed = %v; want %v", i+1, got, reqs[:i+2])
		}
	}
	_, turns := fh.Rules(ref)
	if hasPublishDenies(turns[0]) || !hasPublishDenies(turns[len(turns)-1]) {
		t.Fatalf("turn rules: first %v, last %v; want the current policy at each hand-over", turns[0], turns[len(turns)-1])
	}
	mu.Lock()
	defer mu.Unlock()
	if prepared != 4 {
		t.Fatalf("PrepareWorktree ran %d times for 4 hand-overs", prepared)
	}
	if err := fh.Session(ref).Reply(ctx, "t4", loomharness.Reply{Allow: true}); err != nil {
		t.Fatal(err)
	}
	drained(t, s, "idle", func() bool { return s.get(t, a.AgentID).State == StateIdle })
	slots, _ := s.store.Slots(ctx, a.AgentID)
	for _, sl := range slots {
		if sl.State != loomstore.SlotDelivered {
			t.Fatalf("%s's %s = %s; want delivered", sl.Sender, sl.RequestID, sl.State)
		}
	}
	if err := s.Dispatch(ctx, a.AgentID); err != nil || s.get(t, a.AgentID).State != StateIdle {
		t.Fatalf("a delivered message was handed again: %v", err)
	}
}

// TestDispatchArchiveDoneAndSingleTaskOutcome: Archive(done) on a busy lead
// lets the turn and the waiting messages finish, then archives; a single
// task finishes with its turn's stop reason as the outcome.
func TestDispatchArchiveDoneAndSingleTaskOutcome(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s := e.service(ServiceConfig{})
	pump(t, s, e.h, e.st)
	a, ref := newLead(t, e, s, "alpha")
	fh.Script(a.AgentID, fake.Turn{Steps: []fake.Step{{Ask: "t1"}}}, fake.Turn{Steps: []fake.Step{{Ask: "t2"}}})
	mustSendMsg(t, s, sendReq(a.AgentID, "u1", "first", user))
	mustSendMsg(t, s, sendReq(a.AgentID, "c1", "child done", child))
	if err := s.Archive(ctx, ArchiveRequest{AgentID: a.AgentID, Reason: ArchiveDone}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, sendReq(a.AgentID, "u2", "late", user)); !isCode(err, CodeAgentArchived) {
		t.Fatalf("Send while stopping = %v", err)
	}
	if err := fh.Session(ref).Reply(ctx, "t1", loomharness.Reply{Allow: true}); err != nil {
		t.Fatal(err)
	}
	drained(t, s, "the waiting message's turn", func() bool { return len(handedReqs(t, s, a.AgentID, "u1", "c1")) == 2 })
	if got := s.get(t, a.AgentID); got.State != StateStopping {
		t.Fatalf("state = %s; want stopping until the waiting message ran", got.State)
	}
	if err := fh.Session(ref).Reply(ctx, "t2", loomharness.Reply{Allow: true}); err != nil {
		t.Fatal(err)
	}
	drained(t, s, "archived", func() bool { return s.get(t, a.AgentID).State == StateArchived })

	info, err := s.Create(ctx, CreateRequest{Envelope: Envelope{RequestID: "w1"}, Preset: "daemon-worker", Name: "w",
		Repo: "/repo", BaseRef: "main", Overrides: Overrides{Harness: "opencode"}, FirstMessage: "fix it"})
	if err != nil {
		t.Fatal(err)
	}
	drained(t, s, "the task finished", func() bool { return s.get(t, info.AgentID).State == StateFinished })
	if got := s.get(t, info.AgentID); deref(got.Outcome) != "completed" || got.FinishedAt == nil || got.RunningTurnID != nil {
		t.Fatalf("finished task: outcome %q finished_at %v turn %v", deref(got.Outcome), got.FinishedAt, got.RunningTurnID)
	}
}

// TestDispatchHandedIgnoresOtherSessionEvents: a turn.completed from a session
// that is not the agent's current one (another NativeID, or the same NativeID
// under another root), or for a turn that is not running, changes nothing.
func TestDispatchHandedIgnoresOtherSessionEvents(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s := e.service(ServiceConfig{})
	a, ref := newLead(t, e, s, "alpha")
	fh.Script(a.AgentID, fake.Turn{Steps: []fake.Step{{Ask: "t1"}}})
	mustSendMsg(t, s, sendReq(a.AgentID, "u1", "first", user))
	run := deref(s.get(t, a.AgentID).RunningTurnID)
	done := func(ref loomharness.NativeRef, turn string) loomharness.Event {
		return loomharness.Event{Type: loomharness.EventTurnCompleted, Session: ref, TurnID: turn, StopReason: "completed"}
	}
	for _, ev := range []loomharness.Event{
		done(loomharness.NativeRef{Root: ref.Root, NativeID: "other"}, run),
		done(loomharness.NativeRef{Root: "/root/other", NativeID: ref.NativeID}, run),
		done(ref, "an-old-turn"),
	} {
		if err := s.HarnessEvent(ctx, a.AgentID, ev); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.get(t, a.AgentID); got.State != StateActive || got.RunningTurnID == nil {
		t.Fatalf("a stray turn.completed ended the turn: %s %v", got.State, got.RunningTurnID)
	}
	if err := s.HarnessEvent(ctx, a.AgentID, done(ref, run)); err != nil || s.get(t, a.AgentID).State != StateIdle {
		t.Fatalf("the running turn's own completion did not end it: %v", err)
	}
}

// TestDispatchStaleCompletionKeepsNextTurn: an older turn's late
// turn.completed or turn.started, arriving after the next message was handed
// over (before or after its delivery), changes nothing; only the new turn's
// own start, which names its input, binds it, and only its completion ends it.
func TestDispatchStaleCompletionKeepsNextTurn(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	a, ref := newLead(t, e, s, "alpha")
	ev := func(typ loomharness.EventType, turn, key string) {
		t.Helper()
		if err := s.HarnessEvent(ctx, a.AgentID, loomharness.Event{Type: typ, Session: ref, TurnID: turn,
			InputKey: key, StopReason: "completed"}); err != nil {
			t.Fatal(err)
		}
	}
	running := func(want string) {
		t.Helper()
		if got := s.get(t, a.AgentID); got.State != StateActive || deref(got.RunningTurnID) != want {
			t.Fatalf("running turn = %s %v; want active %s", got.State, got.RunningTurnID, want)
		}
	}
	k1, k2 := defaultInputKey("", a.AgentID, "u1"), defaultInputKey("", a.AgentID, "c1")
	mustSendMsg(t, s, sendReq(a.AgentID, "u1", "first", user))
	ev(loomharness.EventMessageDelivered, "", k1)
	ev(loomharness.EventTurnStarted, "T1", k1)
	mustSendMsg(t, s, sendReq(a.AgentID, "c1", "child done", child))
	ev(loomharness.EventTurnCompleted, "T1", "") // hands c1 over
	running(k2)
	ev(loomharness.EventTurnStarted, "T1", k1) // the old turn's late start, c1 still handed
	ev(loomharness.EventTurnStarted, "T1", "")
	running(k2)
	ev(loomharness.EventMessageDelivered, "", k2)
	ev(loomharness.EventTurnCompleted, "T1", "") // the old turn's late completion
	running(k2)
	ev(loomharness.EventTurnStarted, "T2", k2)
	ev(loomharness.EventTurnStarted, "T1", k1) // a stale start never renames a named turn
	running("T2")
	ev(loomharness.EventTurnCompleted, "T2", "")
	if got := s.get(t, a.AgentID); got.State != StateIdle || got.RunningTurnID != nil {
		t.Fatalf("T2's completion: %s %v", got.State, got.RunningTurnID)
	}
}

// TestNoBridgeFailsClosed: with no host bridge wired, a preset with bridge
// tools (lead) gets no policy, so it never launches; one without tools does.
func TestNoBridgeFailsClosed(t *testing.T) {
	s := New(ServiceConfig{})
	lead, _ := BuiltinPresets{}.Get(context.Background(), "lead")
	task, _ := BuiltinPresets{}.Get(context.Background(), "task")
	if _, err := s.policy(context.Background(), Config{Preset: lead}); err == nil {
		t.Fatal("lead got a policy with no bridge wired")
	}
	if _, err := s.policy(context.Background(), Config{Preset: task}); err != nil {
		t.Fatalf("task: %v", err)
	}
}
