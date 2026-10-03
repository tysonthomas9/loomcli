package loomagent

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"

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
//  2. resumes each session with a running or interrupted turn or an open
//     ask that this process has not opened or resumed since it started or
//     the harness restarted: Resume installs the current policy (an OpenCode
//     session refuses Prompt and Reply until then), recovers an interrupted
//     turn, and a new NativeRef it returns is recorded before it is used.
//     An idle session is resumed lazily (§4.15), before its next Prompt
//     (handOff) or Respond;
//  3. backfills every session's native history: missed events are saved
//     once, open asks are rebuilt and stale ones saved as ask.lost, and a
//     turn that ended while Loom was down ends (a single task finishes once);
//  4. ends a running turn the harness no longer runs, and settles handed
//     messages: found is delivered, not_found goes back in line, unknown
//     shows Attention delivery_unknown and is never resent; the dispatcher
//     then hands the next one over.
//
// RunFeed sends subscribers feed.gap before each pass, so they catch up.
// A failure on one agent shows as its Attention, or for a failed history
// read leaves it as it was, and does not stop the others; the failed
// backfills are returned, and RunFeed retries the pass. A repeat changes
// nothing.
func (s *Service) Reconcile(ctx context.Context, harness string) error {
	agents, _, err := s.store.ListAgents(ctx, loomstore.AgentFilter{WorkspaceID: s.workspaceID, Harness: harness,
		IncludeArchived: true})
	if err != nil {
		return err
	}
	for _, a := range agents {
		switch {
		case a.DeleteRequested:
			s.failed(ctx, a.AgentID, AttentionDeleteIncomplete, s.delete(ctx, DeleteRequest{AgentID: a.AgentID}))
		case a.State == StateCreating:
			_, err := s.finishCreate(ctx, a.AgentID) // clears create_incomplete when done
			s.failed(ctx, a.AgentID, AttentionCreateIncomplete, err)
		case a.State != StateArchived && (a.RunningTurnID != nil || len(s.openAsks(a.AgentID)) > 0):
			if err := s.resumeLive(ctx, a.AgentID); err != nil {
				slog.Warn("loomagent: reconcile could not resume a session", "agent", a.AgentID, "error", err)
			}
		}
	}
	failed, err := s.backfill(ctx, harness)
	for _, a := range agents {
		if !a.DeleteRequested && !failed[a.AgentID] && failed != nil {
			s.failed(ctx, a.AgentID, AttentionHarnessUnavailable, s.settle(ctx, a.AgentID))
		}
	}
	return err
}

// recoverAtStart reconciles every wired harness once, then opens the write
// gate; a failure shows on the agents it hit and does not keep it shut.
func (s *Service) recoverAtStart(ctx context.Context) {
	if s.ready == nil {
		return
	}
	defer s.recovered()
	for _, name := range slices.Sorted(maps.Keys(s.harnesses)) {
		if err := s.Reconcile(ctx, name); err != nil {
			slog.Warn("loomagent: start-up reconcile", "harness", name, "error", err)
		}
	}
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

// resumeLive is resumeOnce for agentID under its lock.
func (s *Service) resumeLive(ctx context.Context, agentID string) error {
	defer s.lock(agentID)()
	a, err := s.live(ctx, agentID)
	if err != nil {
		return err
	}
	_, err = s.resumeOnce(ctx, a)
	return err
}

// resumeOnce resumes a's current session, with the agent lock held, if it
// is live and this process has not opened or resumed it since the harness
// last restarted.
func (s *Service) resumeOnce(ctx context.Context, a loomstore.Agent) (loomstore.Agent, error) {
	if a.HarnessSessionID == nil || a.State == StateArchived || a.State == StateCreating {
		return a, nil
	}
	s.mu.Lock()
	done := s.resumed[a.Harness][loomharness.NativeRef{Root: deref(a.HarnessSessionRoot), NativeID: *a.HarnessSessionID}]
	s.mu.Unlock()
	if done {
		return a, nil
	}
	return s.resume(ctx, a)
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
	if ended, err := endedNatively(ctx, sess, *a.RunningTurnID); err != nil || ended {
		return harnessErr(err) // it ended after the backfill read: the feed or the next backfill applies its end
	}
	return s.endLostTurn(ctx, a, sess)
}

// endedNatively reports whether sess's history, read after its Status showed
// no turn running, has the end of running (a turn ID, or an input key until
// turn.started names the turn).
func endedNatively(ctx context.Context, sess loomharness.Session, running string) (bool, error) {
	f := newFold()
	for after := ""; ; {
		page, err := sess.Messages(ctx, after, 100)
		if err != nil {
			return false, err
		}
		for _, e := range page.Events {
			f.add(e)
		}
		if after = page.Next; after == "" {
			_, _, ended := f.resolve(running)
			return ended != nil, nil
		}
	}
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
