package loomagent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

var (
	usageLimit = &loomharness.Failure{Class: loomharness.FailureUsageLimit, Retryable: true}
	limitFail  = fake.Turn{Steps: []fake.Step{{Fail: "usage limit", Failure: usageLimit}}}
)

// limitEnv is a Lead on harness whose feed and dispatcher run, on a
// hand-driven resync clock and usage-limit clock.
type limitEnv struct {
	t       *testing.T
	e       *createEnv
	harness string
	s       *Service
	c       *testClock
	fh      *fake.Harness
	a       loomstore.Agent
	mu      sync.Mutex
	at      time.Time
	stop    func()
}

func newLimitEnv(t *testing.T, harness string, optIn bool) *limitEnv {
	t.Helper()
	l := &limitEnv{t: t, e: newCreateEnv(t), harness: harness, at: time.Now()}
	l.fh = l.e.h.Harness.(*fake.Harness)
	l.start()
	if err := l.s.SetLimitResume(context.Background(), optIn); err != nil {
		t.Fatal(err)
	}
	info, err := l.s.Create(context.Background(), CreateRequest{Envelope: Envelope{RequestID: "alpha"}, Preset: "lead",
		Name: "alpha", Repo: "/repo", BaseRef: "main", Overrides: Overrides{Harness: harness}})
	if err != nil {
		t.Fatal(err)
	}
	l.a = l.s.get(t, info.AgentID)
	return l
}

// start starts a service on the env's store, as a loom serve (re)start does.
func (l *limitEnv) start() {
	s := l.e.service(ServiceConfig{})
	s.harnesses = map[string]loomharness.Harness{l.harness: l.e.h}
	l.c = useTestClock(s)
	s.now = func() time.Time { l.mu.Lock(); defer l.mu.Unlock(); return l.at }
	stopFeed := runFeed(l.t, s, l.harness)
	ctx, cancel := context.WithCancel(context.Background())
	run, done := s.Dispatcher(), make(chan struct{})
	go func() { defer close(done); run(ctx) }()
	l.s, l.stop = s, sync.OnceFunc(func() { cancel(); <-done; stopFeed() })
	l.t.Cleanup(l.stop)
	settled(l.t, s)
}

func (l *limitEnv) restart() { l.stop(); l.start() }

// advance moves the clock by d, then ticks the resync clock and waits for
// what it started, including a resumed turn.
func (l *limitEnv) advance(d time.Duration) {
	l.t.Helper()
	l.mu.Lock()
	l.at = l.at.Add(d)
	l.mu.Unlock()
	l.c.tick(l.t)
	settled(l.t, l.s)
}

// userTurn runs one turn from the user, as scripted, to its end.
func (l *limitEnv) userTurn(id string) {
	l.t.Helper()
	mustSendMsg(l.t, l.s, sendReq(l.a.AgentID, id, id, user))
	drained(l.t, l.s, "the turn ended", func() bool { return l.s.get(l.t, l.a.AgentID).State == StateIdle })
}

// resumes are the agent's resume receipts' request IDs, oldest first.
func (l *limitEnv) resumes() []string {
	l.t.Helper()
	rs, err := l.s.store.SenderReceipts(context.Background(), l.a.AgentID, limitResumeSender)
	if err != nil {
		l.t.Fatal(err)
	}
	var ids []string
	for _, r := range rs {
		if r.Body == nil || *r.Body != limitResumeText {
			l.t.Fatalf("resume %s body = %v; want %q", r.RequestID, r.Body, limitResumeText)
		}
		ids = append(ids, r.RequestID)
	}
	return ids
}

// owed is the agent's resume owed, or nil.
func (l *limitEnv) owed() *loomstore.LimitResume {
	l.t.Helper()
	r, err := l.s.store.GetLimitResume(context.Background(), l.a.AgentID)
	if errors.Is(err, loomstore.ErrNotFound) {
		return nil
	} else if err != nil {
		l.t.Fatal(err)
	}
	return &r
}

