package loomagent

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// WithdrawRequest is the Withdraw input: clear the caller's own waiting message.
type WithdrawRequest struct {
	Envelope
	AgentID string
	// Actor is the sender whose slot is cleared, set by the entry point.
	// Empty means the local user.
	Actor ActorRef `json:"-"`
}

// WithdrawResult is withdrawn, nothing_waiting or already_handed.
type WithdrawResult struct{ Result string }

// Withdraw clears the caller's waiting message (design v2 §4.9). A message
// already handed over can't be withdrawn (already_handed; interrupt instead).
// A clear emits message.withdrawn. Withdraw does not touch Send receipts, so
// a later retry of the withdrawn Send returns its result and stores nothing.
func (s *Service) Withdraw(ctx context.Context, req WithdrawRequest) (WithdrawResult, error) {
	if req.Actor.Kind == "" {
		req.Actor = ActorRef{Kind: "user", ID: "local"}
	}
	defer s.lock(req.AgentID)()
	a, err := s.live(ctx, req.AgentID)
	if err != nil {
		return WithdrawResult{}, err
	}
	sender := senderOf(req.Actor)
	res, err := s.store.ClearSlot(ctx, a.AgentID, sender)
	if err != nil {
		return WithdrawResult{}, err
	}
	if res == loomstore.Withdrawn {
		if err := s.emit(ctx, Event{AgentID: a.AgentID, Type: EventWithdrawn, Reason: sender, Time: time.Now()}, true); err != nil {
			return WithdrawResult{}, err
		}
	}
	return WithdrawResult{Result: res}, nil
}
