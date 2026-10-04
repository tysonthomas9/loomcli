package loomstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func send(sender, req, body string) SlotSend {
	return SlotSend{AgentID: "a1", Sender: sender, RequestID: req, Body: body, Source: "user_chat",
		Result: func(replaced bool) (string, error) {
			return fmt.Sprintf(`{"req":%q,"replaced":%t}`, req, replaced), nil
		}}
}

func mustSend(t *testing.T, s *Store, in SlotSend) (Receipt, bool) {
	t.Helper()
	r, retry, err := s.Send(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return r, retry
}

func slotOf(t *testing.T, s *Store, sender string) Slot {
	t.Helper()
	sl, err := s.Slots(context.Background(), "a1")
	if err != nil {
		t.Fatal(err)
	}
	for _, x := range sl {
		if x.Sender == sender {
			return x
		}
	}
	t.Fatalf("no slot for %s", sender)
	return Slot{}
}

func nativeKey(sl Slot) string { return "k-" + sl.RequestID }

func newSlotStore(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "loom.db")
	s := openAt(t, path)
	if err := s.InsertAgent(context.Background(), agent("a1", "interactive")); err != nil {
		t.Fatal(err)
	}
	return s, path
}

func TestSlotOldestWinsExceptFirstAndReplaceKeepsPlace(t *testing.T) {
	ctx := context.Background()
	s, _ := newSlotStore(t)
	mustSend(t, s, send("user:u", "r1", "u1"))
	mustSend(t, s, send("agent:c1", "r2", "c1"))
	mustSend(t, s, send("agent:c2", "r3", "c2"))
	before := *slotOf(t, s, "user:u").QueuedAt
	if r, _ := mustSend(t, s, send("user:u", "r4", "u2")); r.ResultJSON != `{"req":"r4","replaced":true}` {
		t.Fatalf("replace result = %s", r.ResultJSON)
	}
	if got := slotOf(t, s, "user:u"); *got.QueuedAt != before || got.Body != "u2" || got.RequestID != "r4" {
		t.Fatalf("replace moved or lost text: %+v", got)
	}
	in := send("agent:c2", "r5", "c2-interrupt")
	in.First = true
	mustSend(t, s, in) // replaces c2's text and jumps the line
	want := []string{"agent:c2", "user:u", "agent:c1"}
	for i, sender := range want {
		sl, err := s.HandNext(ctx, "a1", nativeKey)
		if err != nil || sl.Sender != sender || sl.State != SlotHanded || *sl.NativeKey != nativeKey(sl) {
			t.Fatalf("hand %d = %+v, %v; want %s", i, sl, err, sender)
		}
		if err := s.MarkDelivered(ctx, "a1", sl.Sender, sl.RequestID); err != nil {
			t.Fatal(err)
		}
		if got := slotOf(t, s, sender); got.State != SlotDelivered || got.First {
			t.Fatalf("delivered slot = %+v", got)
		}
	}
	if _, err := s.HandNext(ctx, "a1", nativeKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("HandNext on empty = %v", err)
	}
	// A refill after delivery takes a new place in line.
	mustSend(t, s, send("agent:c1", "r6", "c1b"))
	mustSend(t, s, send("user:u", "r7", "u3"))
	if sl, _ := s.HandNext(ctx, "a1", nativeKey); sl.Sender != "agent:c1" {
		t.Fatalf("refill order: got %s", sl.Sender)
	}
}

