package loomagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	lh "github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

func openLog(t *testing.T, path string, agents ...string) (*loomstore.Store, *EventLog) {
	t.Helper()
	s, err := loomstore.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	for _, id := range agents {
		err := s.InsertAgent(context.Background(), loomstore.Agent{AgentID: id, WorkspaceID: "ws", Name: id,
			ProfileKey: id, Preset: "lead", PresetVersion: "1", Mode: "persistent", InteractionMode: "interactive",
			RoleKind: "interactive", SpecJSON: "{}", SpecVersion: 1, OwnerKind: "user", OwnerID: "u",
			CreatedByKind: "user", CreatedByID: "u", CreateRequestID: "req-" + id, Repo: "/repo",
			Harness: "fake", State: "idle"})
		if err != nil {
			t.Fatal(err)
		}
	}
	return s, NewEventLog(s)
}

func ev(agent, id, kind string) loomstore.Event {
	return loomstore.Event{AgentID: agent, EventID: id, Kind: kind, Payload: json.RawMessage(`{}`)}
}

func mustAppend(t *testing.T, l *EventLog, e loomstore.Event) loomstore.Event {
	t.Helper()
	got, err := l.Append(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// recv reads n events from s, failing on timeout or early close.
func recv(t *testing.T, s *Subscription, n int) []loomstore.Event {
	t.Helper()
	var out []loomstore.Event
	for len(out) < n {
		select {
		case e, ok := <-s.C:
			if !ok {
				t.Fatalf("subscription closed after %d of %d: %v", len(out), n, s.Err())
			}
			out = append(out, e)
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out after %d of %d events", len(out), n)
		}
	}
	return out
}

func quiet(t *testing.T, s *Subscription) {
	t.Helper()
	select {
	case e := <-s.C:
		t.Fatalf("unexpected event %s seq %d", e.EventID, e.Seq)
	case <-time.After(50 * time.Millisecond):
	}
}

func ids(es []loomstore.Event) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.EventID)
	}
	return out
}

