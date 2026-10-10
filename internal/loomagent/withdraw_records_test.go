package loomagent

import (
	"context"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// TestWithdrawKeepsWaitingRecord: c1's slot holds c1's message and then
// its record. c1 withdraws its message; the record still reaches the lead,
// once, without the message.
func TestWithdrawKeepsWaitingRecord(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	lead, ref := leadWithKids(t, e, s, "c1")
	mustSendMsg(t, s, sendReq(lead.AgentID, "u0", "start", user)) // the lead's turn runs
	mustSendMsg(t, s, sendReq(lead.AgentID, "m1", "heads up", child))
	finishKids(t, s, "c1")
	dispatchOK(t, s, lead.AgentID) // c1's record follows c1's message
	if w, err := s.Withdraw(ctx, WithdrawRequest{AgentID: lead.AgentID, Actor: child}); err != nil || w.Result != loomstore.Withdrawn {
		t.Fatalf("Withdraw = %+v, %v; want withdrawn", w, err)
	}
	finishTurn(t, s, lead.AgentID, "completed")
	recordOnce(t, e, s, lead.AgentID, ref, 2, completionKey("c1", 1)+" ")
	if text, _ := deliveredRow(t, s, lead.AgentID, inputKey(t, s, lead.AgentID, "agent:c1")); strings.Contains(text, "heads up") {
		t.Fatalf("delivery text %q; want c1's withdrawn message gone", text)
	}
}

// TestWithdrawLeavesOnlyRecord: c1's slot holds only its record, so c1 has
// nothing waiting to withdraw and the lead still gets the record once.
func TestWithdrawLeavesOnlyRecord(t *testing.T) {
	ctx := context.Background()
	e := newCreateEnv(t)
	s := e.service(ServiceConfig{})
	lead, ref := leadWithKids(t, e, s, "c1")
	mustSendMsg(t, s, sendReq(lead.AgentID, "u0", "start", user)) // the lead's turn runs
	finishKids(t, s, "c1")
	dispatchOK(t, s, lead.AgentID) // c1's record waits
	if w, err := s.Withdraw(ctx, WithdrawRequest{AgentID: lead.AgentID, Actor: child}); err != nil || w.Result != loomstore.NothingWaiting {
		t.Fatalf("Withdraw = %+v, %v; want nothing_waiting", w, err)
	}
	finishTurn(t, s, lead.AgentID, "completed")
	recordOnce(t, e, s, lead.AgentID, ref, 2, completionKey("c1", 1)+" ")
}
