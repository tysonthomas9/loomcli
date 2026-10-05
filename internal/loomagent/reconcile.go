package loomagent

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// Attention reasons Reconcile raises (design v2 §5.1).
const (
	AttentionSessionMissing   = "session_missing"
	AttentionCreateIncomplete = "create_incomplete" // terminal: the Create can never finish
	AttentionCreateRetrying   = "create_retrying"   // the reconcile queue retries the Create
	AttentionDeleteIncomplete = "delete_incomplete"
)

// Reconcile brings harness's agents in line with the harness after a loom
// serve start or a harness restart (design v2 §4.14). RunFeed runs it each
// time the feed connects or reports a gap, before it reads live events. In
// order it:
//  1. runs reconcileAgent on each row below done (recording the returned
//     NativeRef before agent.created) or with a Delete left half done; one
//     that fails is retried by the reconcile queue;
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
//  4. runs reconcileAgent on every other agent: it ends a running turn the
//     harness no longer runs, and settles handed messages: found is
//     delivered, not_found goes back in line, unknown shows Attention
//     delivery_unknown and is never resent; the dispatcher then hands the
//     next one over. One that fails is retried by the reconcile queue.
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
		case owes(a, true):
			if err := s.reconcileAgent(ctx, a.AgentID); err != nil {
				s.retryLater(a.AgentID)
			}
		case a.DeleteRequested: // a Delete waiting on its user: nothing to resume
		case a.State != StateArchived && (a.RunningTurnID != nil || len(s.openAsks(a.AgentID)) > 0):
			if err := s.resumeLive(ctx, a.AgentID); err != nil {
				slog.Warn("loomagent: reconcile could not resume a session", "agent", a.AgentID, "error", err)
			}
		}
	}
	failed, err := s.backfill(ctx, harness)
	for _, a := range agents {
		if !owes(a, false) && !failed[a.AgentID] && failed != nil {
			if err := s.reconcileAgent(ctx, a.AgentID); err != nil {
				s.retryLater(a.AgentID)
			}
		}
	}
	return err
}

// reconcileBackoff is the first wait before the reconcile queue retries an
// agent; each failure doubles it, up to reconcileBackoffMax.
var reconcileBackoff, reconcileBackoffMax = 100 * time.Millisecond, 30 * time.Second

// owes reports whether a, not deleted, has a lifecycle marker for
// reconcileAgent: a Delete requested, which replaces any Create, or a
// Create below done that is not terminal (create_incomplete: only a retried
// Create request can finish it). With live, a Delete showing
// delete_incomplete is left out: it may need the user's unsaved-work
// confirmation, and a failing retry stays queued with its backoff anyway.
func owes(a loomstore.Agent, live bool) bool {
	r := deref(a.AttentionReason)
	switch {
	case a.DeletedAt != nil:
		return false
	case a.DeleteRequested:
		return !live || r != AttentionDeleteIncomplete
	}
	return a.CreateStep < stepDone && r != AttentionCreateIncomplete
}

// reconcileAgent is the one entry point that finishes agentID's lifecycle
// markers (OR4a) and settles it (OR4b): the task_completed records owed
// for its attempts, its purge-pending native sessions, then a Delete it
// requested, or else its Create steps below done, or else its turn and
// waiting or handed slots (settle). Start-up, feed gaps, the dispatcher's
// wakes, failures and the resync clock all come here. An error
// means retry; a permanent Create failure shows create_incomplete and
// returns nil, as no retry can fix it. It takes each agent lock itself and
// holds none across another's.
func (s *Service) reconcileAgent(ctx context.Context, agentID string) (err error) {
	a, err := s.store.GetAgent(ctx, agentID)
	if errors.Is(err, loomstore.ErrNotFound) || (err == nil && a.WorkspaceID != s.workspaceID) {
		return s.recordMarkers(ctx, agentID) // owed to its parent even once it is gone
	} else if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, s.recordMarkers(ctx, agentID)) }() // after a Delete, which the record names
	err = s.purgePending(ctx, agentID)
	switch {
	case a.DeletedAt != nil:
	case !owes(a, false):
		serr := s.settle(ctx, agentID)
		s.failed(ctx, agentID, AttentionHarnessUnavailable, serr)
		if !isPermanent(serr) {
			err = errors.Join(err, serr)
		}
	case a.DeleteRequested:
		derr := s.delete(ctx, DeleteRequest{AgentID: agentID})
		s.failed(ctx, agentID, AttentionDeleteIncomplete, derr)
		if !isCode(derr, CodeUnsavedWork) { // only the user's Delete, with its fingerprint, gets past it
			err = errors.Join(err, derr)
		}
	default:
		_, cerr := s.finishCreate(ctx, agentID)
		s.createFailed(ctx, agentID, cerr)
		if !isPermanent(cerr) {
			err = errors.Join(err, cerr)
		}
	}
	return err
}

