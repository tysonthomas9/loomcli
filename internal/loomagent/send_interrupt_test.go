package loomagent

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomharness/fake"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

func interruptReq(agentID, requestID, text string, from ActorRef) SendRequest {
	r := sendReq(agentID, requestID, text, from)
	r.Delivery = DeliveryInterrupt
	return r
}

// TestSendInterrupt: Stop is Send{Delivery: interrupt} (design v2 §4.9),
// through the default interrupt hook (the session's own Interrupt). With no
// message it ends the running turn and the waiting slots stay in line; with
// a message it fills or replaces the sender's slot, which is handed over
// first when the interrupted turn ends. A retry by RequestID returns the
// stored result and interrupts nothing.
func TestSendInterrupt(t *testing.T) {
	e := newCreateEnv(t)
	fh := e.h.Harness.(*fake.Harness)
	s := e.service(ServiceConfig{})
	pump(t, s, e.h, e.st)
	a, _ := newLead(t, e, s, "alpha")
	ask := func(id string) fake.Turn { return fake.Turn{Steps: []fake.Step{{Delta: id}, {Ask: id}}} }
	fh.Script(a.AgentID, ask("t1"), ask("t2"), ask("t3"), ask("t4"), ask("t5"))
	reqs := []string{"u1", "c1", "x1"} // the messages sent so far
	handed := func(want ...string) {
		t.Helper()
		eventually(t, "handed "+want[len(want)-1], func() bool { return len(handedReqs(t, s, a.AgentID, reqs...)) == len(want) })
		if got := handedReqs(t, s, a.AgentID, reqs...); !slices.Equal(got, want) {
			t.Fatalf("handed = %v; want %v", got, want)
		}
	}

	mustSendMsg(t, s, sendReq(a.AgentID, "u1", "first", user))
	eventually(t, "u1 delivered", func() bool { return slotState(t, s, a.AgentID, "u1") == loomstore.SlotDelivered })
	mustSendMsg(t, s, sendReq(a.AgentID, "c1", "child done", child))
	mustSendMsg(t, s, sendReq(a.AgentID, "x1", "from the system", ActorRef{Kind: "system", ID: "x"}))

	// Stop with no message: t1 ends, the waiting slots stay, oldest first.
	before := s.get(t, a.AgentID).State
	stop := mustSendMsg(t, s, interruptReq(a.AgentID, "stop1", "", user))
	if stop.Interrupted == nil || !*stop.Interrupted || stop.State != before || stop.MessageID != "" {
		t.Fatalf("stop = %+v; want interrupted, no message, state %s", stop, before)
	}
	handed("u1", "c1")
	if w := waiting(t, s, a.AgentID); !slices.Equal(w, []string{"system:x=from the system"}) {
		t.Fatalf("waiting = %v after stop", w)
	}
	if again := mustSendMsg(t, s, interruptReq(a.AgentID, "stop1", "", user)); again.State != stop.State || again.Interrupted == nil || !*again.Interrupted {
		t.Fatalf("retry = %+v; want %+v", again, stop)
	}
	if r := s.get(t, a.AgentID); r.RunningTurnID == nil {
		t.Fatal("the retried stop interrupted c1's turn")
	}

	// Interrupt with a message: it fills the user's slot and goes ahead of
	// the older x1 when t2 ends.
	r := mustSendMsg(t, s, interruptReq(a.AgentID, "u2", "do this instead", user))
	reqs = append(reqs, "u2")
	if r.Interrupted == nil || !*r.Interrupted || r.State != loomstore.SlotWaiting || r.Replaced || r.MessageID == "" {
		t.Fatalf("interrupt with a message = %+v", r)
	}
	handed("u1", "c1", "u2")
	retry := mustSendMsg(t, s, interruptReq(a.AgentID, "u2", "do this instead", user))
	if retry.MessageID != r.MessageID || retry.Interrupted == nil || !*retry.Interrupted {
		t.Fatalf("retry = %+v; want %+v", retry, r)
	}
	handed("u1", "c1", "u2") // the retry ended no turn

	// Interrupt with a message replaces the sender's waiting text and keeps it first.
	mustSendMsg(t, s, sendReq(a.AgentID, "c2", "child again", child))
	if r := mustSendMsg(t, s, interruptReq(a.AgentID, "c3", "child replaced", child)); !r.Replaced || !*r.Interrupted {
		t.Fatalf("interrupt replacing a waiting slot = %+v", r)
	}
	reqs = append(reqs, "c3")
	handed("u1", "c1", "u2", "c3")
	if w := waiting(t, s, a.AgentID); !slices.Equal(w, []string{"system:x=from the system"}) {
		t.Fatalf("waiting = %v; want only x1", w)
	}
}