func TestReceiptStaleRequestIDHasNoEffect(t *testing.T) {
	ctx := context.Background()
	s, path := newSlotStore(t)
	r1, _ := mustSend(t, s, send("user:u", "r1", "one"))
	mustSend(t, s, send("user:u", "r2", "two")) // replaces r1

	retry := func(st *Store, req, wantBody, wantState string, orig Receipt) {
		t.Helper()
		in := send("user:u", req, "stale text")
		in.Result = func(bool) (string, error) { t.Fatal("retry rebuilt its result"); return "", nil }
		r, isRetry, err := st.Send(ctx, in)
		if err != nil || !isRetry || r != orig {
			t.Fatalf("retry %s = %+v, %t, %v; want %+v", req, r, isRetry, err, orig)
		}
		if got := slotOf(t, st, "user:u"); got.Body != wantBody || got.State != wantState {
			t.Fatalf("after retry %s slot = %+v", req, got)
		}
	}
	retry(s, "r1", "two", SlotWaiting, r1) // after replace

	sl, err := s.HandNext(ctx, "a1", nativeKey)
	if err != nil {
		t.Fatal(err)
	}
	r2, _ := s.GetReceipt(ctx, "a1", "r2")
	retry(s, "r2", "two", SlotHanded, r2) // while handed
	if err := s.MarkDelivered(ctx, "a1", "user:u", sl.RequestID); err != nil {
		t.Fatal(err)
	}
	r3, _ := mustSend(t, s, send("user:u", "r3", "three"))
	retry(s, "r1", "three", SlotWaiting, r1) // after delivery: newer text untouched
	retry(s, "r2", "three", SlotWaiting, r2)

	if out, err := s.ClearSlot(ctx, "a1", "user:u"); err != nil || out != Withdrawn {
		t.Fatalf("ClearSlot = %s, %v", out, err)
	}
	retry(s, "r3", "three", SlotWithdrawn, r3) // after withdrawal: stays withdrawn

	s.Close()
	s = openAt(t, path) // receipts survive a restart
	retry(s, "r1", "three", SlotWithdrawn, r1)
	retry(s, "r3", "three", SlotWithdrawn, r3)
	if _, err := s.HandNext(ctx, "a1", nativeKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale retries queued a message: %v", err)
	}
}

func TestSlotGuardsAndWithdraw(t *testing.T) {
	ctx := context.Background()
	s, _ := newSlotStore(t)
	if out, _ := s.ClearSlot(ctx, "a1", "user:u"); out != NothingWaiting {
		t.Fatalf("clear empty = %s", out)
	}
	mustSend(t, s, send("user:u", "r1", "one"))
	in := send("user:u", "r2", "two")
	in.Hand, in.NativeKey = true, "k-r2"
	if _, _, err := s.Send(ctx, in); !errors.Is(err, ErrSlotBusy) {
		t.Fatalf("Hand over waiting text = %v", err)
	}
	if _, err := s.GetReceipt(ctx, "a1", "r2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("refused Send stored a receipt: %v", err)
	}
	if _, err := s.HandNext(ctx, "a1", nativeKey); err != nil {
		t.Fatal(err)
	}
	if out, _ := s.ClearSlot(ctx, "a1", "user:u"); out != AlreadyHanded {
		t.Fatalf("clear handed = %s", out)
	}
	if _, _, err := s.Send(ctx, send("user:u", "r3", "three")); !errors.Is(err, ErrSlotBusy) {
		t.Fatalf("fill over handed = %v", err)
	}
	if err := s.MarkDelivered(ctx, "a1", "user:u", "r0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("MarkDelivered stale = %v", err)
	}
	if err := s.MarkDelivered(ctx, "a1", "user:u", "r1"); err != nil {
		t.Fatal(err)
	}
	in.RequestID = "r4"
	if _, _, err := s.Send(ctx, in); err != nil { // idle hand-over goes straight to handed
		t.Fatal(err)
	}
	if got := slotOf(t, s, "user:u"); got.State != SlotHanded || *got.NativeKey != "k-r2" || got.RequestID != "r4" {
		t.Fatalf("hand slot = %+v", got)
	}
}

func TestSlotReceiptCommitTogether(t *testing.T) {
	ctx := context.Background()
	s, _ := newSlotStore(t)
	mustSend(t, s, send("user:u", "r1", "one"))
	in := send("user:u", "r2", "two")
	in.Result = func(bool) (string, error) { return "", errors.New("boom") }
	if _, _, err := s.Send(ctx, in); err == nil {
		t.Fatal("Send with failing result succeeded")
	}
	if got := slotOf(t, s, "user:u"); got.Body != "one" || got.RequestID != "r1" {
		t.Fatalf("slot changed without receipt: %+v", got)
	}
	if _, err := s.GetReceipt(ctx, "a1", "r2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("receipt without slot change: %v", err)
	}
}

