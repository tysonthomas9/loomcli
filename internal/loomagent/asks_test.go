package loomagent

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// askIDs is a Get's open asks as "id:type".
func askIDs(t *testing.T, s *Service, agentID string) []string {
	t.Helper()
	info, err := s.Get(context.Background(), agentID)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, a := range info.OpenAsks {
		out = append(out, a.ID+":"+a.Type)
	}
	return out
}

// tweaked wraps a harness: its history leaves out ask gone (as OpenCode's
// lists only asks still pending), or fails with msgErr (after a first page
// page1, if set; page2, if set, serves the page after it), and ends with
// extra; it waits for block, if set; a Reply with Always
// fails with alwaysErr. It counts history reads and records replies.
type tweaked struct {
	loomharness.Harness
	gone              string
	msgErr, alwaysErr error
	reads             *atomic.Int32
	page1, extra      []loomharness.Event
	page2             func() (loomharness.MessagePage, error)
	block             chan struct{} // a history read waits until it closes
	replies           *[]loomharness.Reply
}

func (w tweaked) Session(ref loomharness.NativeRef) loomharness.Session {
	return tweakedSession{w.Harness.Session(ref), w}
}

type tweakedSession struct {
	loomharness.Session
	w tweaked
}

func (p tweakedSession) Messages(ctx context.Context, after string, limit int) (loomharness.MessagePage, error) {
	if p.w.reads != nil {
		p.w.reads.Add(1)
	}
	if p.w.block != nil {
		<-p.w.block
	}
	if p.w.page1 != nil && after == "" {
		return loomharness.MessagePage{Events: p.w.page1, Next: "p2"}, nil
	}
	if p.w.page2 != nil && after == "p2" {
		return p.w.page2()
	}
	if p.w.msgErr != nil {
		return loomharness.MessagePage{}, p.w.msgErr
	}
	page, err := p.Session.Messages(ctx, after, limit)
	page.Events = slices.DeleteFunc(page.Events, func(e loomharness.Event) bool { return e.AskID == p.w.gone })
	if page.Next == "" {
		page.Events = append(page.Events, p.w.extra...)
	}
	return page, err
}

func (p tweakedSession) Reply(ctx context.Context, askID string, r loomharness.Reply) error {
	if p.w.replies != nil {
		*p.w.replies = append(*p.w.replies, r)
	}
	if r.Always && p.w.alwaysErr != nil {
		return p.w.alwaysErr
	}
	return p.Session.Reply(ctx, askID, r)
}

