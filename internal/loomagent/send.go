package loomagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// EventWaiting is published when Send fills or edits a sender's slot.
const EventWaiting = "message.waiting" // Reason: the sender

// Send delivery modes (design v2 §4.9). The default is queue.
const (
	DeliveryQueue     = "queue"
	DeliveryInterrupt = "interrupt"
)

// StateNoOp is the Send state of an interrupt with no message while no turn runs.
const StateNoOp = "no_op"

// SendRequest is the Send input (design v2 §4.9). Phase 1 adds no text cap.
type SendRequest struct {
	Envelope
	AgentID string
	Text    string
	Source  string // user_chat | agent | system
	// Delivery is queue (default) or interrupt: stop the running turn, and
	// hand a message it carries over first when that turn ends.
	Delivery string
	// Actor is the sender, set by the entry point. Empty means the local user.
	Actor ActorRef `json:"-"`
}

// SendResult is what Send returns, and what its receipt stores.
type SendResult struct {
	MessageID string `json:"messageId"`        // from AgentID, sender and RequestID
	State     string `json:"state"`            // handed if the dispatcher handed it over at once, else waiting
	Replaced  bool   `json:"replaced"`         // this Send replaced the sender's waiting text
	TurnID    string `json:"turnId,omitempty"` // set by the dispatcher when a turn starts at once
	// Interrupted is set only for Delivery interrupt: whether a running turn was stopped.
	Interrupted *bool `json:"interrupted,omitempty"`
}

// Send stores text in the sender's one slot (design v2 §4.9, §8.1.8): it
// fills the slot, or replaces the waiting text while keeping its place. The
// slot change, the RequestID receipt and the Send's events commit together
// under the event lane, so a retry of any earlier Send returns its stored
// result and changes nothing. Send to a
// finished single task starts its next attempt and cancels its R29 history
// deadline in that same transaction. Send never calls the harness itself:
// it then runs the dispatcher (§8.1.1) under the same lock, which hands the
// oldest waiting slot over if no turn runs.
//
// Delivery interrupt (Stop) first interrupts the running turn through the
// interrupt hook; its message then fills or replaces the sender's slot,
// marked to be handed over first when the interrupted turn ends. With no
// turn running it is a queue Send, and with no message it stores only its
// receipt (state no_op when no turn ran, else the agent's state).
func (s *Service) Send(ctx context.Context, req SendRequest) (SendResult, error) {
	if req.RequestID == "" {
		return SendResult{}, invalid("Send needs a RequestID")
	}
	if req.Delivery != "" && req.Delivery != DeliveryQueue && req.Delivery != DeliveryInterrupt {
		return SendResult{}, invalid("Send delivery must be queue or interrupt", DeliveryQueue, DeliveryInterrupt)
	}
	if req.Actor.Kind == "" {
		req.Actor = ActorRef{Kind: "user", ID: "local"}
	}
	if r, ok, err := s.receipt(ctx, req); ok || err != nil { // a retry, before any state check
		return r, err
	}
	a, err := s.live(ctx, req.AgentID)
	if err != nil {
		return SendResult{}, err
	}
	if a.State == StateCreating { // run Create's remaining steps first; they take the lock
		if _, err := s.finishCreate(ctx, a.AgentID); err != nil {
			return SendResult{}, &Error{Code: CodeHarnessUnavailable, Message: err.Error()}
		}
	}
	defer s.lockReady(ctx, req.AgentID)()
	if a, err = s.live(ctx, req.AgentID); err != nil {
		return SendResult{}, err
	}
	if err := sendable(a); err != nil {
		return SendResult{}, err
	}
	sender := senderOf(req.Actor)
	var interrupted *bool
	if req.Delivery == DeliveryInterrupt {
		running := a.RunningTurnID != nil
		interrupted = &running
		if r, ok, err := s.interruptTurn(ctx, &a, req, sender); ok || err != nil {
			return r, err
		}
	}
	rec, retry, err := s.commitSend(ctx, a, req, sender, interrupted)
	switch {
	case err != nil:
		return SendResult{}, sendErr(a.AgentID, err)
	case retry:
		return decodeResult(rec)
	}
	return s.accepted(ctx, a, rec)
}