func TestSlotReceiptRaceOneEffect(t *testing.T) {
	ctx := context.Background()
	s, path := newSlotStore(t)
	s2 := openAt(t, path) // a second handle stands in for a concurrent writer
	const n = 16
	var built atomic.Int32
	var fresh atomic.Int32
	var wg sync.WaitGroup
	var mu sync.Mutex
	handedTo := map[string]int{}
	errs := make(chan error, 3*n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			st := []*Store{s, s2}[i%2]
			in := send("user:u", "same", fmt.Sprintf("body-%d", i))
			in.Result = func(bool) (string, error) { built.Add(1); return `{"req":"same"}`, nil }
			_, retry, err := st.Send(ctx, in)
			if !retry {
				fresh.Add(1)
			}
			errs <- err
			// Distinct senders racing: each must land in its own slot with its receipt.
			_, _, err = st.Send(ctx, send(fmt.Sprintf("agent:c%d", i), fmt.Sprintf("c%d", i), "x"))
			errs <- err
			sl, err := st.HandNext(ctx, "a1", nativeKey)
			if err == nil {
				mu.Lock()
				handedTo[sl.Sender]++
				mu.Unlock()
			} else if errors.Is(err, ErrNotFound) {
				err = nil
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if fresh.Load() != 1 || built.Load() != 1 {
		t.Fatalf("one RequestID took effect %d times (results built %d)", fresh.Load(), built.Load())
	}
	slots, err := s.Slots(ctx, "a1")
	if err != nil || len(slots) != n+1 {
		t.Fatalf("slots = %d, %v", len(slots), err)
	}
	var receipts, handed int
	s.db.QueryRow(`SELECT COUNT(*) FROM agent_send_receipts WHERE agent_id = 'a1'`).Scan(&receipts)
	for _, sl := range slots {
		if sl.State == SlotHanded {
			handed++
			if handedTo[sl.Sender] != 1 {
				t.Fatalf("%s handed %d times", sl.Sender, handedTo[sl.Sender])
			}
		}
	}
	if receipts != n+1 || handed != len(handedTo) {
		t.Fatalf("receipts = %d, handed = %d, hand-overs = %v", receipts, handed, handedTo)
	}
}

// TestReceiptReopenCancelsExpiryAtomically covers a new background attempt:
// the Send that reopens a finished agent clears its R29 deadline in the same
// transaction as its slot and receipt, whichever of it and the sweep commits
// first, and a Send after the purge stores nothing.
func TestReceiptReopenCancelsExpiryAtomically(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, filepath.Join(t.TempDir(), "loom.db"))
	now := time.Now()
	old := Stamp(now.Add(-HistoryRetention - time.Hour))
	for _, id := range []string{"b_send_first", "b_sweep_first", "b_active"} {
		a := agent(id, "background")
		a.Mode, a.State, a.Attempt, a.Outcome, a.FinishedAt = "single_task", "finished", 1, ptr("failed"), ptr(old)
		if id == "b_active" {
			a.State, a.FinishedAt = "active", nil
		}
		if err := s.InsertAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	reopen := func(id, req string) (Receipt, error) {
		in := send("daemon:d", req, "retry")
		in.AgentID, in.Hand, in.NativeKey, in.Reopen = id, true, "k-"+req, true
		r, _, err := s.Send(ctx, in)
		return r, err
	}

	// The Send commits first: the agent is active on attempt 2 with no
	// deadline, and the racing sweep refuses.
	if _, err := reopen("b_send_first", "r1"); err != nil {
		t.Fatal(err)
	}
	a, _ := s.GetAgent(ctx, "b_send_first")
	if a.State != "active" || a.Attempt != 2 || a.Outcome != nil || a.FinishedAt != nil {
		t.Fatalf("reopened agent = state %s attempt %d outcome %v finished_at %v", a.State, a.Attempt, a.Outcome, a.FinishedAt)
	}
	if err := s.MarkHistoryPurged(ctx, "b_send_first", now); !errors.Is(err, ErrNotDue) {
		t.Fatalf("sweep after the new attempt = %v; want ErrNotDue", err)
	}
	// A retry of that Send has no effect, even on the attempt.
	if _, retry, err := s.Send(ctx, func() SlotSend {
		in := send("daemon:d", "r1", "retry")
		in.AgentID, in.Reopen = "b_send_first", true
		return in
	}()); err != nil || !retry {
		t.Fatalf("retry = %v, %v", retry, err)
	}
	if a, _ := s.GetAgent(ctx, "b_send_first"); a.Attempt != 2 {
		t.Fatalf("a retry started attempt %d", a.Attempt)
	}

	// The sweep commits first: the Send fails and stores nothing.
	if err := s.MarkHistoryPurged(ctx, "b_sweep_first", now); err != nil {
		t.Fatal(err)
	}
	if _, err := reopen("b_sweep_first", "r2"); !errors.Is(err, ErrHistoryPurged) {
		t.Fatalf("Send after the purge = %v; want ErrHistoryPurged", err)
	}
	if _, err := s.GetReceipt(ctx, "b_sweep_first", "r2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a refused Send left a receipt: %v", err)
	}
	if sl, _ := s.Slots(ctx, "b_sweep_first"); len(sl) != 0 {
		t.Fatalf("a refused Send left a slot: %+v", sl)
	}

	// Reopen on an agent that is not finished stores nothing.
	if _, err := reopen("b_active", "r3"); !errors.Is(err, ErrStateChanged) {
		t.Fatalf("reopen of an active agent = %v; want ErrStateChanged", err)
	}
	if _, err := s.GetReceipt(ctx, "b_active", "r3"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a refused Send left a receipt: %v", err)
	}
}

// TestReceiptReopenRacesSweep runs the reopening Send and the sweep at once,
// many times: either the Send wins (no purge) or the sweep does (no receipt).
func TestReceiptReopenRacesSweep(t *testing.T) {
	ctx := context.Background()
	s := openAt(t, filepath.Join(t.TempDir(), "loom.db"))
	now := time.Now()
	old := Stamp(now.Add(-HistoryRetention - time.Hour))
	for i := range 20 {
		id := fmt.Sprintf("b%d", i)
		a := agent(id, "background")
		a.Mode, a.State, a.FinishedAt = "single_task", "finished", ptr(old)
		if err := s.InsertAgent(ctx, a); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var sendErr, sweepErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			in := send("daemon:d", "r", "retry")
			in.AgentID, in.Hand, in.NativeKey, in.Reopen = id, true, "k", true
			_, _, sendErr = s.Send(ctx, in)
		}()
		go func() { defer wg.Done(); sweepErr = s.MarkHistoryPurged(ctx, id, now) }()
		wg.Wait()
		_, receiptErr := s.GetReceipt(ctx, id, "r")
		switch {
		case sendErr == nil && errors.Is(sweepErr, ErrNotDue) && receiptErr == nil:
		case errors.Is(sendErr, ErrHistoryPurged) && sweepErr == nil && errors.Is(receiptErr, ErrNotFound):
		default:
			t.Fatalf("%s: send=%v sweep=%v receipt=%v", id, sendErr, sweepErr, receiptErr)
		}
	}
}