// TestSendInterruptIdle: with no turn running there is nothing to interrupt
// (Interrupted false): a message is handed over at once as for queue, and
// an interrupt with no message is no_op. Neither calls the interrupt hook.
func TestSendInterruptIdle(t *testing.T) {
	e := newCreateEnv(t)
	calls := 0
	s := e.service(ServiceConfig{Interrupt: func(context.Context, loomstore.Agent) error { calls++; return nil }})
	pump(t, s, e.h, e.st)
	a, _ := newLead(t, e, s, "alpha")

	r := mustSendMsg(t, s, interruptReq(a.AgentID, "n1", "", user))
	if r.State != StateNoOp || r.Interrupted == nil || *r.Interrupted || r.MessageID != "" {
		t.Fatalf("idle stop = %+v; want no_op, not interrupted", r)
	}
	if again := mustSendMsg(t, s, interruptReq(a.AgentID, "n1", "", user)); again.State != StateNoOp || *again.Interrupted {
		t.Fatalf("retry = %+v", again)
	}
	r = mustSendMsg(t, s, interruptReq(a.AgentID, "m1", "hello", user))
	if r.State != loomstore.SlotHanded || r.Interrupted == nil || *r.Interrupted {
		t.Fatalf("idle interrupt with a message = %+v; want handed, not interrupted", r)
	}
	if calls != 0 {
		t.Fatalf("interrupt hook ran %d times with no turn running", calls)
	}
	if q := mustSendMsg(t, s, sendReq(a.AgentID, "q1", "queued", child)); q.Interrupted != nil {
		t.Fatalf("queue Send = %+v; want no Interrupted", q)
	}
}

// TestSendInterruptRefusals: a bad Delivery, and a message whose sender's
// slot is still handed, are refused before anything is interrupted.
func TestSendInterruptRefusals(t *testing.T) {
	ctx := context.Background()
	calls := 0
	st, err := loomstore.Open(ctx, t.TempDir()+"/loom.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.InsertAgent(ctx, busy("a1", "persistent", StateActive)); err != nil {
		t.Fatal(err)
	}
	s := New(ServiceConfig{Store: st, WorkspaceID: "ws",
		Interrupt: func(context.Context, loomstore.Agent) error { calls++; return nil }})

	bad := sendReq("a1", "b1", "x", user)
	bad.Delivery = "steer"
	var e *Error
	if _, err := s.Send(ctx, bad); !errors.As(err, &e) || e.Code != CodePresetInvalid {
		t.Fatalf("Delivery steer: %v", err)
	}
	mustSendMsg(t, s, sendReq("a1", "q1", "first", user))
	if _, err := st.HandNext(ctx, "a1", func(loomstore.Slot) string { return "k" }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, interruptReq("a1", "i1", "instead", user)); !errors.As(err, &e) || e.Code != CodeAgentBusy {
		t.Fatalf("interrupt over a handed slot: %v; want agent_busy", err)
	}
	if calls != 0 {
		t.Fatalf("a refused interrupt ran the hook %d times", calls)
	}
	if _, err := st.GetReceipt(ctx, "a1", "i1"); !errors.Is(err, loomstore.ErrNotFound) {
		t.Fatalf("refused interrupt stored a receipt: %v", err)
	}
}