// TestGetOpenAsksIntegration: approval and question asks show in Get with
// their native IDs and types while open, and the agent waits on them;
// Respond accepts only an open ask; a resolved ask disappears. After a serve
// restart the ask table is rebuilt from the native history: an ask the
// harness still has keeps its ID, and one it no longer has is reported
// ask.lost, leaves Get and refuses Respond.
func TestGetOpenAsksIntegration(t *testing.T) {
	ctx := context.Background()
	page := askPage
	askPage = 1 // the saved asks are read in many pages
	t.Cleanup(func() { askPage = page })
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s1 := e.service(ServiceConfig{})
	stop1 := startFeed(s1, e)
	alpha, _ := newLead(t, e, s1, "alpha")
	beta, _ := newLead(t, e, s1, "beta")
	fh.Script(alpha.AgentID, fake.Turn{Steps: []fake.Step{{Ask: "a1"}, {Ask: "q1", Question: true}}})
	fh.Script(beta.AgentID, fake.Turn{Steps: []fake.Step{{Ask: "b1"}}})
	mustSendMsg(t, s1, sendReq(alpha.AgentID, "u1", "go", user))
	mustSendMsg(t, s1, sendReq(beta.AgentID, "u1", "go", user))
	eventually(t, "the approvals open and alpha waits on approval", func() bool {
		a := s1.get(t, alpha.AgentID)
		return slices.Equal(askIDs(t, s1, alpha.AgentID), []string{"a1:approval"}) &&
			slices.Equal(askIDs(t, s1, beta.AgentID), []string{"b1:approval"}) &&
			s1.get(t, beta.AgentID).State == StateWaiting && a.State == StateWaiting && deref(a.WaitingOn) == "approval"
	})
	wantCode(t, s1.Respond(ctx, RespondRequest{AgentID: alpha.AgentID, AskID: "zz", Decision: "allow_once"}), CodeAskNotFound)
	if err := s1.Respond(ctx, RespondRequest{AgentID: alpha.AgentID, AskID: "a1", Decision: "allow_once"}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, s1.Respond(ctx, RespondRequest{AgentID: alpha.AgentID, AskID: "a1", Decision: "allow_once"}), CodeAskNotFound)
	eventually(t, "the question opens and alpha waits on input", func() bool {
		a := s1.get(t, alpha.AgentID)
		return slices.Equal(askIDs(t, s1, alpha.AgentID), []string{"q1:question"}) && a.State == StateWaiting && deref(a.WaitingOn) == "input"
	})

	stop1() // serve restarts; meanwhile beta's approval stopped being pending
	s2 := e.service(ServiceConfig{})
	s2.harnesses["opencode"] = tweaked{Harness: e.h, gone: "b1"}
	stop2 := startFeed(s2, e)
	defer stop2()
	eventually(t, "the ask table rebuilt", func() bool {
		return slices.Equal(askIDs(t, s2, alpha.AgentID), []string{"q1:question"}) && len(askIDs(t, s2, beta.AgentID)) == 0 &&
			s2.get(t, beta.AgentID).State == StateActive
	})
	if lost := kinds(rows(t, s2, beta.AgentID, 0), KindAskLost); len(lost) != 1 {
		t.Fatalf("beta ask.lost rows = %d; want b1 reported lost once", len(lost))
	}
	wantCode(t, s2.Respond(ctx, RespondRequest{AgentID: beta.AgentID, AskID: "b1", Decision: "allow_once"}), CodeAskNotFound)
	if err := s2.Respond(ctx, RespondRequest{AgentID: alpha.AgentID, AskID: "q1", Answer: "yes"}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "alpha's turn ends", func() bool { return s2.get(t, alpha.AgentID).State == StateIdle })
	if got := askIDs(t, s2, alpha.AgentID); len(got) != 0 {
		t.Fatalf("open asks after the answer = %v", got)
	}
	if lost := kinds(rows(t, s2, alpha.AgentID, 0), KindAskLost); len(lost) != 0 {
		t.Fatalf("alpha has %d ask.lost rows; its asks were answered", len(lost))
	}
}

// TestAskLostWhenTurnEnds: a turn that ends with an ask still open reports
// it ask.lost; Respond to it then fails.
func TestAskLostWhenTurnEnds(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	a, ref := newLead(t, e, s, "alpha")
	mustSendMsg(t, s, sendReq(a.AgentID, "u1", "go", user))
	key := defaultInputKey("", a.AgentID, "u1")
	for _, ev := range []loomharness.Event{
		{Type: loomharness.EventTurnStarted, TurnID: "T1", InputKey: key},
		{Type: loomharness.EventAskOpened, TurnID: "T1", AskID: "x1"},
		{Type: loomharness.EventTurnCompleted, TurnID: "T1", StopReason: "cancelled"},
	} {
		ev.Session = ref
		if err := s.HarnessEvent(ctx, a.AgentID, ev); err != nil {
			t.Fatal(err)
		}
	}
	if lost := kinds(rows(t, s, a.AgentID, 0), KindAskLost); len(lost) != 1 {
		t.Fatalf("ask.lost rows = %d; want x1 reported lost", len(lost))
	}
	wantCode(t, s.Respond(ctx, RespondRequest{AgentID: a.AgentID, AskID: "x1", Decision: "deny"}), CodeAskNotFound)
}