// commitSend saves req's slot change, receipt and events (sendEvents) in one
// transaction under the event lane, then publishes the events. A finished a
// starts its next attempt in it. retry reports that req already had a receipt.
func (s *Service) commitSend(ctx context.Context, a loomstore.Agent, req SendRequest, sender string,
	interrupted *bool) (rec loomstore.Receipt, retry bool, err error) {
	reopen := a.State == StateFinished
	out := sendEvents(a, sender, req.RequestID, reopen)
	rows, err := eventRows(out)
	if err != nil {
		return rec, false, err
	}
	_, err = s.events.commit(func() (saved []loomstore.Event, err error) {
		rec, saved, retry, err = s.store.SendEvents(ctx, loomstore.SlotSend{AgentID: a.AgentID, Sender: sender,
			RequestID: req.RequestID, Body: req.Text, Source: req.Source, Reopen: reopen, First: interrupted != nil && *interrupted,
			Events: rows, Result: func(replaced bool) (string, error) {
				b, err := json.Marshal(SendResult{MessageID: messageID(a.AgentID, sender, req.RequestID),
					State: loomstore.SlotWaiting, Replaced: replaced, Interrupted: interrupted})
				return string(b), err
			}})
		return saved, err
	}, s.busPublish(out))
	return rec, retry, err
}

// sendEvents are the events an accepted Send saves with its slot change:
// a reopen's state change, named by the revision it bumps to, then
// message.waiting, named by the Send's RequestID.
func sendEvents(a loomstore.Agent, sender, requestID string, reopen bool) []Event {
	var out []Event
	if reopen {
		after := a
		after.State, after.Attempt, after.Outcome, after.FinishedAt = StateActive, a.Attempt+1, nil, nil
		out = changeEvents(a, after)
	}
	return append(out, Event{AgentID: a.AgentID, EventID: a.AgentID + ":send:" + requestID + ":" + EventWaiting,
		Type: EventWaiting, Reason: sender, Time: time.Now()})
}

// interruptTurn runs the interrupt step of a Send with Delivery interrupt,
// under the agent lock. It returns done when the Send is complete: a retry
// that raced the first, or an interrupt with no message, whose receipt it
// stores. A message whose sender's slot is still handed is refused before
// anything is interrupted, so a refused Send changes nothing. It reloads *a
// when the interrupt ended the turn (endUnrunTurn).
func (s *Service) interruptTurn(ctx context.Context, a *loomstore.Agent, req SendRequest, sender string) (SendResult, bool, error) {
	if r, ok, err := s.receipt(ctx, req); ok || err != nil {
		return r, true, err
	}
	running := a.RunningTurnID != nil
	if running && req.Text != "" {
		slots, err := s.store.Slots(ctx, a.AgentID)
		if err != nil {
			return SendResult{}, true, err
		}
		if slices.ContainsFunc(slots, func(sl loomstore.Slot) bool { return sl.Sender == sender && sl.State == loomstore.SlotHanded }) {
			return SendResult{}, true, sendErr(a.AgentID, loomstore.ErrSlotBusy)
		}
	}
	if running {
		if err := s.interrupt(ctx, *a); err != nil {
			return SendResult{}, true, harnessErr(err)
		}
		ended, err := s.endUnrunTurn(ctx, *a)
		if err != nil {
			return SendResult{}, true, err
		}
		if ended {
			if *a, err = s.live(ctx, a.AgentID); err != nil {
				return SendResult{}, true, err
			}
		}
	}
	if req.Text != "" {
		return SendResult{}, false, nil
	}
	// With a turn running the design names no Send state for a bare stop:
	// report the agent's state, which stays until the turn ends.
	res := SendResult{State: a.State, Interrupted: &running}
	if !running {
		res.State = StateNoOp
	}
	b, err := json.Marshal(res)
	if err != nil {
		return SendResult{}, true, err
	}
	_, err = s.store.SaveReceipt(ctx, loomstore.Receipt{AgentID: a.AgentID, RequestID: req.RequestID, Sender: sender, ResultJSON: string(b)})
	return res, true, err
}

