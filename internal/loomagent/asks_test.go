package loomagent

import (
	"context"
	"slices"
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

// pendingOnly serves a native history without the given asks, as OpenCode's
// history lists only asks still pending.
type pendingOnly struct {
	loomharness.Harness
	gone string
}

func (p pendingOnly) Session(ref loomharness.NativeRef) loomharness.Session {
	return pendingSession{p.Harness.Session(ref), p.gone}
}

type pendingSession struct {
	loomharness.Session
	gone string
}

func (p pendingSession) Messages(ctx context.Context, after string, limit int) (loomharness.MessagePage, error) {
	page, err := p.Session.Messages(ctx, after, limit)
	page.Events = slices.DeleteFunc(page.Events, func(e loomharness.Event) bool { return e.AskID == p.gone })
	return page, err
}

// TestGetOpenAsksIntegration: approval and question asks show in Get with
// their native IDs and types while open, and the agent waits on them;
// Respond accepts only an open ask; a resolved ask disappears. After a serve
// restart the ask table is rebuilt from the native history: an ask the
// harness still has keeps its ID, and one it no longer has is reported
// ask.lost, leaves Get and refuses Respond.
func TestGetOpenAsksIntegration(t *testing.T) {
	ctx := context.Background()
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
	eventually(t, "the approvals open", func() bool {
		return slices.Equal(askIDs(t, s1, alpha.AgentID), []string{"a1:approval"}) &&
			slices.Equal(askIDs(t, s1, beta.AgentID), []string{"b1:approval"})
	})
	if a := s1.get(t, alpha.AgentID); a.State != StateWaiting || deref(a.WaitingOn) != "approval" {
		t.Fatalf("alpha %s on %q; want waiting on approval", a.State, deref(a.WaitingOn))
	}
	wantCode(t, s1.Respond(ctx, RespondRequest{AgentID: alpha.AgentID, AskID: "zz", Decision: "allow_once"}), CodeAskNotFound)
	if err := s1.Respond(ctx, RespondRequest{AgentID: alpha.AgentID, AskID: "a1", Decision: "allow_once"}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, s1.Respond(ctx, RespondRequest{AgentID: alpha.AgentID, AskID: "a1", Decision: "allow_once"}), CodeAskNotFound)
	eventually(t, "the question opens", func() bool { return slices.Equal(askIDs(t, s1, alpha.AgentID), []string{"q1:question"}) })
	if a := s1.get(t, alpha.AgentID); a.State != StateWaiting || deref(a.WaitingOn) != "input" {
		t.Fatalf("alpha %s on %q; want waiting on input", a.State, deref(a.WaitingOn))
	}

	stop1() // serve restarts; meanwhile beta's approval stopped being pending
	s2 := e.service(ServiceConfig{})
	s2.harnesses["opencode"] = pendingOnly{e.h, "b1"}
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