func (l *limitEnv) wantResumes(what string, n int) {
	l.t.Helper()
	if got := l.resumes(); len(got) != n {
		l.t.Fatalf("%s: resumes %v; want %d", what, got, n)
	}
}

// TestUsageLimitResumeOnSchedule (OR7): a turn that fails on a usage limit
// is resumed once, 15 minutes later: nothing a second before; after, two
// ticks and a restart still make exactly one resume, from loom:limit-resume.
// The resumed turn ends well, which ends the episode.
func TestUsageLimitResumeOnSchedule(t *testing.T) {
	l := newLimitEnv(t, "opencode", true)
	l.fh.Script(l.a.AgentID, limitFail)
	l.userTurn("u1")
	r := l.owed()
	if r == nil || r.Attempt != 1 || r.DueAt != loomstore.Stamp(l.at.Add(15*time.Minute)) {
		t.Fatalf("owed after the limit = %+v; want attempt 1 due in 15m", r)
	}
	l.advance(15*time.Minute - time.Second)
	l.wantResumes("a second before due", 0)
	l.advance(2 * time.Second)
	l.wantResumes("after due", 1)
	l.advance(time.Second)
	l.restart()
	l.advance(time.Hour)
	l.wantResumes("after more ticks and a restart", 1)
	if got, want := l.resumes()[0], "limit-resume:"+l.a.AgentID+":"+r.TurnID+":1"; got != want {
		t.Fatalf("resume request id %s; want %s", got, want)
	}
	if r := l.owed(); r != nil {
		t.Fatalf("owed after the resumed turn ended well = %+v; want none", r)
	}
	if a := l.s.get(t, l.a.AgentID); a.State != StateIdle {
		t.Fatalf("state %s; want idle", a.State)
	}
}

// TestUsageLimitScheduleAndCap (OR7): limits in a row are resumed after
// 15m, 30m, 1h, 1h, 1h and 2h; the seventh is not resumed.
func TestUsageLimitScheduleAndCap(t *testing.T) {
	l := newLimitEnv(t, "opencode", true)
	for range 7 {
		l.fh.Script(l.a.AgentID, limitFail)
	}
	l.userTurn("u1")
	for i, d := range []time.Duration{15 * time.Minute, 30 * time.Minute, time.Hour, time.Hour, time.Hour, 2 * time.Hour} {
		r := l.owed()
		if r == nil || r.Attempt != int64(i+1) || r.DueAt != loomstore.Stamp(l.at.Add(d)) {
			t.Fatalf("limit %d owes %+v; want attempt %d due in %s", i+1, r, i+1, d)
		}
		l.advance(d - time.Second)
		l.wantResumes("before due", i)
		l.advance(time.Second)
		l.wantResumes("at due", i+1)
	}
	if r := l.owed(); r != nil && r.DueAt != "" {
		t.Fatalf("the seventh limit in a row owes %+v; want none", r)
	}
	l.advance(24 * time.Hour)
	l.wantResumes("after the cap", 6)
}

// TestUsageLimitRepeatedSweep (OR7): sweeps back to back, even at once,
// send one resume and consume it.
func TestUsageLimitRepeatedSweep(t *testing.T) {
	l := newLimitEnv(t, "opencode", true)
	l.fh.Script(l.a.AgentID, limitFail, fake.Turn{Steps: []fake.Step{{Ask: "hold"}}}) // the resumed turn stays running
	l.userTurn("u1")
	l.mu.Lock()
	l.at = l.at.Add(time.Hour)
	l.mu.Unlock()
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() { l.s.sweepLimitResumes(context.Background()) })
	}
	wg.Wait()
	l.s.sweepLimitResumes(context.Background())
	settled(t, l.s)
	l.wantResumes("three sweeps at once and one after", 1)
	if r := l.owed(); r == nil || r.DueAt != "" {
		t.Fatalf("owed after the resume = %+v; want it marked sent", r)
	}
}

