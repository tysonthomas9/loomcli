package loomagent

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// TestDecideSendTable is Send's transition table: from the agent's row, its
// slots and the request, decideSend returns the row after the Send, its
// events and its slot change, a retry, or a refusal.
func TestDecideSendTable(t *testing.T) {
	finished := svcAgent("a1", "single_task", StateFinished)
	finished.Attempt, finished.Outcome, finished.FinishedAt = 1, sp("success"), sp("2026-01-01T00:00:00Z")
	deleted := svcAgent("a1", "persistent", StateIdle)
	deleted.DeletedAt = sp("2026-01-01T00:00:00Z")
	handed := []loomstore.Slot{{AgentID: "a1", Sender: senderOf(user), RequestID: "r0", State: loomstore.SlotHanded}}
	other := []loomstore.Slot{{AgentID: "a1", Sender: "agent:a2", RequestID: "r0", State: loomstore.SlotHanded}}
	stopped, prior := true, &loomstore.Receipt{AgentID: "a1", RequestID: "r1", ResultJSON: `{}`}
	interrupt := sendReq("a1", "r1", "hi", user)
	interrupt.Delivery = DeliveryInterrupt
	waitingID := "a1:send:r1:" + EventWaiting

	for _, c := range []struct {
		name string
		in   sendInput
		want string // the decision, or the refusal's code
	}{
		{"idle", sendInput{Row: svcAgent("a1", "persistent", StateIdle), Req: sendReq("a1", "r1", "hi", user)},
			"idle attempt 1; " + waitingID + "; slot hi first=false reopen=false"},
		{"active", sendInput{Row: busy("a1", "persistent", StateActive), Req: sendReq("a1", "r1", "hi", user), Slots: other},
			"active attempt 1; " + waitingID + "; slot hi first=false reopen=false"},
		{"active interrupted", sendInput{Row: busy("a1", "persistent", StateActive), Req: interrupt, Interrupted: &stopped},
			"active attempt 1; " + waitingID + "; slot hi first=true reopen=false"},
		{"active, own message handed", sendInput{Row: busy("a1", "persistent", StateActive), Req: sendReq("a1", "r1", "hi", user), Slots: handed},
			string(CodeAgentBusy)},
		{"finished reopens", sendInput{Row: finished, Req: sendReq("a1", "r1", "hi", user)},
			"active attempt 2; " + EventStateChanged + " finished->active, " + waitingID + "; slot hi first=false reopen=true"},
		{"archived", sendInput{Row: svcAgent("a1", "persistent", StateArchived), Req: sendReq("a1", "r1", "hi", user)},
			string(CodeAgentArchived)},
		{"stopping", sendInput{Row: svcAgent("a1", "persistent", StateStopping), Req: sendReq("a1", "r1", "hi", user)},
			string(CodeAgentArchived)},
		{"creating", sendInput{Row: svcAgent("a1", "persistent", StateCreating), Req: sendReq("a1", "r1", "hi", user)},
			string(CodeHarnessUnavailable)},
		{"deleted", sendInput{Row: deleted, Req: sendReq("a1", "r1", "hi", user)}, string(CodeAgentNotFound)},
		{"duplicate request", sendInput{Row: svcAgent("a1", "persistent", StateArchived), Req: sendReq("a1", "r1", "hi", user),
			Prior: prior}, "retry"},
	} {
		t.Run(c.name, func(t *testing.T) {
			d, err := decideSend(c.in)
			if got := decision(d, err); got != c.want {
				t.Fatalf("decideSend = %q; want %q", got, c.want)
			}
		})
	}
}

// decision is a readable form of decideSend's outcome.
func decision(d sendDecision, err error) string {
	if e, ok := err.(*Error); ok {
		return string(e.Code)
	} else if err != nil {
		return err.Error()
	}
	if d.Retry {
		return "retry"
	}
	var evs []string
	for _, e := range d.Events {
		if e.Type == EventStateChanged {
			evs = append(evs, fmt.Sprintf("%s %s->%s", e.Type, e.From, e.To))
		} else {
			evs = append(evs, e.EventID)
		}
	}
	return fmt.Sprintf("%s attempt %d; %s; slot %s first=%t reopen=%t", d.Row.State, d.Row.Attempt,
		joinComma(evs), d.Slot.Body, d.Slot.First, d.Slot.Reopen)
}

func joinComma(s []string) string {
	out := ""
	for i, v := range s {
		if i > 0 {
			out += ", "
		}
		out += v
	}
	return out
}

// TestSendQueueCrashAfterCommit: a queue Send to an active agent crashes
// between its commit and its fanout; after a restart its slot, receipt and
// message.waiting are each saved once, and its retry adds nothing.
func TestSendQueueCrashAfterCommit(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "loom.db")
	s := serviceAt(t, path, busy("a1", "persistent", StateActive))
	crashCommit(t, 1)
	if !panics(func() { _, _ = s.Send(ctx, sendReq("a1", "r1", "hi", user)) }) {
		t.Fatal("Send did not crash")
	}
	s = serviceAt(t, path) // restart
	check := func() {
		t.Helper()
		got := ids(rows(t, s, "a1", 0))
		if !slices.Equal(got, []string{"a1:send:r1:" + EventWaiting}) || !slices.Equal(waiting(t, s, "a1"), []string{"user:u=hi"}) {
			t.Fatalf("after restart: events %v waiting %q; want one message.waiting and [user:u=hi]", got, waiting(t, s, "a1"))
		}
		if _, err := s.store.GetReceipt(ctx, "a1", "r1"); err != nil {
			t.Fatalf("receipt: %v", err)
		}
	}
	check()
	if r, err := s.Send(ctx, sendReq("a1", "r1", "hi", user)); err != nil || r.State != loomstore.SlotWaiting {
		t.Fatalf("retry = %+v, %v", r, err)
	}
	check()
}
