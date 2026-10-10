package loomagent

import (
	"context"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// Archive reasons (design v2 §4.7).
const (
	ArchiveDone      = "done"
	ArchiveCancelled = "cancelled"
)

// ArchiveRequest is the Archive input. Reason defaults to done.
type ArchiveRequest struct {
	Envelope
	AgentID, Reason string
}

// Archive stops an agent for good (design v2 §4.7) and starts its R29 history
// clock. A persistent agent archived as done while busy stays stopping until
// its turn and waiting messages finish; the slot dispatcher then calls
// finishArchive. Repeating Archive is safe.
func (s *Service) Archive(ctx context.Context, req ArchiveRequest) error {
	if req.Reason == "" {
		req.Reason = ArchiveDone
	}
	if req.Reason != ArchiveDone && req.Reason != ArchiveCancelled {
		return invalid("archive reason "+req.Reason, ArchiveDone, ArchiveCancelled)
	}
	defer s.lockReady(ctx, req.AgentID)()
	a, err := s.live(ctx, req.AgentID)
	if err != nil {
		return err
	}
	if a.State == StateArchived {
		return s.finishArchive(ctx, a, deref(a.ArchiveReason))
	}
	if req.Reason == ArchiveCancelled {
		if a, err = s.stop(ctx, a, &archiveCols{reason: sp(req.Reason)}); err != nil {
			return err
		}
		if a.Mode == "single_task" {
			to := a.StateOf()
			to.Outcome = sp(ArchiveCancelled)
			if a, err = s.setState(ctx, a, to); err != nil {
				return err
			}
		}
		return s.finishArchive(ctx, a, req.Reason)
	}
	switch {
	case a.Mode == "single_task" && a.State != StateFinished:
		return &Error{Code: CodeAgentBusy, Message: "single task is not finished; archive it as cancelled"}
	case a.State == StateActive || a.State == StateWaiting:
		to := a.StateOf()
		to.State = StateStopping
		_, err = s.changeState(ctx, a, to, &archiveCols{reason: sp(req.Reason)})
		return err
	case a.State == StateStopping:
		return nil
	}
	return s.finishArchive(ctx, a, req.Reason)
}

// finishArchive moves a through stopping to archived and starts the R29
// clock. It keeps a's outcome. Each move records the archive reason in its
// transaction, so a restart finishes a stopping done archive; the move to
// archived also records the clock and agent.archived.
func (s *Service) finishArchive(ctx context.Context, a loomstore.Agent, reason string) error {
	var err error
	now := time.Now()
	if a.State == StateArchived { // a repeat: keep the clock, save nothing new
		err = s.store.SetArchive(ctx, a.AgentID, sp(reason), &now)
	} else {
		if a.State != StateStopping {
			if a, err = s.changeState(ctx, a, loomstore.AgentState{State: StateStopping, Outcome: a.Outcome, Attempt: a.Attempt},
				&archiveCols{reason: sp(reason)}); err != nil {
				return err
			}
		}
		a, err = s.changeState(ctx, a, loomstore.AgentState{State: StateArchived, Outcome: a.Outcome, Attempt: a.Attempt},
			&archiveCols{reason: sp(reason), at: &now}, Event{AgentID: a.AgentID, Type: EventArchived, Reason: reason, Time: now})
	}
	if err != nil {
		return err
	}
	return s.retireLaunch(ctx, a)
}

// retireLaunch runs the Retire hook for a, which is archived or deleted.
func (s *Service) retireLaunch(ctx context.Context, a loomstore.Agent) error {
	if s.retire == nil {
		return nil
	}
	return s.retire(ctx, a)
}

// Unarchive returns a persistent agent to idle and a single task to finished,
// canceling the R29 clock. History already purged fails with history_expired.
func (s *Service) Unarchive(ctx context.Context, req ArchiveRequest) error {
	defer s.lockReady(ctx, req.AgentID)()
	a, err := s.live(ctx, req.AgentID)
	if err != nil {
		return err
	}
	if a.HistoryPurgedAt != nil {
		return &Error{Code: CodeHistoryExpired, Message: a.AgentID}
	}
	if a.State != StateArchived {
		return s.store.SetArchive(ctx, a.AgentID, nil, nil)
	}
	to := loomstore.AgentState{State: StateIdle, Outcome: a.Outcome, Attempt: a.Attempt}
	if a.Mode == "single_task" {
		to.State = StateFinished
	}
	_, err = s.changeState(ctx, a, to, &archiveCols{}) // the clock is cleared in the state change's transaction
	return err
}

// DeleteRequest is the Delete input. Fingerprint confirms the unsaved work
// a previous unsaved_work error listed.
type DeleteRequest struct {
	Envelope
	AgentID     string
	Cascade     bool
	Fingerprint string
}

// Delete checks children and unsaved work, then stops the agent, purges only
// its recorded native sessions, removes its working copy (keeping the branch)
// and tombstones the row (design v2 §4.8). Each step is safe to repeat; a
// failure leaves the row stopping with its delete flag, and the reconcile
// queue retries it.
func (s *Service) Delete(ctx context.Context, req DeleteRequest) error {
	if err := s.waitReady(ctx); err != nil {
		return err
	}
	err := s.delete(ctx, req)
	if err != nil {
		s.retryIfOwed(ctx, req.AgentID)
	}
	return err
}

// retryIfOwed queues agentID for its retry when a failed Delete left it
// with a marker; it reads the row without ctx's cancel.
func (s *Service) retryIfOwed(ctx context.Context, agentID string) {
	if a, err := s.store.GetAgent(context.WithoutCancel(ctx), agentID); err == nil && owes(a, false) {
		s.retryLater(agentID)
	}
}

// delete is Delete without the start-up gate; Reconcile finishes a Delete with it.
// A cascade deletes the children of a live agent first, each under its own
// lock only.
func (s *Service) delete(ctx context.Context, req DeleteRequest) error {
	if req.Cascade {
		a, err := s.agent(ctx, req.AgentID)
		if err != nil || a.DeletedAt != nil {
			return err
		}
		if err := s.deleteChildren(ctx, req.AgentID, true); err != nil {
			return err
		}
	}
	defer s.lock(req.AgentID)()
	a, err := s.agent(ctx, req.AgentID)
	if err != nil || a.DeletedAt != nil {
		return err
	}
	if err := s.deleteChildren(ctx, a.AgentID, false); err != nil {
		return err
	}
	spec, err := s.checkUnsaved(ctx, a, req.Fingerprint)
	if err != nil {
		return err
	}
	if err := s.store.MarkDeleteRequested(ctx, a.AgentID); err != nil {
		return err
	}
	if a.State != StateArchived {
		if a, err = s.stop(ctx, a, nil); err != nil {
			return err
		}
	}
	if s.purge != nil {
		owned, err := s.store.NativeSessions(ctx, a.AgentID)
		if err != nil {
			return err
		}
		if err := s.purge(ctx, a, owned); err != nil {
			return err
		}
	}
	if spec != nil {
		spec.Confirm = req.Fingerprint
		if err := s.workspace.Remove(ctx, *spec); err != nil {
			return err
		}
	}
	if err := s.retireLaunch(ctx, a); err != nil { // before the tombstone, so a failure is retried
		return err
	}
	return s.tombstone(ctx, a)
}

// tombstone marks a deleted and purges its history in one transaction under
// the event lane, then publishes its settled and agent.deleted events, live
// only, as the history is gone.
func (s *Service) tombstone(ctx context.Context, a loomstore.Agent) error {
	now, after := time.Now(), a
	after.DeletedAt = sp(loomstore.Stamp(now))
	out := append(changeEvents(a, after), Event{AgentID: a.AgentID, Type: EventDeleted, Time: now})
	for i := range out {
		out[i].EventID = a.AgentID + ":deleted:" + out[i].Type
	}
	rows, err := eventRows(out)
	if err != nil {
		return err
	}
	_, err = s.events.commit(func() ([]loomstore.Event, error) {
		return s.store.TombstoneEvents(ctx, a.AgentID, now, rows)
	}, s.busPublish(out))
	if err == nil {
		s.forgetCalls(a.AgentID)
	}
	return err
}

// deleteChildren refuses with children_live while a child is not settled,
// or with cascade deletes every child by the same rules.
func (s *Service) deleteChildren(ctx context.Context, agentID string, cascade bool) error {
	children, _, err := s.store.ListAgents(ctx, loomstore.AgentFilter{Parent: agentID, IncludeArchived: true})
	if err != nil {
		return err
	}
	for _, c := range children {
		if settledReason(c) == "" && !cascade {
			return &Error{Code: CodeChildrenLive, Message: c.AgentID + " is not settled"}
		}
	}
	for _, c := range children {
		if cascade {
			if err := s.delete(ctx, DeleteRequest{AgentID: c.AgentID, Cascade: true}); err != nil {
				s.retryIfOwed(ctx, c.AgentID)
				return err
			}
		}
	}
	return nil
}

// checkUnsaved asks Workspace.Status for a's uncommitted paths and fails with
// unsaved_work unless fingerprint matches them. It returns a's working copy,
// or nil when a has none.
func (s *Service) checkUnsaved(ctx context.Context, a loomstore.Agent, fingerprint string) (*WorkspaceSpec, error) {
	if a.WorktreePath == nil || s.workspace == nil {
		return nil, nil
	}
	repo, err := s.repoPath(ctx, a.Repo)
	if err != nil {
		return nil, err
	}
	spec := &WorkspaceSpec{Key: a.AgentID, Repo: repo, BaseRef: deref(a.BaseRef), Branch: deref(a.Branch), Detached: a.Branch == nil}
	st, err := s.workspace.Status(ctx, *spec)
	if err != nil {
		return nil, err
	}
	if len(st.Uncommitted) > 0 && st.Fingerprint != fingerprint {
		return nil, &Error{Code: CodeUnsavedWork, Message: "uncommitted changes in " + *a.WorktreePath,
			Paths: st.Uncommitted, Fingerprint: st.Fingerprint}
	}
	return spec, nil
}

// stop marks a stopping, recording arch when set in the same transaction,
// interrupts its running turn and withdraws every waiting message.
func (s *Service) stop(ctx context.Context, a loomstore.Agent, arch *archiveCols) (loomstore.Agent, error) {
	var err error
	if a.State != StateStopping {
		to := a.StateOf()
		to.State, to.WaitingOn, to.AttentionReason = StateStopping, nil, nil
		if a, err = s.changeState(ctx, a, to, arch); err != nil {
			return a, err
		}
	}
	if a.RunningTurnID != nil {
		if err := s.interrupt(ctx, a); err != nil {
			return a, err
		}
	}
	slots, err := s.store.Slots(ctx, a.AgentID)
	if err != nil {
		return a, err
	}
	for _, sl := range slots {
		if sl.State != loomstore.SlotWaiting {
			continue
		}
		res, err := s.store.ClearSlot(ctx, a.AgentID, sl.Sender, false)
		if err != nil {
			return a, err
		}
		if res == loomstore.Withdrawn {
			if err := s.emit(ctx, Event{AgentID: a.AgentID, Type: EventWithdrawn, Reason: sl.Sender, Time: time.Now()}); err != nil {
				return a, err
			}
		}
	}
	return a, nil
}

// live reads agentID's row; a deleted agent is agent_not_found.
func (s *Service) live(ctx context.Context, agentID string) (loomstore.Agent, error) {
	a, err := s.agent(ctx, agentID)
	if err == nil && a.DeletedAt != nil {
		err = &Error{Code: CodeAgentNotFound, Message: agentID + " is deleted"}
	}
	return a, err
}

func sp(s string) *string { return &s }