// TestUsageLimitArchiveRace (OR7): an agent archived between the sweep's
// read and its lock is not resumed, and its resume is dropped.
func TestUsageLimitArchiveRace(t *testing.T) {
	l := newLimitEnv(t, "opencode", true)
	l.fh.Script(l.a.AgentID, limitFail)
	l.userTurn("u1")
	l.mu.Lock()
	l.at = l.at.Add(time.Hour)
	l.mu.Unlock()
	ctx := context.Background()
	due, err := l.s.store.DueLimitResumes(ctx, "ws", l.s.now()) // the sweep's read
	if err != nil || len(due) != 1 {
		t.Fatalf("due = %+v, %v", due, err)
	}
	if err := l.s.Archive(ctx, ArchiveRequest{Envelope: Envelope{RequestID: "arch"}, AgentID: l.a.AgentID}); err != nil {
		t.Fatal(err)
	}
	if err := l.s.limitResume(ctx, due[0].AgentID); err != nil { // the rest of the sweep
		t.Fatal(err)
	}
	l.wantResumes("archived", 0)
	if r := l.owed(); r != nil {
		t.Fatalf("owed after archive = %+v; want dropped", r)
	}
}

// TestUsageLimitWaitsForOtherTurn (OR7): while another input's turn runs
// (a child's record, which is not a Send), a due resume waits and stays
// owed: that turn's end decides.
func TestUsageLimitWaitsForOtherTurn(t *testing.T) {
	l := newLimitEnv(t, "opencode", true)
	l.fh.Script(l.a.AgentID, limitFail, fake.Turn{Steps: []fake.Step{{Ask: "hold"}}})
	l.userTurn("u1")
	ctx := context.Background()
	if _, err := l.s.store.Notify(ctx, l.a.AgentID, "agent:kid", "system", []loomstore.Notice{{Key: "rec1", Text: "kid done"}},
		func(string, bool) (string, error) { return "{}", nil }); err != nil {
		t.Fatal(err)
	}
	if err := l.s.dispatchWake(ctx, l.a.AgentID); err != nil {
		t.Fatal(err)
	}
	drained(t, l.s, "the record's turn runs", func() bool { return l.s.get(t, l.a.AgentID).RunningTurnID != nil })
	l.advance(time.Hour)
	l.wantResumes("while another turn runs", 0)
	if r := l.owed(); r == nil || r.DueAt == "" {
		t.Fatalf("owed while another turn runs = %+v; want still owed", r)
	}
}

// TestUsageLimitOptInOff (OR7): with the workspace not opted in, a limit
// owes nothing and nothing is resumed.
func TestUsageLimitOptInOff(t *testing.T) {
	l := newLimitEnv(t, "opencode", false)
	l.fh.Script(l.a.AgentID, limitFail)
	l.userTurn("u1")
	if r := l.owed(); r != nil {
		t.Fatalf("owed while opted out = %+v", r)
	}
	l.advance(24 * time.Hour)
	l.wantResumes("opted out", 0)
}

