package loomagent

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// moveTo sets id's state to state from its saved row.
func moveTo(t *testing.T, s *Service, id, state string) {
	t.Helper()
	a := s.get(t, id)
	to := a.StateOf()
	to.State = state
	if _, err := s.setState(context.Background(), a, to); err != nil {
		t.Fatalf("%s -> %s: %v", a.State, state, err)
	}
}

// toggleAttention raises or clears id's Attention, retrying a lost race.
func toggleAttention(t *testing.T, s *Service, id string) {
	for {
		a := s.get(t, id)
		var err error
		if a.AttentionReason == nil {
			_, err = s.raiseAttention(context.Background(), a, "look")
		} else {
			_, err = s.clearAttention(context.Background(), a)
		}
		if !errors.Is(err, loomstore.ErrStateChanged) {
			if err != nil {
				t.Error(err)
			}
			return
		}
	}
}

// TestAtomicStateCrashAfterCommitBeforePublish: a crash between the commit
// and the fanout publishes nothing live; after a restart the row change and
// its event exist exactly once, and a subscriber reconnecting from its
// cursor receives the event.
func TestAtomicStateCrashAfterCommitBeforePublish(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loom.db")
	s := serviceAt(t, path, svcAgent("a1", "persistent", StateIdle))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, err := s.events.Subscribe(ctx, map[string]int64{"a1": 0})
	if err != nil {
		t.Fatal(err)
	}
	moveTo(t, s, "a1", StateActive)
	cursor := recv(t, sub, 1)[0].Seq // sub is live now: anything more it gets is a fanout
	commitStateCrash = func() { panic("crash") }
	t.Cleanup(func() { commitStateCrash = func() {} })
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("did not crash")
			}
		}()
		moveTo(t, s, "a1", StateIdle)
	}()
	commitStateCrash = func() {}
	old := s

	s = serviceAt(t, path) // restart
	a := s.get(t, "a1")
	got := rows(t, s, "a1", cursor)
	want := []string{"a1:2:" + EventStateChanged, "a1:2:" + EventIdle}
	if a.State != StateIdle || a.Revision != 2 || !slices.Equal(ids(got), want) {
		t.Fatalf("after restart: state %s revision %d events %v; want idle, 2, %v", a.State, a.Revision, ids(got), want)
	}
	re, err := s.events.Subscribe(ctx, map[string]int64{"a1": cursor})
	if err != nil {
		t.Fatal(err)
	}
	if e := recv(t, re, 2); !slices.Equal(ids(e), want) {
		t.Fatalf("reconnect got %v; want %v", ids(e), want)
	}
	// The crashed change was never fanned out: the old subscriber's next
	// live event is the one after it.
	moveTo(t, old, "a1", StateActive)
	if e := recv(t, sub, 1)[0]; e.EventID != "a1:3:"+EventStateChanged {
		t.Fatalf("old subscriber's next event %s; want a1:3:%s", e.EventID, EventStateChanged)
	}
}