// TestSubscribeFiltersAgentsKindsDeltasAndGap: a subscription gets only its
// agents, only the kinds it asked for, live deltas only if asked, and a
// feed.gap notice; one that falls behind ends with subscriber_lagged.
func TestSubscribeFiltersAgentsKindsDeltasAndGap(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s := e.service(ServiceConfig{})
	stop := startFeed(s, e)
	defer stop()
	alpha, _ := newLead(t, e, s, "alpha")
	beta, _ := newLead(t, e, s, "beta")
	req := SubscribeRequest{AgentIDs: []string{alpha.AgentID}, Cursors: map[string]int64{alpha.AgentID: 0},
		Kinds: []string{"ask.opened"}, Deltas: true}
	withDeltas, err := s.Subscribe(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	req.Deltas = false
	noDeltas, err := s.Subscribe(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	fh.Script(alpha.AgentID, fake.Turn{Steps: []fake.Step{{Delta: "hi"}, {Ask: "a1", Gap: true}}})
	fh.Script(beta.AgentID, fake.Turn{Steps: []fake.Step{{Delta: "beta"}, {Ask: "b1"}}})
	mustSendMsg(t, s, sendReq(beta.AgentID, "u1", "go", user))
	mustSendMsg(t, s, sendReq(alpha.AgentID, "u1", "go", user))
	got := func(sub *Subscription, n int) []string {
		var out []string
		for _, r := range recv(t, sub, n) {
			if r.AgentID != "" && r.AgentID != alpha.AgentID {
				t.Fatalf("got %s of %s", r.Kind, r.AgentID)
			}
			out = append(out, r.Kind)
		}
		quiet(t, sub)
		return out
	}
	if k := got(withDeltas, 3); !slices.Equal(k, []string{KindDelta, KindFeedGap, "ask.opened"}) {
		t.Fatalf("with deltas: %v", k)
	}
	if k := got(noDeltas, 2); !slices.Equal(k, []string{KindFeedGap, "ask.opened"}) {
		t.Fatalf("without deltas: %v", k)
	}

	lagged, err := s.Subscribe(ctx, SubscribeRequest{AgentIDs: []string{alpha.AgentID}, Deltas: true})
	if err != nil {
		t.Fatal(err)
	}
	for range subscriberBuffer + 2 { // nobody reads
		s.events.Notify(loomstore.Event{AgentID: alpha.AgentID, Kind: KindDelta})
	}
	deadline := time.After(5 * time.Second)
	for open := true; open; {
		select {
		case _, open = <-lagged.C:
		case <-deadline:
			t.Fatal("a lagging subscription never ended")
		}
	}
	wantCode(t, lagged.Err(), CodeSubscriberLagged)
}

// TestListEventsAndSubscribeExpiredHistory: ListEvents pages saved events
// with a stable snapshot; purged history fails with history_expired, or
// cursor_expired for a cursor, and so does a Subscribe cursor.
func TestListEventsAndSubscribeExpiredHistory(t *testing.T) {
	ctx := context.Background()
	gone := svcAgent("gone", "persistent", StateArchived)
	gone.HistoryPurgedAt = sp(loomstore.Stamp(time.Now()))
	s := newService(t, ServiceConfig{}, svcAgent("a1", "persistent", StateIdle), gone)
	for _, id := range []string{"e1", "e2", "e3"} {
		if err := s.appendEvent(ctx, "a1", "note", id, map[string]string{"id": id}); err != nil {
			t.Fatal(err)
		}
	}
	p, err := s.ListEvents(ctx, loomstore.EventQuery{AgentID: "a1", Limit: 2})
	if err != nil || len(p.Events) != 2 || !p.More {
		t.Fatalf("page 1 = %+v, %v", p, err)
	}
	if err := s.appendEvent(ctx, "a1", "note", "e4", nil); err != nil {
		t.Fatal(err)
	}
	p, err = s.ListEvents(ctx, loomstore.EventQuery{AgentID: "a1", After: p.Next, Snapshot: p.SnapshotSeq, Limit: 2})
	if err != nil || len(p.Events) != 1 || p.Events[0].EventID != "e3" || p.More {
		t.Fatalf("page 2 = %+v, %v; want e3 only (snapshot)", p, err)
	}
	_, err = s.ListEvents(ctx, loomstore.EventQuery{AgentID: "gone"})
	wantCode(t, err, CodeHistoryExpired)
	_, err = s.ListEvents(ctx, loomstore.EventQuery{AgentID: "gone", After: 3})
	wantCode(t, err, CodeCursorExpired)
	_, err = s.Subscribe(ctx, SubscribeRequest{AgentIDs: []string{"gone"}, Cursors: map[string]int64{"gone": 3}})
	wantCode(t, err, CodeCursorExpired)
}

// TestHarnessAttentionOnlyAffectedAgents: a harness whose feed cannot open
// raises Attention harness_unavailable on its own agents only, keeps another
// Attention as it is, and clears it once the feed is back.
func TestHarnessAttentionOnlyAffectedAgents(t *testing.T) {
	retry := feedRetry
	feedRetry = 10 * time.Millisecond
	t.Cleanup(func() { feedRetry = retry })
	fa, fb := fake.New(), fake.New()
	a1, a2, a3 := svcAgent("a1", "persistent", StateIdle), svcAgent("a2", "persistent", StateIdle), svcAgent("a3", "persistent", StateIdle)
	a1.Harness, a2.Harness, a3.Harness = "fa", "fb", "fa"
	a3.AttentionReason = sp(AttentionDeliveryUnknown)
	s := newService(t, ServiceConfig{Harnesses: map[string]loomharness.Harness{"fa": fa, "fb": fb}}, a1, a2, a3)
	fa.Crash()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.RunFeed(ctx, "fa") }()
	defer func() { cancel(); <-done }()
	eventually(t, "a1 shows harness_unavailable", func() bool {
		return deref(s.get(t, "a1").AttentionReason) == AttentionHarnessUnavailable
	})
	if r := s.get(t, "a2").AttentionReason; r != nil {
		t.Fatalf("a2 on another harness got Attention %s", *r)
	}
	if r := deref(s.get(t, "a3").AttentionReason); r != AttentionDeliveryUnknown {
		t.Fatalf("a3's Attention = %s; want delivery_unknown kept", r)
	}
	if err := fa.Restart(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a1's Attention cleared", func() bool { return s.get(t, "a1").AttentionReason == nil })
	if r := deref(s.get(t, "a3").AttentionReason); r != AttentionDeliveryUnknown {
		t.Fatalf("a3's Attention = %s after recovery; want delivery_unknown kept", r)
	}
}

// TestFailedBackfillKeepsOpenAsks: a backfill whose history read fails
// leaves the open asks as they are: Get still lists them, no ask.lost is
// saved, and Respond still answers them.
func TestFailedBackfillKeepsOpenAsks(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s := e.service(ServiceConfig{})
	stop := startFeed(s, e)
	a, _ := newLead(t, e, s, "alpha")
	fh.Script(a.AgentID, fake.Turn{Steps: []fake.Step{{Ask: "a1"}}})
	mustSendMsg(t, s, sendReq(a.AgentID, "u1", "go", user))
	eventually(t, "a1 opens", func() bool {
		return slices.Equal(askIDs(t, s, a.AgentID), []string{"a1:approval"}) && s.get(t, a.AgentID).State == StateWaiting
	})
	stop()
	var reads atomic.Int32
	s.harnesses["opencode"] = tweaked{Harness: e.h, msgErr: errors.New("history read failed"), reads: &reads}
	stop = startFeed(s, e)
	eventually(t, "two failed backfills", func() bool { return reads.Load() >= 2 })
	stop()
	if got := askIDs(t, s, a.AgentID); !slices.Equal(got, []string{"a1:approval"}) {
		t.Fatalf("open asks after a failed backfill = %v", got)
	}
	if lost := kinds(rows(t, s, a.AgentID, 0), KindAskLost); len(lost) != 0 {
		t.Fatalf("a failed backfill saved %d ask.lost", len(lost))
	}
	if ag := s.get(t, a.AgentID); ag.State != StateWaiting {
		t.Fatalf("state %s; want still waiting", ag.State)
	}
	s.harnesses["opencode"] = e.h
	if err := s.Respond(ctx, RespondRequest{AgentID: a.AgentID, AskID: "a1", Decision: "allow_once"}); err != nil {
		t.Fatal(err)
	}
}

// TestRespondAlwaysNotNarrowed: allow_always reaches the harness as Always;
// a harness that cannot keep it fails, Respond returns that failure and the
// ask stays open for another answer.
func TestRespondAlwaysNotNarrowed(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s := e.service(ServiceConfig{})
	stop := startFeed(s, e)
	a, _ := newLead(t, e, s, "alpha")
	fh.Script(a.AgentID, fake.Turn{Steps: []fake.Step{{Ask: "a1"}}})
	mustSendMsg(t, s, sendReq(a.AgentID, "u1", "go", user))
	eventually(t, "a1 opens", func() bool {
		return slices.Equal(askIDs(t, s, a.AgentID), []string{"a1:approval"}) && s.get(t, a.AgentID).State == StateWaiting
	})
	stop()
	var replies []loomharness.Reply
	s.harnesses["opencode"] = tweaked{Harness: e.h, alwaysErr: loomharness.ErrUnavailable, replies: &replies}
	wantCode(t, s.Respond(ctx, RespondRequest{AgentID: a.AgentID, AskID: "a1", Decision: "allow_always"}), CodeHarnessUnavailable)
	if len(replies) != 1 || !replies[0].Allow || !replies[0].Always {
		t.Fatalf("replies = %+v; want one Allow+Always", replies)
	}
	if got := askIDs(t, s, a.AgentID); !slices.Equal(got, []string{"a1:approval"}) {
		t.Fatalf("open asks after a failed reply = %v", got)
	}
	if err := s.Respond(ctx, RespondRequest{AgentID: a.AgentID, AskID: "a1", Decision: "allow_once"}); err != nil {
		t.Fatal(err)
	}
	if len(replies) != 2 || replies[1].Always {
		t.Fatalf("second reply = %+v; want once", replies)
	}
}

// TestProbePartialBackfillKeepsOpenAsk: a replay whose first page resolves
// open ask a1 and saves a new item, and whose second page then fails to
// read or to save, saves, publishes and changes nothing: no row is added, a
// subscriber gets nothing, and a1 is still open.
func TestProbePartialBackfillKeepsOpenAsk(t *testing.T) {
	for _, fail := range []string{"read", "write"} {
		e := newCreateEnv(t)
		fh := e.h.Harness.(*fake.Harness)
		s := e.service(ServiceConfig{})
		stop := startFeed(s, e)
		a, ref := newLead(t, e, s, "alpha")
		fh.Script(a.AgentID, fake.Turn{Steps: []fake.Step{{Ask: "a1"}}})
		mustSendMsg(t, s, sendReq(a.AgentID, "u1", "go", user))
		eventually(t, "a1 opens", func() bool {
			return slices.Equal(askIDs(t, s, a.AgentID), []string{"a1:approval"}) && s.get(t, a.AgentID).State == StateWaiting
		})
		stop()
		ctx, cancel := context.WithCancel(context.Background())
		turn := deref(s.get(t, a.AgentID).RunningTurnID)
		item := func(id string) loomharness.Event {
			return loomharness.Event{Type: loomharness.EventItemCompleted, Session: ref, TurnID: turn, ItemID: id, ItemKind: "message"}
		}
		w := tweaked{Harness: e.h, page1: []loomharness.Event{item("new1"),
			{Type: loomharness.EventAskResolved, Session: ref, TurnID: turn, AskID: "a1"}}}
		if fail == "read" {
			w.msgErr = errors.New("page 2 failed")
		} else {
			w.page2 = func() (loomharness.MessagePage, error) {
				return loomharness.MessagePage{Events: []loomharness.Event{item("new2")}}, nil
			}
			failInsert(t, e, ":new2") // the batch's third write fails, after new1 and the resolve
		}
		s.harnesses["opencode"] = w
		sub, err := s.Subscribe(context.Background(), SubscribeRequest{AgentIDs: []string{a.AgentID}})
		if err != nil {
			t.Fatal(err)
		}
		before := len(rows(t, s, a.AgentID, 0))
		if err := s.replay(ctx, "opencode", s.get(t, a.AgentID)); err == nil ||
			(fail == "write") != strings.Contains(err.Error(), "injected write failure") {
			t.Fatalf("%s: replay = %v", fail, err)
		}
		cancel()
		if n := len(rows(t, s, a.AgentID, 0)); n != before {
			t.Fatalf("%s: a failed replay saved %d rows", fail, n-before)
		}
		quiet(t, sub)
		if got := askIDs(t, s, a.AgentID); !slices.Equal(got, []string{"a1:approval"}) {
			t.Fatalf("%s: open asks after a failed replay = %v", fail, got)
		}
		if ag := s.get(t, a.AgentID); ag.State != StateWaiting {
			t.Fatalf("%s: state %s; want still waiting", fail, ag.State)
		}
	}
}

// TestAppendAllAllOrNothing: a batch whose write fails after an earlier
// row of it was written saves and publishes none of it; a batch that
// commits publishes each new event once, in order, and not one it already
// had.
func TestAppendAllAllOrNothing(t *testing.T) {
	ctx := context.Background()
	s := newService(t, ServiceConfig{}, svcAgent("a1", "persistent", StateIdle))
	if err := s.appendEvent(ctx, "a1", "note", "old", nil); err != nil {
		t.Fatal(err)
	}
	sub, err := s.events.Subscribe(ctx, map[string]int64{"a1": LiveOnly})
	if err != nil {
		t.Fatal(err)
	}
	row := func(agent, id string) loomstore.Event {
		return loomstore.Event{AgentID: agent, EventID: id, Kind: "note", Payload: json.RawMessage(`{}`)}
	}
	err = s.events.AppendAll(ctx, "a1", []loomstore.Event{row("a1", "x1"), row("other", "x2")})
	if err == nil || len(rows(t, s, "a1", 0)) != 1 {
		t.Fatalf("failed batch: err %v, rows %d; want an error and only the old row", err, len(rows(t, s, "a1", 0)))
	}
	quiet(t, sub)
	if err := s.events.AppendAll(ctx, "a1", []loomstore.Event{row("a1", "old"), row("a1", "y1"), row("a1", "y2")}); err != nil {
		t.Fatal(err)
	}
	if got := ids(recv(t, sub, 2)); !slices.Equal(got, []string{"y1", "y2"}) {
		t.Fatalf("published %v; want y1, y2", got)
	}
	quiet(t, sub)
}

// TestReplayedTurnEndLosesItsAsk: a replay whose history ends the turn of
// open ask a1 reports a1 lost once and leaves it closed; if the history
// resolves a1 first, the turn end reports nothing lost.
func TestReplayedTurnEndLosesItsAsk(t *testing.T) {
	for _, resolved := range []bool{false, true} {
		e := newCreateEnv(t)
		fh := e.h.Harness.(*fake.Harness)
		s := e.service(ServiceConfig{})
		stop := startFeed(s, e)
		a, ref := newLead(t, e, s, "alpha")
		fh.Script(a.AgentID, fake.Turn{Steps: []fake.Step{{Ask: "a1"}}})
		mustSendMsg(t, s, sendReq(a.AgentID, "u1", "go", user))
		eventually(t, "a1 opens", func() bool {
			return slices.Equal(askIDs(t, s, a.AgentID), []string{"a1:approval"}) && s.get(t, a.AgentID).State == StateWaiting
		})
		stop()
		turn := deref(s.get(t, a.AgentID).RunningTurnID)
		extra := []loomharness.Event{{Type: loomharness.EventTurnCompleted, Session: ref, TurnID: turn, StopReason: "cancelled"}}
		if resolved {
			extra = append([]loomharness.Event{{Type: loomharness.EventAskResolved, Session: ref, TurnID: turn, AskID: "a1"}}, extra...)
		}
		s.harnesses["opencode"] = tweaked{Harness: e.h, extra: extra}
		stop = startFeed(s, e)
		eventually(t, "the turn ends and a1 closes", func() bool {
			return s.get(t, a.AgentID).State == StateIdle && len(askIDs(t, s, a.AgentID)) == 0
		})
		stop()
		want := 1
		if resolved {
			want = 0
		}
		if lost := kinds(rows(t, s, a.AgentID, 0), KindAskLost); len(lost) != want {
			t.Fatalf("resolved %v: ask.lost rows = %d; want %d", resolved, len(lost), want)
		}
	}
}

// TestReplayAppliesMissedEvents: with no feed running, a replay applies
// the delivery, turn start and ask the live feed missed: the message is
// delivered, the turn is named and the agent waits on the open ask.
func TestReplayAppliesMissedEvents(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s := e.service(ServiceConfig{})
	a, _ := newLead(t, e, s, "alpha")
	fh.Script(a.AgentID, fake.Turn{Steps: []fake.Step{{Ask: "a1"}}})
	mustSendMsg(t, s, sendReq(a.AgentID, "u1", "go", user))
	key := deref(s.get(t, a.AgentID).RunningTurnID)
	if err := s.replay(ctx, "opencode", s.get(t, a.AgentID)); err != nil {
		t.Fatal(err)
	}
	ag := s.get(t, a.AgentID)
	if ag.RunningTurnID == nil || *ag.RunningTurnID == key || ag.State != StateWaiting {
		t.Fatalf("turn %v state %s; want the named turn, waiting", ag.RunningTurnID, ag.State)
	}
	slots, err := s.store.Slots(ctx, a.AgentID)
	if err != nil || len(slots) != 1 || slots[0].State != loomstore.SlotDelivered {
		t.Fatalf("slots = %+v, %v; want the message delivered", slots, err)
	}
	if got := askIDs(t, s, a.AgentID); !slices.Equal(got, []string{"a1:approval"}) {
		t.Fatalf("open asks = %v", got)
	}
}

// TestReplayRetryConverges: after a replay fails (page 2 does not read, or
// its write fails) or crashes between its commit and its apply, a retry in
// the same process or after a restart ends exactly as an uninterrupted
// replay does: the same new rows, once each, in order, the same open asks
// and the same state.
func TestReplayRetryConverges(t *testing.T) {
	type outcome struct {
		kinds []string
		asks  []string
		state string
	}
	run := func(mode string) outcome {
		e := newCreateEnv(t)
		fh := e.h.Harness.(*fake.Harness)
		s := e.service(ServiceConfig{})
		stop := startFeed(s, e)
		a, ref := newLead(t, e, s, "alpha")
		fh.Script(a.AgentID, fake.Turn{Steps: []fake.Step{{Ask: "a1"}}})
		mustSendMsg(t, s, sendReq(a.AgentID, "u1", "go", user))
		eventually(t, "a1 opens", func() bool {
			return slices.Equal(askIDs(t, s, a.AgentID), []string{"a1:approval"}) && s.get(t, a.AgentID).State == StateWaiting
		})
		stop()
		turn := deref(s.get(t, a.AgentID).RunningTurnID)
		before := len(rows(t, s, a.AgentID, 0))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		item := func(id string) loomharness.Event {
			return loomharness.Event{Type: loomharness.EventItemCompleted, Session: ref, TurnID: turn, ItemID: id, ItemKind: "message"}
		}
		page2 := []loomharness.Event{item("new2"), {Type: loomharness.EventTurnCompleted, Session: ref, TurnID: turn, StopReason: "end_turn"}}
		w := tweaked{Harness: e.h, page1: []loomharness.Event{item("new1"),
			{Type: loomharness.EventAskResolved, Session: ref, TurnID: turn, AskID: "a1"}}}
		good := w
		good.page2 = func() (loomharness.MessagePage, error) { return loomharness.MessagePage{Events: page2}, nil }
		switch mode {
		case "read", "read+restart":
			w.msgErr = errors.New("page 2 failed")
		case "write":
			w = good
			failInsert(t, e, ":new2") // dropped before the retry
		case "crash+restart":
			w = good
			appendAllCrash = func() { panic("crash") }
			t.Cleanup(func() { appendAllCrash = func() {} })
		}
		if mode != "clean" {
			s.harnesses["opencode"] = w
			func() {
				defer func() { _ = recover() }()
				if err := s.replay(ctx, "opencode", s.get(t, a.AgentID)); err == nil {
					t.Fatalf("%s: the first replay succeeded", mode)
				}
			}()
			appendAllCrash = func() {}
			if mode == "write" {
				failInsert(t, e, "")
			}
			if n := len(rows(t, s, a.AgentID, 0)) - before; mode != "crash+restart" && n != 0 {
				t.Fatalf("%s: the failed replay saved %d rows", mode, n)
			}
			if strings.HasSuffix(mode, "+restart") {
				s = e.service(ServiceConfig{})
			}
		}
		s.harnesses["opencode"] = good
		if err := s.replay(context.Background(), "opencode", s.get(t, a.AgentID)); err != nil {
			t.Fatalf("%s: retry: %v", mode, err)
		}
		var out outcome
		for _, r := range rows(t, s, a.AgentID, 0)[before:] {
			out.kinds = append(out.kinds, r.Kind)
		}
		out.asks, out.state = askIDs(t, s, a.AgentID), s.get(t, a.AgentID).State
		return out
	}
	want := run("clean")
	t.Logf("clean: %+v", want)
	if want.state != StateIdle || len(want.asks) != 0 || !slices.Contains(want.kinds, string(loomharness.EventAskResolved)) ||
		slices.Contains(want.kinds, KindAskLost) {
		t.Fatalf("clean replay = %+v; want a1 resolved, no ask.lost, idle", want)
	}
	for _, mode := range []string{"read", "write", "read+restart", "crash+restart"} {
		if got := run(mode); !slices.Equal(got.kinds, want.kinds) || !slices.Equal(got.asks, want.asks) || got.state != want.state {
			t.Fatalf("%s: %+v; want %+v", mode, got, want)
		}
	}
}

// TestReplayReadHoldsNoLock: while alpha's history read is blocked, another
// agent's events are still saved and published, and a direct store write
// for it succeeds promptly: the read holds no lock and no transaction.
func TestReplayReadHoldsNoLock(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	alpha, _ := newLead(t, e, s, "alpha")
	beta, _ := newLead(t, e, s, "beta")
	var reads atomic.Int32
	block := make(chan struct{})
	s.harnesses["opencode"] = tweaked{Harness: e.h, reads: &reads, block: block}
	done := make(chan error, 1)
	go func() { done <- s.replay(ctx, "opencode", s.get(t, alpha.AgentID)) }()
	eventually(t, "alpha's history read starts", func() bool { return reads.Load() == 1 })
	sub, err := s.Subscribe(ctx, SubscribeRequest{AgentIDs: []string{beta.AgentID}})
	if err != nil {
		t.Fatal(err)
	}
	saved := make(chan error, 1)
	go func() { saved <- s.appendEvent(ctx, beta.AgentID, "note", "n1", nil) }()
	select {
	case err := <-saved:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("beta's event waited on alpha's history read")
	}
	if got := recv(t, sub, 1); got[0].EventID != "n1" {
		t.Fatalf("published %s; want n1", got[0].EventID)
	}
	direct, cancel := context.WithTimeout(ctx, 2*time.Second) // under busy_timeout, so a held write lock fails it
	defer cancel()
	if _, err := s.store.AppendEvent(direct, loomstore.Event{AgentID: beta.AgentID, EventID: "n2", Kind: "note",
		Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatalf("beta's direct store write during alpha's history read: %v", err)
	}
	close(block)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// TestReplayOverCapFailsClosed: a history over the replay cap saves and
// publishes nothing, leaves the asks as they are and shows Attention
// history_too_large without stopping the other agents' backfill; with room,
// the next backfill replays it and clears the Attention.
func TestReplayOverCapFailsClosed(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s := e.service(ServiceConfig{})
	stop := startFeed(s, e)
	a, ref := newLead(t, e, s, "alpha")
	b, _ := newLead(t, e, s, "beta")
	fh.Script(a.AgentID, fake.Turn{Steps: []fake.Step{{Ask: "a1"}}})
	mustSendMsg(t, s, sendReq(a.AgentID, "u1", "go", user))
	eventually(t, "a1 opens", func() bool {
		return slices.Equal(askIDs(t, s, a.AgentID), []string{"a1:approval"}) && s.get(t, a.AgentID).State == StateWaiting
	})
	stop()
	turn := deref(s.get(t, a.AgentID).RunningTurnID)
	big := strings.Repeat("x", 4096)
	s.harnesses["opencode"] = tweaked{Harness: e.h, extra: []loomharness.Event{
		{Type: loomharness.EventItemCompleted, Session: ref, TurnID: turn, ItemID: "big", ItemKind: "message", Text: big},
		{Type: loomharness.EventAskResolved, Session: ref, TurnID: turn, AskID: "a1"}}}
	capped := replayCap
	replayCap = 4096
	t.Cleanup(func() { replayCap = capped })
	sub, err := s.Subscribe(ctx, SubscribeRequest{AgentIDs: []string{a.AgentID}})
	if err != nil {
		t.Fatal(err)
	}
	before := len(rows(t, s, a.AgentID, 0))
	err = s.replay(ctx, "opencode", s.get(t, a.AgentID))
	if !errors.Is(err, errHistoryTooLarge) || !strings.Contains(err.Error(), "4096 bytes") {
		t.Fatalf("replay = %v; want the cap named", err)
	}
	if n := len(rows(t, s, a.AgentID, 0)); n != before {
		t.Fatalf("an over-cap replay saved %d rows", n-before)
	}
	quiet(t, sub)
	if err := s.backfill(ctx, "opencode"); err != nil {
		t.Fatalf("backfill = %v; want the over-cap agent skipped", err)
	}
	if r := deref(s.get(t, a.AgentID).AttentionReason); r != AttentionHistoryTooLarge {
		t.Fatalf("alpha Attention = %q; want history_too_large", r)
	}
	if r := s.get(t, b.AgentID).AttentionReason; r != nil {
		t.Fatalf("beta Attention = %q", *r)
	}
	if got := askIDs(t, s, a.AgentID); !slices.Equal(got, []string{"a1:approval"}) {
		t.Fatalf("open asks after an over-cap replay = %v", got)
	}
	replayCap = capped
	if err := s.backfill(ctx, "opencode"); err != nil {
		t.Fatal(err)
	}
	if ag := s.get(t, a.AgentID); ag.AttentionReason != nil || len(askIDs(t, s, a.AgentID)) != 0 {
		t.Fatalf("after a replay with room: Attention %v, asks %v; want cleared, a1 resolved", ag.AttentionReason, askIDs(t, s, a.AgentID))
	}
}

// failInsert makes e's store abort the insert of any event whose EventID
// ends in suffix, as a failed write; "" removes that. It returns the undo.
func failInsert(t *testing.T, e *createEnv, suffix string) func() {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+e.path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	stmt := `DROP TRIGGER IF EXISTS fail_insert`
	if suffix != "" {
		stmt = `CREATE TRIGGER fail_insert BEFORE INSERT ON agent_events WHEN NEW.event_id LIKE '%` + suffix +
			`' BEGIN SELECT RAISE(ABORT, 'injected write failure'); END`
	}
	if _, err := db.ExecContext(context.Background(), stmt); err != nil {
		t.Fatal(err)
	}
	return func() { failInsert(t, e, "") }
}