// TestSlotHandedRequeueReceiptAndFinish covers the dispatcher's store calls:
// HandNext marks the Send's receipt handed in its transaction; Requeue puts a
// handed message back in line in its old place; PendingAgents lists agents
// with a waiting or handed slot; a turn ending in finished sets finished_at.
func TestSlotHandedRequeueReceiptAndFinish(t *testing.T) {
	ctx := context.Background()
	s, _ := newSlotStore(t)
	mustSend(t, s, send("user:u", "r1", "one"))
	mustSend(t, s, send("agent:c", "r2", "two"))
	if ids, _ := s.PendingAgents(ctx, "ws"); len(ids) != 1 || ids[0] != "a1" {
		t.Fatalf("pending = %v", ids)
	}
	if ids, _ := s.PendingAgents(ctx, "other"); len(ids) != 0 {
		t.Fatalf("pending in another workspace = %v", ids)
	}
	sl, err := s.HandNext(ctx, "a1", nativeKey)
	if err != nil || sl.RequestID != "r1" {
		t.Fatalf("HandNext = %+v, %v", sl, err)
	}
	if r, _ := s.GetReceipt(ctx, "a1", "r1"); r.ResultJSON != `{"req":"r1","replaced":false,"state":"handed"}` {
		t.Fatalf("receipt after hand-over = %s", r.ResultJSON)
	}
	if err := s.Requeue(ctx, "a1", "user:u", "r1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Requeue(ctx, "a1", "user:u", "r1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Requeue = %v", err)
	}
	if again, _ := s.HandNext(ctx, "a1", nativeKey); again.RequestID != "r1" { // its place kept
		t.Fatalf("after Requeue the next hand-over is %s", again.RequestID)
	}
	for _, sl := range []string{"user:u", "agent:c"} {
		cur := slotOf(t, s, sl)
		if cur.State == SlotWaiting {
			if _, err := s.HandNext(ctx, "a1", nativeKey); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.MarkDelivered(ctx, "a1", sl, slotOf(t, s, sl).RequestID); err != nil {
			t.Fatal(err)
		}
	}
	if ids, _ := s.PendingAgents(ctx, "ws"); len(ids) != 0 {
		t.Fatalf("pending after delivery = %v", ids)
	}

	task := agent("t1", "background")
	task.Mode, task.State = "single_task", "active"
	if err := s.InsertAgent(ctx, task); err != nil {
		t.Fatal(err)
	}
	from := AgentState{State: "active"}
	if err := s.CompareAndSetState(ctx, "t1", from, AgentState{State: "finished", Outcome: ptr("completed")}); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.GetAgent(ctx, "t1"); a.FinishedAt == nil {
		t.Fatal("a finished turn set no finished_at")
	}
}

// TestNotifyMergesOnceByKey: notices join a waiting slot in order and keep
// its place; a notice whose key was already added is skipped; a handed
// slot refuses them.
func TestNotifyMergesOnceByKey(t *testing.T) {
	ctx := context.Background()
	st, _ := newSlotStore(t)
	res := func(string, bool) (string, error) { return "{}", nil }
	n1, n2 := Notice{Key: "k1", Text: "one"}, Notice{Key: "k2", Text: "two"}
	if added, err := st.Notify(ctx, "a1", "agent:c", "system", []Notice{n1}, res); err != nil || !added {
		t.Fatalf("first = %v, %v", added, err)
	}
	if added, err := st.Notify(ctx, "a1", "agent:c", "system", []Notice{n1, n2}, res); err != nil || !added {
		t.Fatalf("second = %v, %v", added, err)
	}
	if added, err := st.Notify(ctx, "a1", "agent:c", "system", []Notice{n1, n2}, res); err != nil || added {
		t.Fatalf("repeat = %v, %v", added, err)
	}
	slots, err := st.Slots(ctx, "a1")
	if err != nil || len(slots) != 1 || slots[0].Body != "one\ntwo" || slots[0].RequestID != "k2" || slots[0].State != SlotWaiting {
		t.Fatalf("slots = %+v, %v", slots, err)
	}
	if _, err := st.HandNext(ctx, "a1", func(Slot) string { return "nk" }); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Notify(ctx, "a1", "agent:c", "system", []Notice{{Key: "k3", Text: "three"}}, res); !errors.Is(err, ErrSlotBusy) {
		t.Fatalf("handed = %v", err)
	}
	if text, sender, ok, err := st.HandedText(ctx, "a1", "nk"); err != nil || !ok || text != "one\ntwo" || sender != "agent:c" {
		t.Fatalf("handed text = %q from %q, %v, %v", text, sender, ok, err)
	}
}

// TestMigrationAttemptBoundaryBackfill: a database from before migration 6
// gets each agent's current-attempt boundary: a first attempt keeps every
// reply; a reopened agent, active or finished, starts at its last saved
// reopen event when every reopen saved one; otherwise (no reopen event, a
// missing latest one, or a finished -> stopping change that is no reopen)
// it starts at its last event, so no earlier reply counts.
func TestMigrationAttemptBoundaryBackfill(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "loom.db")
	all := migrations
	defer func() { migrations = all }()
	migrations = all[:5] // the schema before attempt_after_seq
	old := openAt(t, path)
	event := func(id, eid, kind, payload string) {
		t.Helper()
		if _, err := old.AppendEvent(ctx, Event{AgentID: id, EventID: eid, Kind: kind, Payload: json.RawMessage(payload)}); err != nil {
			t.Fatal(err)
		}
	}
	reply := func(id, text string) {
		event(id, "item:"+text, "item.completed", `{"itemKind":"message","text":"`+text+`"}`)
	}
	reopened := func(id, n string) {
		event(id, "reopen:"+id+":"+n, "agent.state_changed", `{"from":"finished","to":"active"}`)
	}
	for _, a := range []struct {
		id, state string
		attempt   int64
	}{{"first", "finished", 0}, {"active", "active", 1}, {"finished", "finished", 1}, {"newer", "active", 1}, {"unmarked", "finished", 2},
		{"partial", "active", 2}, {"archiving", "stopping", 1}, {"twice", "finished", 2}} {
		ag := agent(a.id, "interactive")
		ag.Mode, ag.State, ag.Attempt = "single_task", a.state, a.attempt
		if err := old.InsertAgent(ctx, ag); err != nil {
			t.Fatal(err)
		}
	}
	reply("first", "first reply")
	for _, id := range []string{"active", "finished", "newer", "unmarked", "partial", "archiving", "twice"} {
		reply(id, "old reply "+id)
	}
	reopened("active", "1")
	reopened("finished", "1")
	reopened("newer", "1")
	reply("newer", "new reply")
	reopened("partial", "1") // attempt 1 was published; attempt 2's reopen was lost in a crash
	reply("partial", "reply from attempt one")
	reopened("archiving", "1")
	reply("archiving", "kept reply")
	event("archiving", "stop:archiving", "agent.state_changed", `{"from":"finished","to":"stopping"}`) // not a reopen
	reopened("twice", "1")
	reply("twice", "reply from attempt one")
	reopened("twice", "2")
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	migrations = all
	s := openAt(t, path)
	for id, want := range map[string]string{"first": "first reply", "active": "", "finished": "", "newer": "new reply",
		"unmarked": "", "partial": "", "archiving": "kept reply", "twice": ""} {
		if got, err := s.LastMessage(ctx, id); err != nil || got != want {
			t.Errorf("%s: LastMessage = %q, %v; want %q", id, got, err, want)
		}
	}
}