// queued is one agent in the reconcile queue, under s.mu.
type queued struct {
	wait    time.Duration    // the backoff before the next retry
	timer   <-chan time.Time // fires when a retry is due; nil: due now
	due     time.Time        // when timer fires
	running bool
	again   bool // queued again while running: run once more
}

// enqueue queues agentID for reconcile now, unless it is queued already (a
// retry keeps its backoff).
func (s *Service) enqueue(agentID string) {
	s.mu.Lock()
	if q, ok := s.queue[agentID]; !ok {
		s.queue[agentID] = &queued{wait: reconcileBackoff}
	} else if q.running {
		q.again = true
	}
	s.mu.Unlock()
	s.poke()
}

// retryLater queues agentID for reconcile after the first backoff, unless
// it is queued already; one running is run once more.
func (s *Service) retryLater(agentID string) {
	s.mu.Lock()
	if q, ok := s.queue[agentID]; !ok {
		q := &queued{wait: reconcileBackoff}
		s.backOff(q)
		s.queue[agentID] = q
	} else if q.running {
		q.again = true
	}
	s.mu.Unlock()
	s.poke()
}

// backOff sets q's timer to its backoff and doubles the next, under s.mu.
func (s *Service) backOff(q *queued) {
	q.timer, q.due, q.running = s.after(q.wait), time.Now().Add(q.wait), false
	q.wait = min(2*q.wait, reconcileBackoffMax)
}

// poke wakes the dispatcher to look at the reconcile queue.
func (s *Service) poke() {
	select {
	case s.queueWake <- struct{}{}:
	default:
	}
}

// nextRetry is the timer of the queued agent whose retry is due first.
func (s *Service) nextRetry() (string, <-chan time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, first := "", (*queued)(nil)
	for k, q := range s.queue {
		if q.timer != nil && (first == nil || q.due.Before(first.due)) {
			id, first = k, q
		}
	}
	if first == nil {
		return "", nil
	}
	return id, first.timer
}

// fired marks agentID's retry due: its timer, which the dispatcher took, fired.
func (s *Service) fired(agentID string, timer <-chan time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if q, ok := s.queue[agentID]; ok && q.timer == timer {
		q.timer = nil
	}
}

// takeDue takes one queued agent that is due: queued now, or its timer has fired.
func (s *Service) takeDue() (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, q := range s.queue {
		if q.running {
			continue
		}
		if q.timer != nil {
			select {
			case <-q.timer:
				q.timer = nil
			default:
				continue
			}
		}
		q.running = true
		return id, true
	}
	return "", false
}

// reconcileDue runs reconcileAgent on every due queued agent; one that
// fails is retried after its backoff. It reports whether it ran any.
func (s *Service) reconcileDue(ctx context.Context) bool {
	ran := false
	for ctx.Err() == nil {
		id, ok := s.takeDue()
		if !ok {
			break
		}
		ran = true
		err := s.reconcileAgent(ctx, id)
		if err != nil {
			slog.Warn("loomagent: reconcile will retry", "agent", id, "error", err)
		}
		s.mu.Lock()
		switch q := s.queue[id]; {
		case err != nil:
			s.backOff(q)
		case q.again:
			q.running, q.again = false, false
		default:
			delete(s.queue, id)
		}
		s.mu.Unlock()
	}
	return ran
}

// sweepPause runs between resync's reads and its queueing; tests use it to
// interleave a re-Open.
var sweepPause = func() {}

