package loomagent

import (
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
)

// recordOnce requires lead's next input, from c1, to be text with c1's one
// record, and that record never to be handed over again.
func recordOnce(t *testing.T, e *createEnv, s *Service, lead string, ref loomharness.NativeRef, turns int, text string) {
	t.Helper()
	if got := turnsRun(e, ref); got != turns {
		t.Fatalf("lead ran %d turns; want %d, the last carrying c1's record", got, turns)
	}
	got, done := deliveredRow(t, s, lead, inputKey(t, s, lead, "agent:c1"))
	if !strings.HasPrefix(got, text) || !strings.Contains(got, "child=") || len(done) != 1 || done[0].Child != "c1" {
		t.Fatalf("delivery text %q completions %+v; want %q and c1's record", got, done, text)
	}
	finishTurn(t, s, lead, "completed")
	s.recordCompletions(context.Background())
	dispatchOK(t, s, lead)
	if got := turnsRun(e, ref); got != turns {
		t.Fatalf("lead ran %d turns; want %d, c1's record handed over once", got, turns)
	}
}

// TestSendKeepsWaitingRecord: c1 finishes while its lead is busy, then
// Sends the lead a message before the record is handed over. The message
// replaces only c1's own text; the lead gets the record exactly once.
func TestSendKeepsWaitingRecord(t *testing.T) {
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	lead, ref := leadWithKids(t, e, s, "c1")
	mustSendMsg(t, s, sendReq(lead.AgentID, "u0", "start", user)) // the lead's turn runs
	finishKids(t, s, "c1")
	dispatchOK(t, s, lead.AgentID) // c1's record waits
	mustSendMsg(t, s, sendReq(lead.AgentID, "m1", "one more thing", child))
	finishTurn(t, s, lead.AgentID, "completed")
	recordOnce(t, e, s, lead.AgentID, ref, 2, "one more thing\n")
}
