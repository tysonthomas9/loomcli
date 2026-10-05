package loomagent

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// TestDecideTurnCompletedTable is turn completion's transition table: from
// the agent's row, its slots and the completion event, decideTurnCompleted
// returns whether the event ends the running turn, the handed slots it
// delivers, and the state after it.
func TestDecideTurnCompletedTable(t *testing.T) {
	done := loomharness.Event{Type: loomharness.EventTurnCompleted, TurnID: "turn_1", StopReason: "end_turn"}
	handed := loomstore.Slot{AgentID: "a1", Sender: "user:u", RequestID: "r1", State: loomstore.SlotHanded}
	waitingSlot := loomstore.Slot{AgentID: "a1", Sender: "agent:a2", RequestID: "r2", State: loomstore.SlotWaiting}
	asking := busy("a1", "persistent", StateWaiting)
	asking.WaitingOn = sp("ask_1")
	for _, c := range []struct {
		name string
		in   turnInput
		want string
	}{
		{"persistent", turnInput{Row: busy("a1", "persistent", StateActive), Slots: []loomstore.Slot{handed}, Event: done},
			"ended deliver=[user:u/r1] state=idle outcome= running= waitingOn="},
		{"persistent waiting on an ask", turnInput{Row: asking, Event: done},
			"ended deliver=[] state=idle outcome= running= waitingOn="},
		{"single_task", turnInput{Row: busy("a1", "single_task", StateActive), Slots: []loomstore.Slot{handed}, Event: done},
			"ended deliver=[user:u/r1] state=finished outcome=end_turn running= waitingOn="},
		{"stopping", turnInput{Row: busy("a1", "single_task", StateStopping), Event: done},
			"ended deliver=[] state=stopping outcome= running= waitingOn="},
		{"slots waiting", turnInput{Row: busy("a1", "single_task", StateActive), Slots: []loomstore.Slot{handed, waitingSlot}, Event: done},
			"ended deliver=[user:u/r1] state=active outcome= running= waitingOn="},
		{"stale turn ID", turnInput{Row: busy("a1", "persistent", StateActive), Slots: []loomstore.Slot{handed},
			Event: loomharness.Event{Type: loomharness.EventTurnCompleted, TurnID: "turn_0"}}, "not ended"},
		{"no running turn", turnInput{Row: svcAgent("a1", "persistent", StateIdle), Event: done}, "not ended"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := turnDecisionString(decideTurnCompleted(c.in)); got != c.want {
				t.Fatalf("decideTurnCompleted = %q; want %q", got, c.want)
			}
		})
	}
}

// turnDecisionString is a readable form of decideTurnCompleted's outcome.
func turnDecisionString(d turnDecision) string {
	if !d.Ended {
		return "not ended"
	}
	var deliver []string
	for _, sl := range d.Deliver {
		deliver = append(deliver, sl.Sender+"/"+sl.RequestID)
	}
	return fmt.Sprintf("ended deliver=[%s] state=%s outcome=%s running=%s waitingOn=%s", strings.Join(deliver, " "),
		d.To.State, deref(d.To.Outcome), deref(d.To.RunningTurn), deref(d.To.WaitingOn))
}

// TestTurnCompletedCrashAfterCommit: a single task's completion crashes
// between its commit and its fanout; after a restart the task is finished
// with its outcome and its events saved once, and the completion arriving
// again (a feed replay) changes nothing.
func TestTurnCompletedCrashAfterCommit(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "loom.db")
	s := serviceAt(t, path, busy("a1", "single_task", StateActive))
	done := loomharness.Event{Type: loomharness.EventTurnCompleted, TurnID: "turn_1", StopReason: "end_turn"}
	bus := s.Bus.Subscribe("a1")
	crashCommit(t, 1)
	if !panics(func() { _ = s.turnCompleted(ctx, s.get(t, "a1"), done) }) {
		t.Fatal("turnCompleted did not crash")
	}
	if got := drain(bus); len(got) != 0 {
		t.Fatalf("published %v before the crash; want nothing", types(got))
	}
	s = serviceAt(t, path) // restart
	a, ids0 := s.get(t, "a1"), ids(rows(t, s, "a1", 0))
	if a.State != StateFinished || deref(a.Outcome) != "end_turn" || a.RunningTurnID != nil ||
		!slices.Contains(ids0, "a1:1:"+EventStateChanged) || !slices.Contains(ids0, "a1:1:"+EventSettled) {
		t.Fatalf("after restart: state %s outcome %s running %v events %v; want finished, end_turn, no turn, state_changed and settled",
			a.State, deref(a.Outcome), a.RunningTurnID, ids0)
	}
	if err := s.turnCompleted(ctx, a, done); err != nil {
		t.Fatal(err)
	}
	if again := ids(rows(t, s, "a1", 0)); !slices.Equal(again, ids0) {
		t.Fatalf("replayed completion changed history %v -> %v", ids0, again)
	}
}