// TestMigrationNoticesLegacy: a slot and a receipt saved before notices were
// kept, whose last addition was a child's record, read back as legacy (their
// record key, for the reader to rebuild from); others do not.
func TestMigrationNoticesLegacy(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "loom.db")
	all := migrations
	defer func() { migrations = all }()
	migrations = all[:len(all)-1] // the schema before notices
	old := openAt(t, path)
	if err := old.InsertAgent(ctx, agent("L", "interactive")); err != nil {
		t.Fatal(err)
	}
	for _, sl := range [][2]string{{"agent:c1", "task_completed:c1:1"}, {"agent:c3", "m-plain"}} {
		if _, err := old.db.ExecContext(ctx, `INSERT INTO agent_slots (agent_id, sender, request_id, body, source, state, queued_at, updated_at)
			VALUES ('L', ?, ?, 'text', 'agent', ?, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`, sl[0], sl[1], SlotWaiting); err != nil {
			t.Fatal(err)
		}
	}
	for _, r := range [][2]string{{"k2", "task_completed:c2:1"}, {"k4", "m-other"}} {
		if _, err := old.db.ExecContext(ctx, `INSERT INTO agent_send_receipts (agent_id, request_id, sender, result_json, created_at, native_key)
			VALUES ('L', ?, 'agent:x', '{}', '2026-01-01T00:00:00Z', ?)`, r[1], r[0]); err != nil {
			t.Fatal(err)
		}
	}
	old.Close()
	migrations = all
	s := openAt(t, path)
	w, err := s.WaitingNotices(ctx, "L")
	if err != nil {
		t.Fatal(err)
	}
	if len(w) != 1 || w["agent:c1"].Legacy != "task_completed:c1:1" {
		t.Fatalf("waiting notices = %+v", w)
	}
	for key, want := range map[string]string{"k2": "task_completed:c2:1", "k4": ""} {
		n, err := s.HandedNotices(ctx, "L", key)
		if err != nil || n.Legacy != want || len(n.Keys) != 0 {
			t.Fatalf("handed %s = %+v %v", key, n, err)
		}
	}
}
