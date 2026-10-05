package loomstore

import (
	"context"
	"testing"
)

// TestClaimAskFirstWinsPerTurn: the first claim on an ask wins and a second
// is told it lost; the same ask ID on a later turn is a new ask, and the
// latest claim is the one read back.
func TestClaimAskFirstWinsPerTurn(t *testing.T) {
	ctx := context.Background()
	s, _ := newSlotStore(t)
	c := AskClaim{AgentID: "a1", AskID: "k1", TurnID: "t1", RequestID: "r1", PayloadHash: "h"}
	for i, want := range []bool{true, false} {
		got, won, err := s.ClaimAsk(ctx, c)
		if err != nil || won != want || got.RequestID != "r1" {
			t.Fatalf("claim %d = %+v won %v, %v; want r1 won %v", i, got, won, err, want)
		}
	}
	c.TurnID, c.RequestID = "t2", "r2"
	if _, won, err := s.ClaimAsk(ctx, c); err != nil || !won {
		t.Fatalf("later turn's claim won %v, %v; want won", won, err)
	}
	if got, err := s.AskClaim(ctx, "a1", "k1"); err != nil || got.RequestID != "r2" || got.TurnID != "t2" {
		t.Fatalf("latest claim = %+v, %v; want r2 on t2", got, err)
	}
}