// accepted runs the dispatcher after an accepted Send, which hands the
// message over if no turn runs. It returns the receipt's result: state
// handed if it was.
func (s *Service) accepted(ctx context.Context, a loomstore.Agent, rec loomstore.Receipt) (SendResult, error) {
	res, err := decodeResult(rec)
	if err != nil {
		return res, err
	}
	if a, err = s.live(ctx, a.AgentID); err != nil {
		return res, err
	}
	if _, err := s.wake(ctx, a); err != nil {
		return res, err
	}
	if rec, err = s.store.GetReceipt(ctx, a.AgentID, rec.RequestID); err != nil {
		return res, err
	}
	return decodeResult(rec)
}

// sendable refuses a Send to an agent that can't take one; it stores nothing.
func sendable(a loomstore.Agent) error {
	switch a.State {
	case StateArchived, StateStopping:
		return &Error{Code: CodeAgentArchived, Message: a.AgentID}
	case StateCreating:
		return &Error{Code: CodeHarnessUnavailable, Message: a.AgentID + " is still being created"}
	}
	return nil
}

// sendErr maps a refused slot change to its public error.
func sendErr(agentID string, err error) error {
	switch {
	case errors.Is(err, loomstore.ErrHistoryPurged):
		return &Error{Code: CodeHistoryExpired, Message: agentID}
	case errors.Is(err, loomstore.ErrSlotBusy):
		return &Error{Code: CodeAgentBusy, Message: "the sender's previous message is being handed over; send again after it is delivered"}
	}
	return err
}

// receipt answers a retry of req from its stored receipt.
func (s *Service) receipt(ctx context.Context, req SendRequest) (SendResult, bool, error) {
	rec, err := s.store.GetReceipt(ctx, req.AgentID, req.RequestID)
	if errors.Is(err, loomstore.ErrNotFound) {
		return SendResult{}, false, nil
	}
	if err != nil {
		return SendResult{}, false, err
	}
	r, err := decodeResult(rec)
	return r, true, err
}

func decodeResult(rec loomstore.Receipt) (SendResult, error) {
	var r SendResult
	return r, json.Unmarshal([]byte(rec.ResultJSON), &r)
}

// senderOf is the slot sender for an actor.
func senderOf(a ActorRef) string { return a.Kind + ":" + a.ID }

// messageID is the Loom message id: from the AgentID, sender and RequestID.
func messageID(agentID, sender, requestID string) string {
	sum := sha256.Sum256([]byte(agentID + "\x00" + sender + "\x00" + requestID))
	return "msg_" + hex.EncodeToString(sum[:13])
}

// endUnrunTurn ends a's running turn after a Stop interrupted it if the
// harness runs no turn and its history has no end for this one, as settle
// does after a restart: an interrupt then stops nothing and no end would
// ever come. OpenCode b30c4d0 leaves a turn so after a rejected permission
// whose end the feed did not see. true means it ended the turn.
func (s *Service) endUnrunTurn(ctx context.Context, a loomstore.Agent) (bool, error) {
	sess, _, err := s.current(ctx, a)
	if err != nil || sess == nil {
		return false, err
	}
	st, err := sess.Status(ctx)
	if errors.Is(err, loomharness.ErrSessionNotFound) {
		return false, nil
	} else if err != nil || st.Running {
		return false, harnessErr(err)
	}
	if ended, err := endedNatively(ctx, sess, *a.RunningTurnID); err != nil || ended {
		return false, harnessErr(err) // the feed applies its end
	}
	return true, s.endLostTurn(ctx, a, sess, s.wake) // a failed hand-over shows as Attention: the Send goes on
}

// sessionInterrupt is the default Interrupt hook (Send, Archive cancelled,
// harness switch): the current session's own Interrupt, the same call on
// every harness. With no session wired there is nothing to interrupt.
func (s *Service) sessionInterrupt(ctx context.Context, a loomstore.Agent) error {
	sess, _, err := s.current(ctx, a)
	if err != nil || sess == nil {
		return err
	}
	_, err = sess.Interrupt(ctx)
	return err
}
