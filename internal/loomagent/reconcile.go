package loomagent

import (
	"context"
	"errors"
	"log/slog"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// Attention reasons Reconcile raises (design v2 §5.1).
const (
	AttentionSessionMissing   = "session_missing"
	AttentionCreateIncomplete = "create_incomplete"
	AttentionDeleteIncomplete = "delete_incomplete"
)

// Reconcile brings harness's agents in line with the harness after a loom
// serve start or a harness restart (design v2 §4.14). RunFeed runs it each
// time the feed connects or reports a gap, before it reads live events. In
// order it:
//  1. finishes rows left creating (recording the returned NativeRef before
//     agent.created) and Deletes left half done;
//  2. resumes, once per process, each session with a turn running when Loom
//     went down: Resume installs the current policy (an OpenCode session
//     refuses Prompt and Reply until then), recovers the interrupted turn,
//     and a new NativeRef it returns is recorded before it is used;
//  3. backfills every session's native history: missed events are saved
//     once, open asks are rebuilt and stale ones saved as ask.lost, and a
//     turn that ended while Loom was down ends (a single task finishes once);
//  4. ends a running turn the harness no longer runs, and settles handed
//     messages: found is delivered, not_found goes back in line, unknown
//     shows Attention delivery_unknown and is never resent; the dispatcher
//     then hands the next one over.
//
// RunFeed sends subscribers feed.gap before each pass, so they catch up.
// A failure on one agent shows as its Attention and does not stop the
// others; only a failed backfill is returned, and RunFeed retries the pass.
// A repeat changes nothing.
func (s *Service) Reconcile(ctx context.Context, harness string) error {
	agents, _, err := s.store.ListAgents(ctx, loomstore.AgentFilter{WorkspaceID: s.workspaceID, Harness: harness,
		IncludeArchived: true})
	if err != nil {
		return err
	}
	for _, a := range agents {
		switch {
		case a.DeleteRequested:
			s.failed(ctx, a.AgentID, AttentionDeleteIncomplete, s.Delete(ctx, DeleteRequest{AgentID: a.AgentID}))
		case a.State == StateCreating:
			_, err := s.finishCreate(ctx, a.AgentID) // clears create_incomplete when done
			s.failed(ctx, a.AgentID, AttentionCreateIncomplete, err)
		case a.RunningTurnID != nil:
			if err := s.resumeTurn(ctx, a.AgentID); err != nil {
				slog.Warn("loomagent: reconcile could not resume a running session", "agent", a.AgentID, "error", err)
			}
		}
	}
	if err := s.backfill(ctx, harness); err != nil {
		return err
	}
	for _, a := range agents {
		if !a.DeleteRequested {
			s.failed(ctx, a.AgentID, AttentionHarnessUnavailable, s.settle(ctx, a.AgentID))
		}
	}
	return nil
}

// failed logs err and shows reason as agentID's Attention unless it
// already shows one; a nil err does nothing.
func (s *Service) failed(ctx context.Context, agentID, reason string, err error) {
	if err == nil || isCode(err, CodeAgentNotFound) {
		return
	}
	slog.Warn("loomagent: reconcile", "agent", agentID, "error", err)
	if err := s.flag(ctx, agentID, reason); err != nil {
		slog.Warn("loomagent: reconcile could not raise Attention", "agent", agentID, "reason", reason, "error", err)
	}
}

// resumeTurn resumes agentID's current session if a turn runs on it and
// this process has not resumed it yet.
func (s *Service) resumeTurn(ctx context.Context, agentID string) error {
	defer s.lock(agentID)()
	a, err := s.live(ctx, agentID)
	if err != nil || a.RunningTurnID == nil || a.HarnessSessionID == nil {
		return err
	}
	s.mu.Lock()
	done := s.resumed[loomharness.NativeRef{Root: deref(a.HarnessSessionRoot), NativeID: *a.HarnessSessionID}]
	s.mu.Unlock()
	if done {
		return nil
	}
	_, err = s.resume(ctx, a)
	return err
}

// settle runs after the backfill: it ends agentID's running turn if the
// harness no longer runs it, else dispatches, which first settles a
// message left handed.
func (s *Service) settle(ctx context.Context, agentID string) error {
	defer s.lock(agentID)()
	a, err := s.live(ctx, agentID)
	if err != nil || a.State == StateCreating || a.HarnessSessionID == nil {
		return err
	}
	if a.RunningTurnID == nil {
		_, err = s.wake(ctx, a)
		return err
	}
	sess, _, err := s.current(ctx, a)
	if err != nil || sess == nil {
		return errors.Join(err, &Error{Code: CodeHarnessUnavailable, Message: a.Harness + " is not available"})
	}
	st, err := sess.Status(ctx)
	if errors.Is(err, loomharness.ErrSessionNotFound) {
		return nil // the backfill showed session_missing
	} else if err != nil {
		return harnessErr(err)
	}
	if st.Running {
		return nil
	}
	return s.endLostTurn(ctx, a, sess)
}

// endLostTurn ends a's running turn, which the harness no longer runs and
// whose history has no end: it was cut off while Loom was down. Its handed
// message is checked first; one that never landed goes back in line, and
// one whose fate is unknown shows Attention delivery_unknown and the turn
// stays until a user acts.
func (s *Service) endLostTurn(ctx context.Context, a loomstore.Agent, sess loomharness.Session) error {
	slots, err := s.store.Slots(ctx, a.AgentID)
	if err != nil {
		return err
	}
	for _, sl := range slots {
		if sl.State != loomstore.SlotHanded {
			continue
		}
		landed, err := sess.HasInput(ctx, deref(sl.NativeKey))
		switch {
		case err != nil || landed == loomharness.LandedUnknown:
			if a.AttentionReason == nil {
				_, err = s.raiseAttention(ctx, a, AttentionDeliveryUnknown)
				return err
			}
			return nil
		case landed == loomharness.LandedNotFound:
			if err := s.store.Requeue(ctx, a.AgentID, sl.Sender, sl.RequestID); err != nil {
				return err
			}
		}
	}
	return s.turnCompleted(ctx, a, loomharness.Event{TurnID: *a.RunningTurnID, StopReason: "cancelled"})
}
