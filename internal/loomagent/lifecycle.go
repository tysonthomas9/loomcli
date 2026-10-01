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
	defer s.lock(req.AgentID)()
	a, err := s.live(ctx, req.AgentID)
	if err != nil {
		return err
	}
	if a.State == StateArchived {
		return s.finishArchive(ctx, a, deref(a.ArchiveReason))
	}
	if req.Reason == ArchiveCancelled {
		if a, err = s.stop(ctx, a); err != nil {
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
		if err := s.store.SetArchive(ctx, a.AgentID, sp(req.Reason), nil); err != nil {
			return err
		}
		to := a.StateOf()
		to.State = StateStopping
		_, err = s.setState(ctx, a, to)
		return err
	case a.State == StateStopping:
		return nil
	}
	return s.finishArchive(ctx, a, req.Reason)
}

// finishArchive moves a through stopping to archived and starts the R29
// clock. It keeps a's outcome.
func (s *Service) finishArchive(ctx context.Context, a loomstore.Agent, reason string) error {
	var err error
	if a.State != StateArchived {
		if a.State != StateStopping {
			if a, err = s.setState(ctx, a, loomstore.AgentState{State: StateStopping, Outcome: a.Outcome, Attempt: a.Attempt}); err != nil {
				return err
			}
		}
		if a, err = s.setState(ctx, a, loomstore.AgentState{State: StateArchived, Outcome: a.Outcome, Attempt: a.Attempt}); err != nil {
			return err
		}
		s.Bus.publish(Event{AgentID: a.AgentID, Type: EventArchived, Reason: reason, Time: time.Now()})
	}
	now := time.Now()
	return s.store.SetArchive(ctx, a.AgentID, sp(reason), &now)
}

// Unarchive returns a persistent agent to idle and a single task to finished,
// canceling the R29 clock. History already purged fails with history_expired.
func (s *Service) Unarchive(ctx context.Context, req ArchiveRequest) error {
	defer s.lock(req.AgentID)()
	a, err := s.live(ctx, req.AgentID)
	if err != nil {
		return err
	}
	if a.HistoryPurgedAt != nil {
		return &Error{Code: CodeHistoryExpired, Message: a.AgentID}
	}
	if err := s.store.SetArchive(ctx, a.AgentID, nil, nil); err != nil || a.State != StateArchived {
		return err
	}
	to := loomstore.AgentState{State: StateIdle, Outcome: a.Outcome, Attempt: a.Attempt}
	if a.Mode == "single_task" {
		to.State = StateFinished
	}
	_, err = s.setState(ctx, a, to)
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
// failure leaves the row stopping with its delete flag for Reconcile.
func (s *Service) Delete(ctx context.Context, req DeleteRequest) error {
	defer s.lock(req.AgentID)()
	a, err := s.agent(ctx, req.AgentID)
	if err != nil || a.DeletedAt != nil {
		return err
	}
	if err := s.deleteChildren(ctx, a.AgentID, req.Cascade); err != nil {
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
		if a, err = s.stop(ctx, a); err != nil {
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
		if err := s.workspace.Remove(ctx, *spec); err != nil {
			return err
		}
	}
	now := time.Now()
	if err := s.store.Tombstone(ctx, a.AgentID, now); err != nil {
		return err
	}
	before := a
	a.DeletedAt = sp(loomstore.Stamp(now))
	s.publishChange(before, a)
	s.Bus.publish(Event{AgentID: a.AgentID, Type: EventDeleted, Time: now})
	return nil
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
			if err := s.Delete(ctx, DeleteRequest{AgentID: c.AgentID, Cascade: true}); err != nil {
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

// stop marks a stopping, interrupts its running turn and withdraws every
// waiting message.
func (s *Service) stop(ctx context.Context, a loomstore.Agent) (loomstore.Agent, error) {
	var err error
	if a.State != StateStopping {
		to := a.StateOf()
		to.State, to.WaitingOn, to.AttentionReason = StateStopping, nil, nil
		if a, err = s.setState(ctx, a, to); err != nil {
			return a, err
		}
	}
	if a.RunningTurnID != nil && s.interrupt != nil {
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
		res, err := s.store.ClearSlot(ctx, a.AgentID, sl.Sender)
		if err != nil {
			return a, err
		}
		if res == loomstore.Withdrawn {
			s.Bus.publish(Event{AgentID: a.AgentID, Type: EventWithdrawn, Reason: sl.Sender, Time: time.Now()})
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