// TestRollbackPublishesNothing: when an event of a state change cannot be
// saved, the row change rolls back with it and nothing is published.
func TestRollbackPublishesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loom.db")
	s := serviceAt(t, path, svcAgent("a1", "persistent", StateIdle))
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER fail_insert BEFORE INSERT ON agent_events WHEN NEW.kind = 'agent.state_changed'
		BEGIN SELECT RAISE(ABORT, 'injected write failure'); END`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, err := s.events.Subscribe(ctx, map[string]int64{"a1": 0})
	if err != nil {
		t.Fatal(err)
	}
	bus := s.Bus.Subscribe("a1")
	a := s.get(t, "a1")
	to := a.StateOf()
	to.State = StateActive
	if _, err := s.setState(ctx, a, to); err == nil {
		t.Fatal("setState succeeded; want the injected failure")
	}
	if b := s.get(t, "a1"); b.State != StateIdle || b.Revision != 0 || len(rows(t, s, "a1", 0)) != 0 {
		t.Fatalf("after rollback: state %s revision %d events %d; want idle, 0, 0", b.State, b.Revision, len(rows(t, s, "a1", 0)))
	}
	// Nothing was published: once the write can succeed, the first event
	// either subscriber gets is the successful change's.
	if _, err := db.Exec(`DROP TRIGGER fail_insert`); err != nil {
		t.Fatal(err)
	}
	moveTo(t, s, "a1", StateActive)
	if e := recv(t, sub, 1)[0]; e.EventID != "a1:1:"+EventStateChanged {
		t.Fatalf("subscriber's first event %s; want a1:1:%s", e.EventID, EventStateChanged)
	}
	if got := drain(bus); len(got) != 1 || got[0].EventID != "a1:1:"+EventStateChanged {
		t.Fatalf("bus got %v; want only the successful change", types(got))
	}
}

// TestRepeatedCycleDistinctEventIDs: a persistent agent going idle -> active
// -> idle -> active -> idle in one attempt saves an event for every
// transition, each with its own revision-based ID.
func TestRepeatedCycleDistinctEventIDs(t *testing.T) {
	s := serviceAt(t, filepath.Join(t.TempDir(), "loom.db"), svcAgent("a1", "persistent", StateIdle))
	for _, st := range []string{StateActive, StateIdle, StateActive, StateIdle} {
		moveTo(t, s, "a1", st)
	}
	got := rows(t, s, "a1", 0)
	want := []string{"a1:1:" + EventStateChanged, "a1:2:" + EventStateChanged, "a1:2:" + EventIdle,
		"a1:3:" + EventStateChanged, "a1:4:" + EventStateChanged, "a1:4:" + EventIdle}
	if !slices.Equal(ids(got), want) {
		t.Fatalf("events %v; want %v", ids(got), want)
	}
	if a := s.get(t, "a1"); a.Revision != 4 || a.Attempt != 1 {
		t.Fatalf("revision %d attempt %d; want 4, 1", a.Revision, a.Attempt)
	}
}

// TestRepeatedCycleReconnect: a subscriber dropped midway through a repeated
// cycle reconnects from its cursor and sees each event exactly once.
func TestRepeatedCycleReconnect(t *testing.T) {
	s := serviceAt(t, filepath.Join(t.TempDir(), "loom.db"), svcAgent("a1", "persistent", StateIdle))
	ctx, cancel := context.WithCancel(context.Background())
	sub, err := s.events.Subscribe(ctx, map[string]int64{"a1": 0})
	if err != nil {
		t.Fatal(err)
	}
	moveTo(t, s, "a1", StateActive)
	moveTo(t, s, "a1", StateIdle)
	seen := recv(t, sub, 3)
	cancel()
	moveTo(t, s, "a1", StateActive)
	moveTo(t, s, "a1", StateIdle)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	re, err := s.events.Subscribe(ctx, map[string]int64{"a1": seen[len(seen)-1].Seq})
	if err != nil {
		t.Fatal(err)
	}
	seen = append(seen, recv(t, re, 3)...)
	moveTo(t, s, "a1", StateActive) // a sentinel: re's next event is its, so re got nothing twice
	if e := recv(t, re, 1)[0]; e.EventID != "a1:5:"+EventStateChanged {
		t.Fatalf("re's next event %s; want the sentinel a1:5:%s", e.EventID, EventStateChanged)
	}
	seen = append(seen, rows(t, s, "a1", 0)[6])
	want := []string{"a1:1:" + EventStateChanged, "a1:2:" + EventStateChanged, "a1:2:" + EventIdle,
		"a1:3:" + EventStateChanged, "a1:4:" + EventStateChanged, "a1:4:" + EventIdle, "a1:5:" + EventStateChanged}
	if all := rows(t, s, "a1", 0); !slices.Equal(ids(seen), ids(all)) || !slices.Equal(ids(all), want) {
		t.Fatalf("seen %v; want each of %v once", ids(seen), ids(all))
	}
}

// collect reads sub until it has every saved event of agents, checking each
// agent's seq rises by exactly one.
func collect(t *testing.T, s *Service, sub *Subscription, agents ...string) map[string][]loomstore.Event {
	t.Helper()
	want := 0
	for _, id := range agents {
		want += len(rows(t, s, id, 0))
	}
	got := map[string][]loomstore.Event{}
	for _, e := range recv(t, sub, want) {
		if n := int64(len(got[e.AgentID])); e.Seq != n+1 {
			t.Fatalf("%s: seq %d after %d; want %d", e.AgentID, e.Seq, n, n+1)
		}
		got[e.AgentID] = append(got[e.AgentID], e)
	}
	for _, id := range agents { // a sentinel write: sub's next events are exactly its own
		var last int64
		if n := len(got[id]); n > 0 {
			last = got[id][n-1].Seq
		}
		toggleAttention(t, s, id)
		next := rows(t, s, id, last)
		if e := recv(t, sub, len(next)); !slices.Equal(ids(e), ids(next)) {
			t.Fatalf("%s: after the sentinel got %v; want %v", id, ids(e), ids(next))
		}
	}
	return got
}

// TestPublishOrderPerAgent: concurrent writers on one agent and across
// agents; a subscriber sees each agent's seq rise with no gap, and the
// events in commit order (Attention raised and cleared alternate).
func TestPublishOrderPerAgent(t *testing.T) {
	agents := []string{"a1", "a2"}
	s := serviceAt(t, filepath.Join(t.TempDir(), "loom.db"),
		svcAgent("a1", "persistent", StateIdle), svcAgent("a2", "persistent", StateIdle))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, err := s.events.Subscribe(ctx, map[string]int64{"a1": 0, "a2": 0})
	if err != nil {
		t.Fatal(err)
	}
	bus := s.Bus.Subscribe(agents...)
	var unheld atomic.Int32 // the lane must be held from before BEGIN until the fanout ends
	commitStateCrash = func() {
		if s.events.mu.TryLock() {
			s.events.mu.Unlock()
			unheld.Add(1)
		}
	}
	t.Cleanup(func() { commitStateCrash = func() {} })
	var wg sync.WaitGroup
	for _, id := range agents {
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for range 10 {
					toggleAttention(t, s, id)
				}
			}()
		}
	}
	wg.Wait()
	if n := unheld.Load(); n != 0 {
		t.Fatalf("%d commits were not under the lane", n)
	}
	busIDs := map[string][]string{}
	for _, e := range drain(bus) {
		busIDs[e.AgentID] = append(busIDs[e.AgentID], e.EventID)
	}
	got := collect(t, s, sub, agents...)
	for _, id := range agents {
		if !slices.Equal(busIDs[id], ids(got[id])) {
			t.Fatalf("%s: bus order %v; want commit order %v", id, busIDs[id], ids(got[id]))
		}
		var att []string
		for _, e := range got[id] {
			if e.Kind == EventAttentionRaised || e.Kind == EventAttentionCleared {
				att = append(att, e.Kind)
			}
		}
		if len(att) != 40 {
			t.Fatalf("%s: %d attention events; want 40", id, len(att))
		}
		for i, k := range att {
			if want := []string{EventAttentionRaised, EventAttentionCleared}[i%2]; k != want {
				t.Fatalf("%s: attention event %d is %s; want %s (publish order is not commit order)", id, i, k, want)
			}
		}
	}
}

// TestReconnectNoGap: a subscriber that drops and reconnects from its
// cursor while writes go on sees every event once, with no gap.
func TestReconnectNoGap(t *testing.T) {
	s := serviceAt(t, filepath.Join(t.TempDir(), "loom.db"), svcAgent("a1", "persistent", StateIdle))
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 40 {
			toggleAttention(t, s, "a1")
		}
	}()
	var seen []loomstore.Event
	cursor, finished := int64(0), false
	for {
		if finished { // no more writes: stop once every saved event is seen
			if all := rows(t, s, "a1", 0); int64(len(all)) == cursor {
				if !slices.Equal(ids(seen), ids(all)) {
					t.Fatalf("seen %v; want %v", ids(seen), ids(all))
				}
				return
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		sub, err := s.events.Subscribe(ctx, map[string]int64{"a1": cursor})
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
			finished = true
			done = nil // a nil channel never fires again
		case e, ok := <-sub.C:
			if !ok {
				t.Fatalf("subscription closed: %v", sub.Err())
			}
			if e.Seq != cursor+1 {
				t.Fatalf("reconnect from %d got seq %d", cursor, e.Seq)
			}
			seen, cursor = append(seen, e), e.Seq
		}
		cancel()
	}
}

// TestSendReopenCrashAfterCommit: a crash between a reopening Send's commit
// and its fanout leaves, after a restart, the row reopened, the slot and
// receipt, and the agent.state_changed and message.waiting events, each
// exactly once; a subscriber reconnecting from its cursor receives them, and
// a retry of the Send adds nothing.
func TestSendReopenCrashAfterCommit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loom.db")
	s := serviceAt(t, path, svcAgent("a1", "persistent", StateFinished))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, err := s.events.Subscribe(ctx, map[string]int64{"a1": 0})
	if err != nil {
		t.Fatal(err)
	}
	oldBus := s.Bus.Subscribe("a1")
	if _, err := s.events.Append(ctx, loomstore.Event{AgentID: "a1", EventID: "pre", Kind: "test.sentinel",
		Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	cursor := recv(t, sub, 1)[0].Seq // sub is live now: anything more it gets is a fanout
	commitStateCrash = func() { panic("crash") }
	t.Cleanup(func() { commitStateCrash = func() {} })
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("did not crash")
			}
		}()
		_, _ = s.Send(ctx, sendReq("a1", "r1", "again", user))
	}()
	commitStateCrash = func() {}
	// Nothing was fanned out: the old subscriber's next live event is a
	// sentinel written after the crash, and the Bus got nothing.
	if _, err := s.events.Append(ctx, loomstore.Event{AgentID: "a1", EventID: "sentinel", Kind: "test.sentinel",
		Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if e := recv(t, sub, 1)[0]; e.EventID != "sentinel" {
		t.Fatalf("old subscriber got %s before the sentinel", e.EventID)
	}
	if got := drain(oldBus); len(got) != 0 {
		t.Fatalf("bus got %v before the fanout", types(got))
	}

	s = serviceAt(t, path)                                                      // restart
	want := []string{"a1:1:" + EventStateChanged, "a1:send:r1:" + EventWaiting} // then the sentinel
	check := func() {
		t.Helper()
		a := s.get(t, "a1")
		if a.State != StateActive || a.Attempt != 2 || a.Revision != 1 || !slices.Equal(ids(rows(t, s, "a1", cursor)), append(want, "sentinel")) ||
			!slices.Equal(waiting(t, s, "a1"), []string{"user:u=again"}) {
			t.Fatalf("after restart: state %s attempt %d revision %d events %v waiting %q; want active, 2, 1, %v, [user:u=again]",
				a.State, a.Attempt, a.Revision, ids(rows(t, s, "a1", cursor)), waiting(t, s, "a1"), want)
		}
		if _, err := s.store.GetReceipt(ctx, "a1", "r1"); err != nil {
			t.Fatalf("receipt: %v", err)
		}
	}
	check()
	re, err := s.events.Subscribe(ctx, map[string]int64{"a1": cursor})
	if err != nil {
		t.Fatal(err)
	}
	if e := recv(t, re, 2); !slices.Equal(ids(e), want) {
		t.Fatalf("reconnect got %v; want %v", ids(e), want)
	}
	bus := s.Bus.Subscribe("a1")
	if r, err := s.Send(ctx, sendReq("a1", "r1", "again", user)); err != nil || r.State != loomstore.SlotWaiting {
		t.Fatalf("retry = %+v, %v", r, err)
	}
	check()
	if got := drain(bus); len(got) != 0 {
		t.Fatalf("retry published %v", types(got))
	}
	// The reopen's revision bump keeps OR2's CAS: the next change from the
	// reloaded row commits at revision 2.
	if _, err := s.raiseAttention(ctx, s.get(t, "a1"), "look"); err != nil {
		t.Fatal(err)
	}
	if a, got := s.get(t, "a1"), ids(rows(t, s, "a1", 0)); a.Revision != 2 || got[len(got)-2] != "a1:2:"+EventAttentionRaised {
		t.Fatalf("after the next change: revision %d events %v; want 2, then a1:2:%s", a.Revision, got, EventAttentionRaised)
	}
}

// TestSendRetrySameRequestNoNewEvents: a Send whose message.waiting event
// cannot be saved stores nothing, so its retry is a first Send that commits
// the slot, receipt and event once; a further retry commits nothing new.
func TestSendRetrySameRequestNoNewEvents(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "loom.db")
	s := serviceAt(t, path, busy("a1", "persistent", StateActive))
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER fail_insert BEFORE INSERT ON agent_events WHEN NEW.kind = 'message.waiting'
		BEGIN SELECT RAISE(ABORT, 'injected write failure'); END`); err != nil {
		t.Fatal(err)
	}
	sub, err := s.events.Subscribe(ctx, map[string]int64{"a1": 0})
	if err != nil {
		t.Fatal(err)
	}
	bus := s.Bus.Subscribe("a1")
	if _, err := s.Send(ctx, sendReq("a1", "r1", "hello", user)); err == nil {
		t.Fatal("Send succeeded; want the injected failure")
	}
	if got := drain(bus); len(got) != 0 {
		t.Fatalf("the failed Send published %v", types(got))
	}
	// The failed Send was never fanned out: the subscriber's first event is a
	// sentinel written after it, and the retry's comes next.
	if _, err := s.events.Append(ctx, loomstore.Event{AgentID: "a1", EventID: "mid", Kind: "test.sentinel",
		Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if e := recv(t, sub, 1)[0]; e.EventID != "mid" || e.Seq != 1 {
		t.Fatalf("subscriber's first event %s seq %d; want the sentinel at 1", e.EventID, e.Seq)
	}
	if _, err := db.Exec(`DROP TRIGGER fail_insert`); err != nil {
		t.Fatal(err)
	}
	first := mustSendMsg(t, s, sendReq("a1", "r1", "hello", user))
	if e := recv(t, sub, 1)[0]; e.EventID != "a1:send:r1:"+EventWaiting || e.Seq != 2 {
		t.Fatalf("subscriber's next event %s seq %d; want a1:send:r1:%s at 2", e.EventID, e.Seq, EventWaiting)
	}
	want := []string{"mid", "a1:send:r1:" + EventWaiting}
	if got := ids(rows(t, s, "a1", 0)); !slices.Equal(got, want) {
		t.Fatalf("after the retry: events %v; want %v", got, want)
	}
	if got := types(drain(bus)); !slices.Equal(got, []string{EventWaiting}) {
		t.Fatalf("bus got %v; want one message.waiting", got)
	}
	if again := mustSendMsg(t, s, sendReq("a1", "r1", "hello", user)); again != first {
		t.Fatalf("second retry = %+v; want %+v", again, first)
	}
	if got := ids(rows(t, s, "a1", 0)); !slices.Equal(got, want) || len(drain(bus)) != 0 {
		t.Fatalf("second retry added events: %v", got)
	}
	// Nor did it publish live: the subscriber's next event is a sentinel.
	if _, err := s.events.Append(ctx, loomstore.Event{AgentID: "a1", EventID: "sentinel", Kind: "test.sentinel",
		Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if e := recv(t, sub, 1)[0]; e.EventID != "sentinel" {
		t.Fatalf("subscriber got %s after the second retry; want the sentinel", e.EventID)
	}
	if got := waiting(t, s, "a1"); !slices.Equal(got, []string{"user:u=hello"}) {
		t.Fatalf("waiting = %q", got)
	}
}
