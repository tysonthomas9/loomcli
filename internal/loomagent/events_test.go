package loomagent

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"testing"

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

// startFeed runs s's feed ingestion on e's harness until the returned stop.
func startFeed(s *Service, e *createEnv) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.RunFeed(ctx, "opencode") }()
	return func() { cancel(); <-done }
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
			if n := len(kinds(saved, ev.Type)); n < seen[ev.Type] {
				problems = append(problems, ev.Type+" published before it was saved")
			}
			if ev.Type == EventIdle && !slices.ContainsFunc(kinds(saved, EventTurnCompleted), func(r loomstore.Event) bool { return r.TurnID == ev.TurnID }) {
				problems = append(problems, "agent.idle published before its agent.turn_completed was saved")
			}
			mu.Unlock()
		}
	}()
	stop := startFeed(s, e)
	defer stop()
	a, ref := newLead(t, e, s, "alpha")
	fh.Script(a.AgentID, fake.Turn{Steps: []fake.Step{{Delta: "hi"}, {Ask: "t1"}}})
	mustSendMsg(t, s, sendReq(a.AgentID, "u1", "first", user))
	eventually(t, "the ask saved", func() bool { return len(kinds(rows(t, s, a.AgentID, 0), "ask.opened")) == 1 })
	if err := fh.Session(ref).Reply(ctx, "t1", loomharness.Reply{Allow: true}); err != nil {
		t.Fatal(err)
	}
	eventually(t, "idle", func() bool { return len(kinds(rows(t, s, a.AgentID, 0), EventIdle)) == 1 })
	if err := s.Archive(ctx, ArchiveRequest{AgentID: a.AgentID, Reason: ArchiveDone}); err != nil {
		t.Fatal(err)
	}
	stop()
	s.Bus.Unsubscribe(bus)
	<-checked
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	for _, k := range []string{EventStateChanged, EventIdle, EventSettled, EventWaiting} {
		if seen[k] == 0 {
			t.Fatalf("no %s published", k)
		}
	}
	saved := rows(t, s, a.AgentID, 0)
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
	s.backfill(ctx, "opencode") // a repeat of the whole native history
	if again := rows(t, s, a.AgentID, 0); len(again) != len(saved) {
		t.Fatalf("a repeat backfill added %d rows", len(again)-len(saved))
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
	stop1 := startFeed(s1, e)
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
	eventually(t, "the gap backfilled", func() bool { return len(kinds(rows(t, s1, a.AgentID, 0), "ask.opened")) == 1 })
	stop1() // serve crashes
	if err := fh.Session(ref).Reply(ctx, "t2", loomharness.Reply{Allow: true}); err != nil {
		t.Fatal(err) // the turn ends while Loom is down
	}

	s2 := e.service(ServiceConfig{}) // restart
	sub2, err := s2.events.Subscribe(ctx, map[string]int64{a.AgentID: cursor})
	if err != nil {
		t.Fatal(err)
	}
	stop2 := startFeed(s2, e)
	defer stop2()
	eventually(t, "the missed turn end backfilled", func() bool { return s2.get(t, a.AgentID).State == StateIdle })
	mustSendMsg(t, s2, sendReq(a.AgentID, "u3", "third", user)) // live after the restart
	eventually(t, "turn 3 idle", func() bool { return len(kinds(rows(t, s2, a.AgentID, cursor), EventIdle)) == 2 })

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
	s2.backfill(ctx, "opencode")
	if after := len(rows(t, s2, a.AgentID, 0)); after != before {
		t.Fatalf("a repeat backfill added %d rows", after-before)
	}
}
