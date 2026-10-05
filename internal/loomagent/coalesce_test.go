package loomagent

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// leadWithKids creates a real lead on the fake harness and running
// children ids under it.
func leadWithKids(t *testing.T, e *createEnv, s *Service, ids ...string) (loomstore.Agent, loomharness.NativeRef) {
	t.Helper()
	lead, ref := newLead(t, e, s, "lead")
	for _, id := range ids {
		if err := e.st.InsertAgent(context.Background(), childOf(id, lead.AgentID)); err != nil {
			t.Fatal(err)
		}
	}
	return lead, ref
}

// finishKids ends each child's attempt and saves its record on the lead,
// as the reconcile queue does, without waking the lead.
func finishKids(t *testing.T, s *Service, ids ...string) {
	t.Helper()
	for _, id := range ids {
		finishTurn(t, s, id, "completed")
	}
	s.recordCompletions(context.Background())
}

// keys is each child's first-attempt record key.
func keys(ids ...string) []string {
	var out []string
	for _, id := range ids {
		out = append(out, completionKey(id, 1))
	}
	return out
}

// oneInput requires the records of ids handed to lead as one input: every
// record handed, all with the same native key, in turns turns in all.
func oneInput(t *testing.T, e *createEnv, s *Service, lead loomstore.Agent, ref loomharness.NativeRef, turns int, ids ...string) {
	t.Helper()
	want := keys(ids...)
	if got := handedReqs(t, s, lead.AgentID, want...); !slices.Equal(got, want) {
		t.Fatalf("records handed = %v; want all of %v", got, want)
	}
	slots, err := s.store.Slots(context.Background(), lead.AgentID)
	if err != nil {
		t.Fatal(err)
	}
	inputs := map[string]bool{}
	for _, sl := range slots {
		if slices.Contains(want, sl.RequestID) {
			inputs[deref(sl.NativeKey)] = true
		}
	}
	if len(inputs) != 1 || turnsRun(e, ref) != turns {
		t.Fatalf("records went out as %d inputs over %d turns; want one input and %d turns", len(inputs), turnsRun(e, ref), turns)
	}
}

// TestCoalesceTwoCompletionsBeforeIdleLead: two children finish before
// their idle lead is woken; the one hand-over carries both records.
func TestCoalesceTwoCompletionsBeforeIdleLead(t *testing.T) {
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	lead, ref := leadWithKids(t, e, s, "c1", "c2")
	finishKids(t, s, "c1", "c2")
	dispatchOK(t, s, lead.AgentID)
	oneInput(t, e, s, lead, ref, 1, "c1", "c2")
}

// TestCoalesceTwoCompletionsWhileLeadActive: two children finish while
// their lead's turn runs; nothing is handed over until that turn ends, and
// then both records go out together as its next input.
func TestCoalesceTwoCompletionsWhileLeadActive(t *testing.T) {
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	lead, ref := leadWithKids(t, e, s, "c1", "c2")
	mustSendMsg(t, s, sendReq(lead.AgentID, "u1", "go", user)) // with no feed, Loom sees its turn run until finishTurn
	if s.get(t, lead.AgentID).RunningTurnID == nil {
		t.Fatal("setup: the lead's turn is not running")
	}
	finishKids(t, s, "c1", "c2")
	dispatchOK(t, s, lead.AgentID)
	if got := handedReqs(t, s, lead.AgentID, keys("c1", "c2")...); len(got) != 0 {
		t.Fatalf("handed while the lead's turn runs: %v", got)
	}
	finishTurn(t, s, lead.AgentID, "completed") // the lead goes idle
	oneInput(t, e, s, lead, ref, 2, "c1", "c2")
}

// TestSingleChildCompletesOnce: one child's record is handed to its lead
// exactly once, in one turn.
func TestSingleChildCompletesOnce(t *testing.T) {
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	lead, ref := leadWithKids(t, e, s, "c1")
	finishKids(t, s, "c1")
	dispatchOK(t, s, lead.AgentID)
	dispatchOK(t, s, lead.AgentID)
	oneInput(t, e, s, lead, ref, 1, "c1")
}

