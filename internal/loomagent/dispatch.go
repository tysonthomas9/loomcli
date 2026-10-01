package loomagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// dispatchCrash runs at each dispatcher crash point; tests use it to crash there.
var dispatchCrash = func(string) {}

// Attention reasons the dispatcher raises (design v2 §12).
const (
	AttentionHarnessUnavailable = "harness_unavailable"
	AttentionDeliveryUnknown    = "delivery_unknown"
)

// defaultInputKey is the native input key when ServiceConfig.InputKey is nil:
// OpenCode's msg_ id form, which codex and the fake also accept.
func defaultInputKey(_, agentID, requestID string) string {
	sum := sha256.Sum256([]byte(agentID + "\x00" + requestID))
	return "msg_" + hex.EncodeToString(sum[:13])
}

// Dispatch hands agentID's next waiting message to its harness if no turn
// runs (design v2 §8.1.1). It is the one queue-to-harness path: Send, the
// end of a turn, the end of Create and the agent.idle wake all call it, under
// the agent lock, so a message is handed over exactly once. A message left
// handed by a crash is first checked with HasInput: found marks it
// delivered, not found puts it back in line, unknown raises Attention
// delivery_unknown and sends nothing.
func (s *Service) Dispatch(ctx context.Context, agentID string) error {
	defer s.lock(agentID)()
	a, err := s.live(ctx, agentID)
	if err != nil {
		return err
	}
	_, err = s.dispatch(ctx, a)
	return err
}

// wake dispatches for a caller that already accepted its own change. A
// harness failure leaves the message in line and shows Attention
// harness_unavailable; the next wake retries.
func (s *Service) wake(ctx context.Context, a loomstore.Agent) (loomstore.Agent, error) {
	a, err := s.dispatch(ctx, a)
	var e *Error
	if errors.As(err, &e) && (e.Code == CodeHarnessUnavailable || e.Code == CodeHarnessError) {
		cur, rerr := s.live(ctx, a.AgentID)
		if rerr != nil {
			return a, rerr
		}
		return s.raiseAttention(ctx, cur, AttentionHarnessUnavailable)
	}
	return a, err
}

// takes reports whether a can be handed a message now: no turn runs, and it
// is idle, a reopened single task, or archiving as done.
func takes(a loomstore.Agent) bool {
	if a.RunningTurnID != nil || deref(a.AttentionReason) == AttentionDeliveryUnknown {
		return false
	}
	switch a.State {
	case StateIdle, StateActive:
		return true
	case StateStopping:
		return deref(a.ArchiveReason) == ArchiveDone && !a.DeleteRequested
	}
	return false
}

// dispatch is Dispatch with the agent lock held.
func (s *Service) dispatch(ctx context.Context, a loomstore.Agent) (loomstore.Agent, error) {
	if !takes(a) {
		return a, nil
	}
	slots, err := s.store.Slots(ctx, a.AgentID)
	if err != nil {
		return a, err
	}
	for _, sl := range slots {
		if sl.State != loomstore.SlotHanded {
			continue
		}
		var ok bool
		if a, ok, err = s.recoverHanded(ctx, a, sl); err != nil || !ok {
			return a, err
		}
		if slots, err = s.store.Slots(ctx, a.AgentID); err != nil {
			return a, err
		}
		break // one turn's input can be handed at a time
	}
	waiting := slices.ContainsFunc(slots, func(sl loomstore.Slot) bool { return sl.State == loomstore.SlotWaiting })
	if !waiting {
		if a.State == StateStopping {
			return a, s.finishArchive(ctx, a, ArchiveDone)
		}
		return a, nil
	}
	return s.handOff(ctx, a)
}

