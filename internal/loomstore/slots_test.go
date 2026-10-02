package loomstore

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
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