// TestCoalesceRestartBetweenEventAndWake: two records are saved and Loom
// crashes before the lead is woken. After the restart the dispatcher hands
// them over once, as one input.
func TestCoalesceRestartBetweenEventAndWake(t *testing.T) {
	e := newCreateEnv(t)
	s1 := e.service(ServiceConfig{})
	lead, ref := leadWithKids(t, e, s1, "c1", "c2")
	finishKids(t, s1, "c1", "c2")
	s := e.service(ServiceConfig{}) // the restart
	useTestClock(s)
	runDispatcher(t, s)
	settled(t, s)
	oneInput(t, e, s, lead, ref, 1, "c1", "c2")
}

// TestCoalescePerChildHistoryUnchanged: a batched hand-over keeps one
// task_completed per child attempt in the lead's history.
func TestCoalescePerChildHistoryUnchanged(t *testing.T) {
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	lead, _ := leadWithKids(t, e, s, "c1", "c2")
	finishKids(t, s, "c1", "c2")
	dispatchOK(t, s, lead.AgentID)
	dispatchOK(t, s, lead.AgentID)
	var got []string
	for _, r := range completions(t, s, lead.AgentID) {
		got = append(got, completionKey(r.Child, r.Attempt))
	}
	if !slices.Equal(got, keys("c1", "c2")) {
		t.Fatalf("records = %v; want one per child attempt", got)
	}
}

// deliveredRow saves lead's delivery of the input handed with key as the
// feed does (its text and the records it carried), and returns its payload.
func deliveredRow(t *testing.T, s *Service, lead, key string) (text string, done []Completion) {
	t.Helper()
	ctx := context.Background()
	e, err := s.withText(ctx, lead, loomharness.Event{Type: loomharness.EventMessageDelivered, InputKey: key})
	if err != nil {
		t.Fatal(err)
	}
	row, err := s.withCompletions(ctx, lead, nativeRow(lead, "message.delivered", e), e)
	if err != nil {
		t.Fatal(err)
	}
	var p struct{ Completions []Completion }
	if err := json.Unmarshal(row.Payload, &p); err != nil {
		t.Fatal(err)
	}
	return e.Text, p.Completions
}

// inputKey is the native key lead's slot from sender was handed with.
func inputKey(t *testing.T, s *Service, lead, sender string) string {
	t.Helper()
	slots, err := s.store.Slots(context.Background(), lead)
	if err != nil {
		t.Fatal(err)
	}
	for _, sl := range slots {
		if sl.Sender == sender {
			return deref(sl.NativeKey)
		}
	}
	t.Fatalf("no slot from %s", sender)
	return ""
}

// TestCoalesceDeliveryNamesEveryRecord: the delivery of a batched input
// carries every record's text and names every record it carried.
func TestCoalesceDeliveryNamesEveryRecord(t *testing.T) {
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	lead, _ := leadWithKids(t, e, s, "c1", "c2")
	finishKids(t, s, "c1", "c2")
	dispatchOK(t, s, lead.AgentID)
	text, done := deliveredRow(t, s, lead.AgentID, inputKey(t, s, lead.AgentID, "agent:c1"))
	if len(done) != 2 || done[0].Child != "c1" || done[1].Child != "c2" ||
		!strings.Contains(text, `child="c1"`) || !strings.Contains(text, `child="c2"`) {
		t.Fatalf("delivery text %q completions %+v; want both records", text, done)
	}
}

// TestCoalesceUserMessageGoesAlone: a user's message that is next in line
// is handed over alone, its delivery naming no record; the two records
// then go out together as the following input.
func TestCoalesceUserMessageGoesAlone(t *testing.T) {
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	lead, ref := leadWithKids(t, e, s, "c1", "c2")
	mustSendMsg(t, s, sendReq(lead.AgentID, "u0", "start", user))                            // the lead's turn runs
	mustSendMsg(t, s, sendReq(lead.AgentID, "u1", "next", ActorRef{Kind: "user", ID: "u2"})) // u1 waits, first in line
	finishKids(t, s, "c1", "c2")
	finishTurn(t, s, lead.AgentID, "completed")
	if got := handedReqs(t, s, lead.AgentID, append([]string{"u1"}, keys("c1", "c2")...)...); !slices.Equal(got, []string{"u1"}) {
		t.Fatalf("handed with the user's message = %v; want u1 alone", got)
	}
	if text, done := deliveredRow(t, s, lead.AgentID, inputKey(t, s, lead.AgentID, "user:u2")); text != "next" || len(done) != 0 {
		t.Fatalf("user delivery text %q completions %+v", text, done)
	}
	finishTurn(t, s, lead.AgentID, "completed")
	oneInput(t, e, s, lead, ref, 3, "c1", "c2")
}