// recoverHanded settles a message a crash left handed. It returns true when
// no turn runs from it and dispatch may go on: it never landed and is back
// in line, or it landed and its turn already ended.
func (s *Service) recoverHanded(ctx context.Context, a loomstore.Agent, sl loomstore.Slot) (loomstore.Agent, bool, error) {
	sess, _, err := s.current(ctx, a)
	if err != nil || sess == nil {
		return a, false, errors.Join(err, &Error{Code: CodeHarnessUnavailable, Message: a.Harness + " is not available"})
	}
	landed, err := sess.HasInput(ctx, deref(sl.NativeKey))
	if err != nil {
		landed = loomharness.LandedUnknown
	}
	switch landed {
	case loomharness.LandedFound:
		if err := s.store.MarkDelivered(ctx, a.AgentID, sl.Sender, sl.RequestID); err != nil {
			return a, false, err
		}
		st, err := sess.Status(ctx)
		if err != nil {
			return a, false, harnessErr(err)
		}
		if !st.Running {
			return a, true, nil // its turn ran while Loom was down
		}
		to := a.StateOf()
		to.RunningTurn = sl.NativeKey // until turn.started names it
		if st.TurnID != "" {
			to.RunningTurn = &st.TurnID
		}
		if a.State == StateIdle {
			to.State = StateActive
		}
		a, err = s.setState(ctx, a, to)
		return a, false, err
	case loomharness.LandedNotFound:
		return a, true, s.store.Requeue(ctx, a.AgentID, sl.Sender, sl.RequestID)
	}
	if deref(a.AttentionReason) == AttentionDeliveryUnknown {
		return a, false, nil
	}
	a, err = s.raiseAttention(ctx, a, AttentionDeliveryUnknown)
	return a, false, err
}

// handOff resumes a's session under its current policy (R-H), stages
// skills, hands over the oldest waiting slot and prompts with it. A failure
// before the hand-over leaves the message waiting; one after it leaves it
// handed for the HasInput check.
func (s *Service) handOff(ctx context.Context, a loomstore.Agent) (loomstore.Agent, error) {
	a, err := s.resume(ctx, a)
	if err != nil {
		return a, err
	}
	sess, _, err := s.current(ctx, a)
	if err != nil {
		return a, err
	}
	if sess == nil {
		return a, &Error{Code: CodeHarnessUnavailable, Message: a.Harness + " is not available"}
	}
	sl, err := s.handOver(ctx, a, func(sl loomstore.Slot) string { return s.inputKey(a.Harness, a.AgentID, sl.RequestID) })
	if err != nil {
		return a, err
	}
	dispatchCrash("handed")
	if err := sess.Prompt(ctx, loomharness.Input{Key: *sl.NativeKey, Text: sl.Body}); err != nil {
		return a, harnessErr(err)
	}
	dispatchCrash("prompted")
	to := a.StateOf()
	to.RunningTurn = sl.NativeKey // the input key until turn.started names the turn
	if a.State == StateIdle {
		to.State = StateActive
	}
	if deref(to.AttentionReason) == AttentionHarnessUnavailable {
		to.AttentionReason = nil
	}
	return s.setState(ctx, a, to)
}

// HarnessEvent applies one harness event of agentID's current session to its
// slots and turn (the 1.6d feed calls it): message.delivered marks the
// handed message delivered; turn.started names the running turn;
// turn.completed ends it, finishes a single task with the stop reason as its
// outcome when nothing waits, and dispatches the next message. Events of
// another session, or for a turn that is not running, change nothing.
func (s *Service) HarnessEvent(ctx context.Context, agentID string, e loomharness.Event) error {
	defer s.lock(agentID)()
	a, err := s.live(ctx, agentID)
	if err != nil || e.Session != (loomharness.NativeRef{Root: deref(a.HarnessSessionRoot), NativeID: deref(a.HarnessSessionID)}) {
		return err
	}
	switch e.Type {
	case loomharness.EventMessageDelivered:
		return s.delivered(ctx, a, e.InputKey)
	case loomharness.EventTurnStarted:
		return s.turnStarted(ctx, a, e.TurnID)
	case loomharness.EventTurnCompleted:
		return s.turnCompleted(ctx, a, e)
	}
	return nil
}

