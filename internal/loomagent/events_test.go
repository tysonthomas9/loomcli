package loomagent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// rows lists an agent's saved events in seq order.
func rows(t *testing.T, s *Service, agentID string, after int64) []loomstore.Event {
	t.Helper()
	var out []loomstore.Event
	q := loomstore.EventQuery{AgentID: agentID, After: after}
	for {
		p, err := s.store.ListEvents(context.Background(), q)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, p.Events...)
		if !p.More {
			return out
		}
		q.After, q.Snapshot = p.Next, p.SnapshotSeq
	}
}

func kinds(es []loomstore.Event, kind string) []loomstore.Event {
	var out []loomstore.Event
	for _, e := range es {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// TestOpenCodeWatchEventsPersistBeforePublish: every event the Bus publishes
// (agent.state_changed, agent.idle, agent.settled, message.waiting, ...) is
// already a saved row when a subscriber receives it; agent.turn_completed is
// saved from the native turn end before agent.idle; each is saved once, a
// repeat backfill adds nothing, and the events before the first turn page.
func TestOpenCodeWatchEventsPersistBeforePublish(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s := e.service(ServiceConfig{})
	bus := s.Bus.Subscribe()
	defer s.Bus.Unsubscribe(bus)
	var mu sync.Mutex
	var problems []string
	seen := map[string]int{}
	checked := make(chan struct{})
	go func() {
		defer close(checked)
		for ev := range bus.C {
			mu.Lock()
			seen[ev.Type]++
			saved := rows(t, s, ev.AgentID, 0)
			if a, _ := s.store.GetAgent(ctx, ev.AgentID); a.HistoryPurgedAt != nil {
				mu.Unlock()
				continue // Delete purged the history: later events are live only
			}
			if !slices.ContainsFunc(saved, func(r loomstore.Event) bool { return r.EventID == ev.EventID && r.Kind == ev.Type }) {
				problems = append(problems, ev.Type+" published before it was saved")
			}
			if ev.Type == EventIdle && !slices.ContainsFunc(kinds(saved, EventTurnCompleted), func(r loomstore.Event) bool { return r.TurnID == ev.TurnID }) {
				problems = append(problems, "agent.idle published before its agent.turn_completed was saved")
			}
			mu.Unlock()
		}
	}()
	stop := runFeed(t, s, "opencode")
	defer stop()
	a, ref := newLead(t, e, s, "alpha")
	fh.Script(a.AgentID, fake.Turn{Steps: []fake.Step{{Delta: "hi"}, {Ask: "t1"}}})
	mustSendMsg(t, s, sendReq(a.AgentID, "u1", "first", user))
	drained(t, s, "the ask saved", func() bool { return len(kinds(rows(t, s, a.AgentID, 0), "ask.opened")) == 1 })
	if err := fh.Session(ref).Reply(ctx, "t1", loomharness.Reply{Allow: true}); err != nil {
		t.Fatal(err)
	}
	drained(t, s, "idle", func() bool { return len(kinds(rows(t, s, a.AgentID, 0), EventIdle)) == 1 })
	idleRows := len(rows(t, s, a.AgentID, 0))
	if _, err := s.backfill(ctx, "opencode"); err != nil { // a repeat of the whole native history
		t.Fatal(err)
	}
	if again := len(rows(t, s, a.AgentID, 0)); again != idleRows {
		t.Fatalf("a repeat backfill added %d rows", again-idleRows)
	}
	if err := s.Archive(ctx, ArchiveRequest{AgentID: a.AgentID, Reason: ArchiveDone}); err != nil {
		t.Fatal(err)
	}
	n := len(rows(t, s, a.AgentID, 0))
	same := Event{AgentID: a.AgentID, Type: EventWaiting, Reason: "user:u", Time: time.Unix(1, 0)}
	for range 2 { // the same event saved again is one row
		if err := s.emit(ctx, same); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(rows(t, s, a.AgentID, 0)); got != n+1 {
		t.Fatalf("one event emitted twice saved %d rows", got-n)
	}
	saved := rows(t, s, a.AgentID, 0)
	if err := s.Delete(ctx, DeleteRequest{AgentID: a.AgentID}); err != nil {
		t.Fatal(err)
	}
	if left := rows(t, s, a.AgentID, 0); len(left) != 0 {
		t.Fatalf("Delete left %d saved events", len(left))
	}
	stop()
	s.Bus.Unsubscribe(bus)
	<-checked
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	for _, k := range []string{EventStateChanged, EventIdle, EventSettled, EventWaiting, EventDeleted} {
		if seen[k] == 0 {
			t.Fatalf("no %s published", k)
		}
	}
	tc := kinds(saved, EventTurnCompleted)
	if len(tc) != 1 || len(kinds(saved, EventIdle)) != 1 || tc[0].TurnID == "" {
		t.Fatalf("turn_completed %v idle %d; want one each", tc, len(kinds(saved, EventIdle)))
	}
	var idle Event
	if err := json.Unmarshal(kinds(saved, EventIdle)[0].Payload, &idle); err != nil || idle.TurnID != tc[0].TurnID {
		t.Fatalf("agent.idle %+v; want turn %s", idle, tc[0].TurnID)
	}
	if saved[0].Kind != EventStateChanged || saved[0].TurnID != "" {
		t.Fatalf("first row %s turn %q; want the pre-turn state change", saved[0].Kind, saved[0].TurnID)
	}
}

// TestOpenCodeWatchReplayAfterRestart: a watcher that reloads resubscribes
// from its last per-agent cursor; a feed.gap is filled from the native
// history; events from while serve was down are backfilled after the
// restart (and end the turn). The resubscribed watcher gets every row after
// its cursor once, in seq order, and then joins live delivery with no gap.
func TestOpenCodeWatchReplayAfterRestart(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s1 := e.service(ServiceConfig{})
	stop1 := runFeed(t, s1, "opencode")
	a, ref := newLead(t, e, s1, "alpha")
	fh.Script(a.AgentID, fake.Turn{Steps: []fake.Step{{Delta: "one"}}}, fake.Turn{Steps: []fake.Step{{Ask: "t2", Gap: true}}})

	sub1, err := s1.events.Subscribe(ctx, map[string]int64{a.AgentID: 0})
	if err != nil {
		t.Fatal(err)
	}
	mustSendMsg(t, s1, sendReq(a.AgentID, "u1", "first", user))
	var got []loomstore.Event
	for !slices.ContainsFunc(got, func(r loomstore.Event) bool { return r.Kind == EventIdle }) {
		got = append(got, recv(t, sub1, 1)...)
	}
	cursor := got[len(got)-1].Seq // the watcher reloads here

	mustSendMsg(t, s1, sendReq(a.AgentID, "u2", "second", user)) // its ask misses the live feed
	drained(t, s1, "the gap backfilled", func() bool { return len(kinds(rows(t, s1, a.AgentID, 0), "ask.opened")) == 1 })
	stop1() // serve crashes
	if err := fh.Session(ref).Reply(ctx, "t2", loomharness.Reply{Allow: true}); err != nil {
		t.Fatal(err) // the turn ends while Loom is down
	}

	s2 := e.service(ServiceConfig{}) // restart
	sub2, err := s2.events.Subscribe(ctx, map[string]int64{a.AgentID: cursor})
	if err != nil {
		t.Fatal(err)
	}
	stop2 := runFeed(t, s2, "opencode")
	defer stop2()
	drained(t, s2, "the missed turn end backfilled", func() bool { return s2.get(t, a.AgentID).State == StateIdle })
	mustSendMsg(t, s2, sendReq(a.AgentID, "u3", "third", user)) // live after the restart
	drained(t, s2, "turn 3 idle", func() bool { return len(kinds(rows(t, s2, a.AgentID, cursor), EventIdle)) == 2 })

	want := rows(t, s2, a.AgentID, cursor)
	replayed := recv(t, sub2, len(want))
	quiet(t, sub2)
	for i, r := range replayed {
		if r.Seq != cursor+int64(i)+1 || r.EventID != want[i].EventID {
			t.Fatalf("row %d: seq %d %s; want seq %d %s", i, r.Seq, r.EventID, cursor+int64(i)+1, want[i].EventID)
		}
	}
	if n := len(kinds(replayed, EventTurnCompleted)); n != 2 {
		t.Fatalf("turn_completed after the cursor = %d; want turns 2 and 3", n)
	}
	if len(kinds(replayed, "ask.opened")) != 1 || len(kinds(replayed, "ask.resolved")) != 1 {
		t.Fatal("the gap's ask.opened or the down-time ask.resolved is missing")
	}
	before := len(rows(t, s2, a.AgentID, 0))
	_, _ = s2.backfill(ctx, "opencode")
	if after := len(rows(t, s2, a.AgentID, 0)); after != before {
		t.Fatalf("a repeat backfill added %d rows", after-before)
	}
}

// TestOpenCodeEventsNativeIDs: a row's EventID uses only ids the live feed
// and a catch-up read share: an item seen live (with its TurnID) and in
// history (without one) is one row; two usage events of one turn are two
// rows, whether they carry their step's ItemID or only a native Seq; two
// resumes of one turn are two rows, each the same live and in history.
func TestOpenCodeEventsNativeIDs(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	a, ref := newLead(t, e, s, "alpha")
	before := rows(t, s, a.AgentID, 0)
	n := before[len(before)-1].Seq
	for _, ev := range []loomharness.Event{
		{Type: loomharness.EventItemCompleted, Session: ref, TurnID: "T1", ItemID: "i1", Text: "done"}, // live
		{Type: loomharness.EventItemCompleted, Session: ref, ItemID: "i1", Text: "done"},               // history
		{Type: loomharness.EventUsage, Session: ref, TurnID: "T1", ItemID: "step1"},
		{Type: loomharness.EventUsage, Session: ref, TurnID: "T1", ItemID: "step2"},
		{Type: loomharness.EventUsage, Session: ref, TurnID: "T1", Seq: 7},
		{Type: loomharness.EventUsage, Session: ref, TurnID: "T1", Seq: 8},
		{Type: loomharness.EventTurnResumed, Session: ref, TurnID: "T1", ItemID: "resume1"},
		{Type: loomharness.EventTurnResumed, Session: ref, TurnID: "T1", ItemID: "resume2"},
		{Type: loomharness.EventTurnResumed, Session: ref, TurnID: "T1", ItemID: "resume2", Seq: 9}, // resume2 from history
	} {
		if _, err := s.ingest(ctx, "opencode", ev); err != nil {
			t.Fatal(err)
		}
	}
	got := rows(t, s, a.AgentID, n)
	if len(kinds(got, "item.completed")) != 1 || len(kinds(got, "usage")) != 4 || len(kinds(got, "turn.resumed")) != 2 {
		t.Fatalf("rows %v; want one item, four usage and two resumes", ids(got))
	}
}

// flaky fails a session's history read once, after the history holds an
// ask.opened, as a backfill on feed.gap would meet it.
type flaky struct {
	loomharness.Harness
	fails *atomic.Int32
}

func (f flaky) Session(ref loomharness.NativeRef) loomharness.Session {
	return flakySession{f.Harness.Session(ref), f.fails}
}

type flakySession struct {
	loomharness.Session
	fails *atomic.Int32
}

func (f flakySession) Messages(ctx context.Context, after string, limit int) (loomharness.MessagePage, error) {
	p, err := f.Session.Messages(ctx, after, limit)
	asked := slices.ContainsFunc(p.Events, func(e loomharness.Event) bool { return e.Type == loomharness.EventAskOpened })
	if err == nil && asked && f.fails.Add(-1) >= 0 {
		return loomharness.MessagePage{}, errors.New("history read failed")
	}
	return p, err
}

// TestOpenCodeEventsBackfillRetriesFailedRead: a backfill that fails on
// feed.gap is not skipped: the feed is reopened and the next backfill saves
// the missed event.
func TestOpenCodeEventsBackfillRetriesFailedRead(t *testing.T) {
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s := e.service(ServiceConfig{})
	var fails atomic.Int32
	fails.Store(1)
	s.harnesses["opencode"] = flaky{e.h, &fails}
	clk := useTestClock(s)
	runFeed(t, s, "opencode")
	a, _ := newLead(t, e, s, "alpha")
	fh.Script(a.AgentID, fake.Turn{Steps: []fake.Step{{Ask: "t1", Gap: true}}})
	mustSendMsg(t, s, sendReq(a.AgentID, "u1", "first", user))
	settled(t, s) // the gap's backfill failed; the feed backs off
	if clk.fire() != 1 {
		t.Fatal("the feed did not back off after the failed backfill")
	}
	drained(t, s, "the missed ask saved after the failed read", func() bool {
		return len(kinds(rows(t, s, a.AgentID, 0), "ask.opened")) == 1
	})
	if fails.Load() >= 0 {
		t.Fatal("the history read never failed")
	}
}

// closedFeeds is a harness whose every feed is already closed.
type closedFeeds struct {
	loomharness.Harness
	opened *atomic.Int32
	first  loomharness.EventType // each feed sends one such event, of no owned session, before it closes
}

func (c closedFeeds) Feed(context.Context) (loomharness.Feed, error) {
	c.opened.Add(1)
	ch := make(chan loomharness.Event, 1)
	if c.first != "" {
		ch <- loomharness.Event{Type: c.first, Session: loomharness.NativeRef{Root: "/x", NativeID: "unowned"}, ItemID: "i"}
	}
	close(ch)
	return closedFeed(ch), nil
}

type closedFeed chan loomharness.Event

func (f closedFeed) Events() <-chan loomharness.Event { return f }
func (closedFeed) Close() error                       { return nil }

// TestOpenCodeEventsBackfillBacksOffOnClosedFeed: a feed that keeps closing,
// with nothing, a feed.gap or an unowned session's event first, is reopened (and history backfilled)
// with a doubling backoff up to feedRetryMax, not at a fixed rate: only a
// live native event resets it. RunFeed still ends at once on ctx cancel,
// mid-backoff.
func TestOpenCodeEventsBackfillBacksOffOnClosedFeed(t *testing.T) {
	const reopens = 9 // 200 ms doubled 8 times passes the 30 s cap
	var want []time.Duration
	for w := feedRetry; len(want) <= reopens; w = min(2*w, feedRetryMax) {
		want = append(want, w)
	}
	for _, first := range []loomharness.EventType{"", loomharness.EventFeedGap, loomharness.EventItemCompleted} {
		e := newCreateEnv(t)
		s := e.service(ServiceConfig{})
		clk := useTestClock(s)
		var opened atomic.Int32
		s.harnesses["opencode"] = closedFeeds{e.h, &opened, first}
		stop := runFeed(t, s, "opencode")
		for range reopens {
			settled(t, s) // the feed closed; RunFeed backs off
			if clk.fire() != 1 {
				t.Fatalf("first %q: RunFeed did not back off", first)
			}
		}
		settled(t, s)
		stop()
		if got := clk.backoffs(); !slices.Equal(got, want) || opened.Load() != reopens+1 {
			t.Fatalf("first %q: opened %d times, backoffs %v; want %d and %v", first, opened.Load(), got, reopens+1, want)
		}
	}
}

// TestUsageRowCarriesStepTokens: a harness usage event is saved with its
// step's counts under the payload keys loom usage sums; a turn's other rows
// carry none.
func TestUsageRowCarriesStepTokens(t *testing.T) {
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s := e.service(ServiceConfig{})
	stop := runFeed(t, s, "opencode")
	defer stop()
	a, _ := newLead(t, e, s, "alpha")
	fh.Script(a.AgentID, fake.Turn{Steps: []fake.Step{
		{Usage: &loomharness.Usage{InputTokens: 10, OutputTokens: 20, CacheReadTokens: 30, CacheWriteTokens: 40, CostUSD: 0.25}},
		{Delta: "hi"},
		{Usage: &loomharness.Usage{InputTokens: 1, OutputTokens: 2}},
	}})
	mustSendMsg(t, s, sendReq(a.AgentID, "u1", "go", user))
	drained(t, s, "idle", func() bool { return len(kinds(rows(t, s, a.AgentID, 0), EventIdle)) == 1 })
	type tokens struct { // the keys 2.8's reader (cli/cleanup usageTokens) sums
		In    int64   `json:"inputTokens"`
		Out   int64   `json:"outputTokens"`
		Read  int64   `json:"cacheReadTokens"`
		Write int64   `json:"cacheWriteTokens"`
		Cost  float64 `json:"costUsd"`
	}
	var got []tokens
	for _, r := range rows(t, s, a.AgentID, 0) {
		var u tokens
		if err := json.Unmarshal(r.Payload, &u); err != nil {
			t.Fatal(err)
		}
		if r.Kind == string(loomharness.EventUsage) {
			got = append(got, u)
		} else if u != (tokens{}) {
			t.Errorf("%s row has tokens %+v", r.Kind, u)
		}
	}
	want := []tokens{{10, 20, 30, 40, 0.25}, {1, 2, 0, 0, 0}}
	if !slices.Equal(got, want) {
		t.Fatalf("usage rows %+v, want %+v", got, want)
	}
}

// TestFailureFieldsSurviveBackfill (OR9): a failed turn's failure class and
// retryable are saved on its agent.turn_completed row, from the live feed
// and from a backfill when the feed missed the turn end.
func TestFailureFieldsSurviveBackfill(t *testing.T) {
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s := e.service(ServiceConfig{})
	stop := runFeed(t, s, "opencode")
	defer stop()
	a, _ := newLead(t, e, s, "alpha")
	limit := &loomharness.Failure{Class: loomharness.FailureUsageLimit, Retryable: true}
	fh.Script(a.AgentID,
		fake.Turn{Steps: []fake.Step{{Fail: "usage limit", Failure: limit}}},
		fake.Turn{Steps: []fake.Step{{Fail: "bad key", Failure: &loomharness.Failure{Class: loomharness.FailureAuth}, Gap: true}}},
		fake.Turn{Steps: []fake.Step{{Fail: "no class"}}})
	type failure struct {
		Error   string               `json:"error"`
		Failure *loomharness.Failure `json:"failure"`
	}
	ended := func() []failure {
		var out []failure
		for _, r := range kinds(rows(t, s, a.AgentID, 0), EventTurnCompleted) {
			var f failure
			if err := json.Unmarshal(r.Payload, &f); err != nil {
				t.Fatal(err)
			}
			out = append(out, f)
		}
		return out
	}
	for i, msg := range []string{"one", "two", "three"} {
		mustSendMsg(t, s, sendReq(a.AgentID, "u"+msg, msg, user))
		drained(t, s, "turn "+msg+" ended", func() bool { return len(ended()) == i+1 && s.get(t, a.AgentID).State == StateIdle })
	}
	want := []failure{{"usage limit", limit}, {"bad key", &loomharness.Failure{Class: loomharness.FailureAuth}}, {"no class", nil}}
	if got := ended(); !reflect.DeepEqual(got, want) {
		t.Fatalf("turn_completed rows %+v, want %+v", got, want)
	}
	if r := kinds(rows(t, s, a.AgentID, 0), EventTurnCompleted)[1]; !strings.Contains(string(r.Payload), `"failure":{"class":"auth","retryable":false}`) {
		t.Fatalf("a non-retryable failure must say so: %s", r.Payload)
	}
}