func TestCursorPagingSnapshotAndOutOfTurnEvents(t *testing.T) {
	ctx := context.Background()
	_, l := openLog(t, filepath.Join(t.TempDir(), "loom.db"), "a1")
	// Pre-turn and out-of-turn events (no TurnID) page like turn events.
	mustAppend(t, l, ev("a1", "agent.created", "agent.created"))
	mustAppend(t, l, ev("a1", "msg.waiting.1", "message.waiting"))
	turn := ev("a1", "turn.1", "turn.started")
	turn.TurnID = "t1"
	mustAppend(t, l, turn)
	mustAppend(t, l, ev("a1", "state.1", "agent.state_changed"))

	p1, err := l.Page(ctx, loomstore.EventQuery{AgentID: "a1", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ids(p1.Events)) != "[agent.created msg.waiting.1]" || !p1.More || p1.SnapshotSeq != 4 {
		t.Fatalf("page 1 = %v more=%v snap=%d", ids(p1.Events), p1.More, p1.SnapshotSeq)
	}
	mustAppend(t, l, ev("a1", "late", "agent.state_changed")) // after the snapshot
	p2, err := l.Page(ctx, loomstore.EventQuery{AgentID: "a1", After: p1.Next, Snapshot: p1.SnapshotSeq, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ids(p2.Events)) != "[turn.1 state.1]" || p2.More {
		t.Fatalf("page 2 = %v more=%v", ids(p2.Events), p2.More)
	}
	// A repeated EventID keeps its seq; the cursor read stays stable.
	if again := mustAppend(t, l, ev("a1", "turn.1", "turn.started")); again.Seq != 3 {
		t.Fatalf("repeat EventID got seq %d; want 3", again.Seq)
	}
	p3, err := l.Page(ctx, loomstore.EventQuery{AgentID: "a1", After: p2.Next})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(ids(p3.Events)) != "[late]" {
		t.Fatalf("page 3 = %v", ids(p3.Events))
	}
}

func TestReplayToLiveHandoffNoGapNoDuplicate(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, l := openLog(t, filepath.Join(t.TempDir(), "loom.db"), "a1", "a2")
	const n = 200
	for i := 1; i <= 50; i++ {
		mustAppend(t, l, ev("a1", "e"+strconv.Itoa(i), "item.completed"))
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { // keep appending while the subscriber replays
		defer wg.Done()
		for i := 51; i <= n; i++ {
			mustAppend(t, l, ev("a1", "e"+strconv.Itoa(i), "item.completed"))
			mustAppend(t, l, ev("a2", "x"+strconv.Itoa(i), "item.completed"))
			if i%10 == 0 {
				mustAppend(t, l, ev("a1", "e"+strconv.Itoa(i-5), "item.completed")) // repeat
			}
		}
	}()
	sub, err := l.Subscribe(ctx, map[string]int64{"a1": 10})
	if err != nil {
		t.Fatal(err)
	}
	got := recv(t, sub, n-10)
	wg.Wait()
	for i, e := range got {
		if e.AgentID != "a1" || e.Seq != int64(i+11) || e.EventID != "e"+strconv.Itoa(i+11) {
			t.Fatalf("event %d = %s/%s seq %d", i, e.AgentID, e.EventID, e.Seq)
		}
	}
	quiet(t, sub)
}

func TestReplayLiveOnlyAndContextEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	_, l := openLog(t, filepath.Join(t.TempDir(), "loom.db"), "a1")
	mustAppend(t, l, ev("a1", "old", "agent.created"))
	sub, err := l.Subscribe(ctx, map[string]int64{"a1": LiveOnly})
	if err != nil {
		t.Fatal(err)
	}
	mustAppend(t, l, ev("a1", "new", "agent.state_changed"))
	if e := recv(t, sub, 1)[0]; e.EventID != "new" || e.Seq != 2 {
		t.Fatalf("live-only got %s seq %d", e.EventID, e.Seq)
	}
	cancel()
	for range sub.C {
	}
	if !errors.Is(sub.Err(), context.Canceled) {
		t.Fatalf("Err = %v", sub.Err())
	}
}

func TestReplaySlowSubscriberReconnectsFromLastSeq(t *testing.T) {
	ctx := context.Background()
	_, l := openLog(t, filepath.Join(t.TempDir(), "loom.db"), "a1")
	sub, err := l.Subscribe(ctx, map[string]int64{"a1": 0})
	if err != nil {
		t.Fatal(err)
	}
	first := recv(t, sub, 0)
	total := subscriberBuffer + 10
	for i := 1; i <= total; i++ { // the subscriber reads nothing meanwhile
		mustAppend(t, l, ev("a1", "e"+strconv.Itoa(i), "item.completed"))
	}
	var last int64
	for e := range sub.C {
		first = append(first, e)
		last = e.Seq
	}
	if !errors.Is(sub.Err(), ErrSlowSubscriber) {
		t.Fatalf("Err = %v; want ErrSlowSubscriber", sub.Err())
	}
	again, err := l.Subscribe(ctx, map[string]int64{"a1": last})
	if err != nil {
		t.Fatal(err)
	}
	rest := recv(t, again, total-int(last))
	first = append(first, rest...)
	if len(first) != total || first[total-1].Seq != int64(total) {
		t.Fatalf("got %d events ending at seq %d; want %d", len(first), first[len(first)-1].Seq, total)
	}
	quiet(t, again)
}

// fakeEvent maps a fake harness event to a stable Loom event: its EventID is
// built from native ids only, so the live feed and a catch-up read agree.
func fakeEvent(agent string) func(lh.Event) (loomstore.Event, bool) {
	return func(n lh.Event) (loomstore.Event, bool) {
		if n.Type == lh.EventFeedGap {
			return loomstore.Event{}, false
		}
		return loomstore.Event{AgentID: agent, EventID: n.Session.NativeID + ":" + strconv.FormatInt(n.Seq, 10),
			Kind: string(n.Type), TurnID: n.TurnID, Payload: json.RawMessage(strconv.Quote(n.Text))}, true
	}
}

func TestReplayBackfillAfterReloadAndServeRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	path := filepath.Join(t.TempDir(), "loom.db")
	store, l := openLog(t, path, "a1")
	toEvent := fakeEvent("a1")

	h := fake.New()
	var gapTurn fake.Turn
	for _, f := range fake.Fixtures {
		if f.Name == "feed_gap" {
			gapTurn = f.Turn
		}
	}
	h.Script("a1", gapTurn, fake.Turn{Steps: []fake.Step{{Delta: "d"}}})
	ref, err := h.Open(ctx, lh.OpenSpec{Key: "a1"})
	if err != nil {
		t.Fatal(err)
	}
	feed, err := h.Feed(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sess := h.Session(ref)
	ingest := func(l *EventLog) { // the live feed, as 1.6d will run it
		for {
			select {
			case n := <-feed.Events():
				if e, ok := toEvent(n); ok {
					mustAppend(t, l, e)
				}
			default:
				return
			}
		}
	}
	sub, err := l.Subscribe(ctx, map[string]int64{"a1": 0})
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.Prompt(ctx, lh.Input{Key: "k1", Text: "go"}); err != nil {
		t.Fatal(err)
	}
	ingest(l)
	seen := recv(t, sub, 5) // delta b went missing from the live feed

	// Reload: the client drops and resubscribes from its last seq; then
	// serve restarts on the same database and Reconcile backfills.
	sub2Ctx, sub2Cancel := context.WithCancel(ctx)
	sub2, err := l.Subscribe(sub2Ctx, map[string]int64{"a1": seen[len(seen)-1].Seq})
	if err != nil {
		t.Fatal(err)
	}
	quiet(t, sub2)
	sub2Cancel()
	store.Close()
	_, l2 := openLog(t, path)
	sub3, err := l2.Subscribe(ctx, map[string]int64{"a1": seen[len(seen)-1].Seq})
	if err != nil {
		t.Fatal(err)
	}
	if err := l2.Backfill(ctx, sess, toEvent); err != nil {
		t.Fatal(err)
	}
	if err := l2.Backfill(ctx, sess, toEvent); err != nil { // a repeat adds nothing
		t.Fatal(err)
	}
	if err := sess.Prompt(ctx, lh.Input{Key: "k2", Text: "again"}); err != nil {
		t.Fatal(err)
	}
	ingest(l2)
	seen = append(seen, recv(t, sub3, 5)...) // backfilled b, then turn 2 live
	quiet(t, sub3)

	hist, err := sess.Messages(ctx, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, n := range hist.Events {
		e, _ := toEvent(n)
		want[e.EventID] = true
	}
	got := map[string]bool{}
	for i, e := range seen {
		if got[e.EventID] || e.Seq != int64(i+1) {
			t.Fatalf("event %d %s seq %d: duplicate or out of order", i, e.EventID, e.Seq)
		}
		got[e.EventID] = true
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("logged %v; native history %v", got, want)
	}
}

// TestNotifyToolStartsForSubscriptionsNamingThem: a subscription that names
// tool.started gets tool starts without deltas; one that names neither gets
// no tool starts or deltas; one with deltas gets both.
func TestNotifyToolStartsForSubscriptionsNamingThem(t *testing.T) {
	ctx := context.Background()
	_, l := openLog(t, filepath.Join(t.TempDir(), "loom.db"), "a1")
	sub := func(deltas bool, kinds ...string) *Subscription {
		s := &Subscription{notes: true, deltas: deltas, kinds: map[string]bool{}}
		for _, k := range kinds {
			s.kinds[k] = true
		}
		got, err := l.subscribe(ctx, map[string]int64{"a1": LiveOnly}, s)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	named := sub(false, "item.completed", KindToolStarted)
	plain := sub(false, "item.completed")
	all := sub(false)
	withDeltas := sub(true, "item.completed")
	l.Notify(loomstore.Event{AgentID: "a1", EventID: "d", Kind: KindDelta})
	l.Notify(loomstore.Event{AgentID: "a1", EventID: "t", Kind: KindToolStarted})
	mustAppend(t, l, ev("a1", "c", "item.completed"))
	for name, c := range map[string]struct {
		s    *Subscription
		want []string
	}{
		"named":       {named, []string{"t", "c"}},
		"plain":       {plain, []string{"c"}},
		"all kinds":   {all, []string{"c"}},
		"with deltas": {withDeltas, []string{"d", "t", "c"}},
	} {
		got := ids(recv(t, c.s, len(c.want))) // a saved row and a notice can cross
		slices.Sort(got)
		slices.Sort(c.want)
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Fatalf("%s: got %v, want %v", name, got, c.want)
		}
		quiet(t, c.s)
	}
}