// turnStarted names a's running turn turnID, only while the turn is still
// known by its input key: a named turn is never renamed.
func (s *Service) turnStarted(ctx context.Context, a loomstore.Agent, turnID string) error {
	if a.RunningTurnID == nil || turnID == "" {
		return nil
	}
	slots, err := s.store.Slots(ctx, a.AgentID)
	if err != nil || !slices.ContainsFunc(slots, func(sl loomstore.Slot) bool { return deref(sl.NativeKey) == *a.RunningTurnID }) {
		return err
	}
	to := a.StateOf()
	to.RunningTurn = &turnID
	_, err = s.setState(ctx, a, to)
	return err
}

// delivered marks a's handed message with native key key delivered.
func (s *Service) delivered(ctx context.Context, a loomstore.Agent, key string) error {
	slots, err := s.store.Slots(ctx, a.AgentID)
	if err != nil {
		return err
	}
	for _, sl := range slots {
		if sl.State == loomstore.SlotHanded && deref(sl.NativeKey) == key {
			return s.store.MarkDelivered(ctx, a.AgentID, sl.Sender, sl.RequestID)
		}
	}
	return nil // already delivered
}

// turnCompleted ends a's running turn e. Only the running turn's own
// completion counts: one for any other turn ID, such as an older turn's late
// completion, or one before turn.started named the turn, changes nothing.
func (s *Service) turnCompleted(ctx context.Context, a loomstore.Agent, e loomharness.Event) error {
	if a.RunningTurnID == nil || *a.RunningTurnID != e.TurnID {
		return nil // not the running turn
	}
	slots, err := s.store.Slots(ctx, a.AgentID)
	if err != nil {
		return err
	}
	for _, sl := range slots { // a turn that ran had its input delivered
		if sl.State == loomstore.SlotHanded {
			if err := s.store.MarkDelivered(ctx, a.AgentID, sl.Sender, sl.RequestID); err != nil {
				return err
			}
		}
	}
	more := slices.ContainsFunc(slots, func(sl loomstore.Slot) bool { return sl.State == loomstore.SlotWaiting })
	to := a.StateOf()
	to.RunningTurn, to.WaitingOn = nil, nil
	switch {
	case a.State == StateStopping || more:
	case a.Mode == "single_task":
		to.State, to.Outcome = StateFinished, &e.StopReason
	default:
		to.State = StateIdle
	}
	if a, err = s.setState(ctx, a, to); err != nil {
		return err
	}
	_, err = s.wake(ctx, a)
	return err
}

// RunDispatcher wakes the dispatcher on agent.idle until ctx ends. It first
// dispatches every agent with a pending slot (a restart), and does so again
// whenever its subscription lags and is replaced.
func (s *Service) RunDispatcher(ctx context.Context) {
	for ctx.Err() == nil {
		sub := s.Bus.Subscribe()
		if ids, err := s.store.PendingAgents(ctx); err == nil {
			for _, id := range ids {
				_ = s.dispatchWake(ctx, id)
			}
		}
		s.follow(ctx, sub)
		s.Bus.Unsubscribe(sub)
	}
}

// follow dispatches on each agent.idle until sub ends or ctx does.
func (s *Service) follow(ctx context.Context, sub *BusSubscription) {
	for {
		select {
		case <-ctx.Done():
			return
		case e, ok := <-sub.C:
			if !ok {
				return
			}
			if e.Type == EventIdle {
				_ = s.dispatchWake(ctx, e.AgentID)
			}
		}
	}
}

// dispatchWake is Dispatch for the background wake: a harness failure shows
// as Attention instead of an error nobody reads.
func (s *Service) dispatchWake(ctx context.Context, agentID string) error {
	defer s.lock(agentID)()
	a, err := s.live(ctx, agentID)
	if err != nil {
		return err
	}
	_, err = s.wake(ctx, a)
	return err
}