// TestUsageLimitOptInTurnedOff (OR7): opting out between the limit and its
// due time sends nothing and drops the resume.
func TestUsageLimitOptInTurnedOff(t *testing.T) {
	l := newLimitEnv(t, "opencode", true)
	l.fh.Script(l.a.AgentID, limitFail)
	l.userTurn("u1")
	if l.owed() == nil {
		t.Fatal("no resume owed after the limit")
	}
	if err := l.s.SetLimitResume(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	l.advance(time.Hour)
	l.wantResumes("opted out before due", 0)
	if r := l.owed(); r != nil {
		t.Fatalf("owed after opting out = %+v; want dropped", r)
	}
}

// TestUsageLimitUserSendEndsEpisode (OR7): a user message before the
// resume is due drops it; nothing is resumed later.
func TestUsageLimitUserSendEndsEpisode(t *testing.T) {
	l := newLimitEnv(t, "opencode", true)
	l.fh.Script(l.a.AgentID, limitFail)
	l.userTurn("u1")
	l.userTurn("u2")
	if r := l.owed(); r != nil {
		t.Fatalf("owed after the user's turn = %+v", r)
	}
	l.advance(24 * time.Hour)
	l.wantResumes("after a user message", 0)
}

// TestUsageLimitNonLimitFailureNoResume (OR7): an auth or provider
// failure owes no resume, and a resumed turn that fails another way ends
// the episode.
func TestUsageLimitNonLimitFailureNoResume(t *testing.T) {
	l := newLimitEnv(t, "opencode", true)
	l.fh.Script(l.a.AgentID,
		fake.Turn{Steps: []fake.Step{{Fail: "bad key", Failure: &loomharness.Failure{Class: loomharness.FailureAuth}}}},
		fake.Turn{Steps: []fake.Step{{Fail: "no class"}}},
		limitFail,
		fake.Turn{Steps: []fake.Step{{Fail: "overloaded", Failure: &loomharness.Failure{Class: loomharness.FailureProvider, Retryable: true}}}})
	l.userTurn("u1")
	l.userTurn("u2")
	if r := l.owed(); r != nil {
		t.Fatalf("owed after non-limit failures = %+v", r)
	}
	l.userTurn("u3")
	l.advance(15 * time.Minute)
	l.wantResumes("the limit", 1)
	if r := l.owed(); r != nil {
		t.Fatalf("owed after the resumed turn failed another way = %+v", r)
	}
	l.advance(24 * time.Hour)
	l.wantResumes("after the episode ended", 1)
}

// TestUsageLimitSingleTaskNeverResumes (OR7): only persistent agents are
// resumed; a single task's failure is its parent's to handle.
func TestUsageLimitSingleTaskNeverResumes(t *testing.T) {
	l := newLimitEnv(t, "opencode", true)
	task := l.a
	task.Mode = "single_task"
	e := loomharness.Event{Type: loomharness.EventTurnCompleted, TurnID: "t1", StopReason: "failed", Failure: usageLimit}
	if err := l.s.limitTurnEnded(context.Background(), task, e); err != nil {
		t.Fatal(err)
	}
	if r := l.owed(); r != nil {
		t.Fatalf("a single task owes %+v", r)
	}
}

// TestUsageLimitSameForAllHarnesses (OR7): the resume keys on the failure
// class alone, so Claude, codex and OpenCode resume alike.
func TestUsageLimitSameForAllHarnesses(t *testing.T) {
	for _, h := range Harnesses {
		t.Run(h, func(t *testing.T) {
			l := newLimitEnv(t, h, true)
			l.fh.Script(l.a.AgentID, limitFail)
			l.userTurn("u1")
			l.advance(15*time.Minute - time.Second)
			l.wantResumes("before due", 0)
			l.advance(time.Second)
			l.advance(time.Hour)
			l.wantResumes("after due", 1)
		})
	}
}

// TestUsageLimitStopEndsEpisode (OR7): a Stop with no message, which saves
// only its receipt, also ends the episode: the resume is dropped.
func TestUsageLimitStopEndsEpisode(t *testing.T) {
	l := newLimitEnv(t, "opencode", true)
	l.fh.Script(l.a.AgentID, limitFail)
	l.userTurn("u1")
	stop := sendReq(l.a.AgentID, "stop1", "", user)
	stop.Delivery = DeliveryInterrupt
	mustSendMsg(t, l.s, stop)
	if r := l.owed(); r != nil && r.DueAt != "" {
		t.Fatalf("owed after a Stop = %+v; want none due", r)
	}
	l.advance(24 * time.Hour)
	l.wantResumes("after a Stop", 0)
}

// TestUsageLimitHarnessSwitchDrops (OR7): a resume is owed to the session
// whose turn hit the limit. After a harness switch the agent runs on a new
// session, which has nothing to continue: the resume is dropped, not sent.
func TestUsageLimitHarnessSwitchDrops(t *testing.T) {
	ctx := context.Background()
	e := newSwitchEnv(t, StateIdle)
	if err := e.s.SetLimitResume(ctx, true); err != nil {
		t.Fatal(err)
	}
	if err := e.s.store.PutLimitResume(ctx, loomstore.LimitResume{AgentID: "a1", TurnID: "t1", Attempt: 1,
		Session: sessionKey(e.s.get(t, "a1")), DueAt: loomstore.Stamp(time.Now().Add(-time.Second))}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Update(ctx, switchReq("sw", 1, "fb")); err != nil {
		t.Fatal(err)
	}
	e.s.sweepLimitResumes(ctx)
	if rs, err := e.s.store.SenderReceipts(ctx, "a1", limitResumeSender); err != nil || len(rs) != 0 {
		t.Fatalf("resumes after a harness switch = %+v, %v; want none", rs, err)
	}
	if _, err := e.s.store.GetLimitResume(ctx, "a1"); !errors.Is(err, loomstore.ErrNotFound) {
		t.Fatalf("owed after a harness switch: %v; want dropped", err)
	}
}

// TestUsageLimitCapSurvivesReplay (OR7): the end of the seventh limit in a
// row leaves a marker, not nothing, so a replay of that end (a crash before
// the turn's end committed) still owes no resume.
func TestUsageLimitCapSurvivesReplay(t *testing.T) {
	l := newLimitEnv(t, "opencode", true)
	ctx := context.Background()
	a := l.s.get(t, l.a.AgentID)
	if err := l.s.store.PutLimitResume(ctx, loomstore.LimitResume{AgentID: a.AgentID, TurnID: "t6", Attempt: 6,
		Session: sessionKey(a)}); err != nil { // the sixth resume was sent
		t.Fatal(err)
	}
	seventh := loomharness.Event{Type: loomharness.EventTurnCompleted, TurnID: "t7", StopReason: "failed", Failure: usageLimit}
	for range 2 { // the end, then its replay
		if err := l.s.limitTurnEnded(ctx, a, seventh); err != nil {
			t.Fatal(err)
		}
	}
	if due, err := l.s.store.DueLimitResumes(ctx, "ws", l.at.Add(48*time.Hour)); err != nil || len(due) != 0 {
		t.Fatalf("due after the capped end and its replay = %+v, %v; want none", due, err)
	}
}

// TestUsageLimitSessionIncludesHarness (OR7): a resume is bound to its
// harness and session root, not only the native ID, which two harnesses
// may share.
func TestUsageLimitSessionIncludesHarness(t *testing.T) {
	a := loomstore.Agent{Harness: "claude", HarnessSessionRoot: sp("/root/claude"), HarnessSessionID: sp("s1")}
	r := loomstore.LimitResume{Session: sessionKey(a)}
	if resumeVoid(a, r) {
		t.Fatal("void on its own session")
	}
	b := a
	b.Harness, b.HarnessSessionRoot = "codex", sp("/root/codex")
	if !resumeVoid(b, r) {
		t.Fatal("not void after a switch to a session with the same native ID")
	}
}

// TestUsageLimitStopBeforeLimitEnd (OR7): a Stop while a turn runs marks
// that turn, so its usage-limit end, if the feed applies it after the Stop,
// owes no resume.
func TestUsageLimitStopBeforeLimitEnd(t *testing.T) {
	l := newLimitEnv(t, "opencode", true)
	l.fh.Script(l.a.AgentID, fake.Turn{Steps: []fake.Step{{Ask: "hold"}}})
	mustSendMsg(t, l.s, sendReq(l.a.AgentID, "u1", "go", user))
	drained(t, l.s, "the turn runs", func() bool { return l.s.get(t, l.a.AgentID).RunningTurnID != nil })
	l.stop() // the feed lags: the turn's end is not applied before the Stop
	a := l.s.get(t, l.a.AgentID)
	turn := *a.RunningTurnID
	stop := sendReq(l.a.AgentID, "stop1", "", user)
	stop.Delivery = DeliveryInterrupt
	mustSendMsg(t, l.s, stop)
	end := loomharness.Event{Type: loomharness.EventTurnCompleted, TurnID: turn, StopReason: "failed", Failure: usageLimit}
	if err := l.s.limitTurnEnded(context.Background(), a, end); err != nil {
		t.Fatal(err)
	}
	if due, err := l.s.store.DueLimitResumes(context.Background(), "ws", l.at.Add(48*time.Hour)); err != nil || len(due) != 0 {
		t.Fatalf("due after a Stop then the turn's limit end = %+v, %v; want none", due, err)
	}
}
