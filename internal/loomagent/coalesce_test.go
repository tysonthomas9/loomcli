package loomagent

import (
	"context"
	"slices"
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