// resync queues every agent of s with a lifecycle marker: a Create below
// done, a Delete requested, a native session purge-pending, a completion
// marker (by its child). The dispatcher runs it at each resubscription and
// on its retry clock, the one recovery resync clock; at its start, with all, it also retries once a Delete
// showing delete_incomplete.
func (s *Service) resync(ctx context.Context, all bool) {
	agents, _, err := s.store.ListAgents(ctx, loomstore.AgentFilter{WorkspaceID: s.workspaceID, IncludeArchived: true})
	pending, perr := s.store.PurgePending(ctx, s.workspaceID)
	owed, merr := s.store.CompletionMarkers(ctx, s.workspaceID)
	sweepPause()
	if err = errors.Join(err, perr, merr); err != nil {
		slog.Warn("loomagent: reconcile resync", "error", err)
	}
	for _, a := range agents {
		if owes(a, !all) {
			s.enqueue(a.AgentID)
		}
	}
	for _, n := range pending {
		s.enqueue(n.AgentID)
	}
	for _, m := range owed {
		s.enqueue(m.Child)
	}
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
// harness no longer runs it, else dispatches, which first puts saved
// task_completed records in its slots and settles a message left handed.
// An error no retry can clear (an unwired harness, an unrecorded session,
// a bad request) is permanent.
func (s *Service) settle(ctx context.Context, agentID string) error {
	defer s.lock(agentID)()
	a, err := s.live(ctx, agentID)
	if err != nil || a.State == StateCreating {
		return err
	}
	sess, _, err := s.current(ctx, a)
	gone := err == nil && sess == nil || errors.Is(err, errUnrecorded) // no retry wires the harness or records the session
	if err == nil && sess != nil {
		if a, err = s.settleClaims(ctx, a, sess); err != nil {
			return err
		}
	}
	if a.RunningTurnID == nil {
		if _, err = s.dispatch(ctx, a); err != nil && gone { // not wake: a failed hand-over is retried
			err = permanent{err}
		}
		return err
	}
	if err != nil || sess == nil {
		err = errors.Join(err, &Error{Code: CodeHarnessUnavailable, Message: a.Harness + " is not available"})
		if gone {
			err = permanent{err}
		}
		return err
	}
	st, err := sess.Status(ctx)
	if errors.Is(err, loomharness.ErrSessionNotFound) {
		return nil // the backfill showed session_missing
	} else if err != nil {
		return openErr(err)
	}
	if st.Running {
		return nil
	}
	if ended, err := endedNatively(ctx, sess, *a.RunningTurnID); err != nil || ended {
		return openErr(err) // it ended after the backfill read: the feed or the next backfill applies its end
	}
	return s.endLostTurn(ctx, a, sess, s.dispatch) // not wake: the queue retries a failed hand-over
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
// stays until a user acts. The end of a turn that ran is saved as a native
// end would be (same EventID), so the chat shows the turn's note and keeps
// it on a reload. next then hands over the next message: dispatch, whose
// error the reconcile queue retries, or wake, which shows it as Attention.
func (s *Service) endLostTurn(ctx context.Context, a loomstore.Agent, sess loomharness.Session,
	next func(context.Context, loomstore.Agent) (loomstore.Agent, error)) error {
	slots, err := s.store.Slots(ctx, a.AgentID)
	if err != nil {
		return err
	}
	requeued, asked := false, map[string]loomharness.Landed{} // one input may span slots (OR4c): ask once per key
	for _, sl := range slots {
		if sl.State != loomstore.SlotHanded {
			continue
		}
		landed, ok := asked[deref(sl.NativeKey)]
		var err error
		if !ok {
			landed, err = sess.HasInput(ctx, deref(sl.NativeKey))
			asked[deref(sl.NativeKey)] = landed
		}
		switch {
		case err != nil || landed == loomharness.LandedUnknown:
			if r := deref(a.AttentionReason); r == "" || r == AttentionHarnessUnavailable { // it replaces a failed settle's
				_, err = s.raiseAttention(ctx, a, AttentionDeliveryUnknown)
				return err
			}
			return nil
		case landed == loomharness.LandedNotFound:
			if err := s.store.Requeue(ctx, a.AgentID, sl.Sender, sl.RequestID); err != nil {
				return err
			}
			requeued = true
		}
	}
	end := loomharness.Event{Type: loomharness.EventTurnCompleted, TurnID: *a.RunningTurnID, StopReason: "cancelled",
		Session: loomharness.NativeRef{Root: deref(a.HarnessSessionRoot), NativeID: deref(a.HarnessSessionID)}}
	if !requeued { // an input that never landed ran no turn: it runs again, with its own end
		if _, err := s.events.Append(ctx, nativeRow(a.AgentID, EventTurnCompleted, end)); err != nil {
			return err
		}
	}
	a, ended, err := s.endTurn(ctx, a, end)
	if err != nil || !ended {
		return err
	}
	_, err = next(ctx, a)
	return err
}
