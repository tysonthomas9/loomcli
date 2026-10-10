package loomagent

import (
	"context"
	"errors"
	"fmt"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// checkpointRef is the ref that holds agentID's working copy as its turn n
// ended; turn/0 is the baseline, captured as Create ends (its wake
// dispatches), before the first hand-over.
func checkpointRef(agentID string, n int) string {
	return fmt.Sprintf("refs/loom/checkpoints/%s/turn/%d", agentID, n)
}

// checkpointFailed is a capture that failed; the reconcile queue retries it
// and the next hand-over waits for it.
type checkpointFailed struct{ error }

func (c checkpointFailed) Unwrap() error { return c.error }

// checkpoint captures a's working copy at the ref of its last ended turn,
// unless that ref exists. Turn N is the count of a's saved
// agent.turn_completed rows (0 before any), so a crash at any point leaves
// the ref owed until a capture makes it, once. No turn may run: dispatch
// captures before every hand-over, which gates the next turn on the ref. An
// archived agent keeps its working copy, so it still captures a ref owed;
// one with no working copy, or being deleted, owes none.
func (s *Service) checkpoint(ctx context.Context, a loomstore.Agent) error {
	if a.RunningTurnID != nil || a.WorktreePath == nil || a.DeleteRequested {
		return nil
	}
	n, err := s.store.CountEvents(ctx, a.AgentID, EventTurnCompleted)
	if err != nil {
		return checkpointFailed{err}
	}
	repo, err := s.repoPath(ctx, a.Repo)
	if err != nil {
		return checkpointFailed{err}
	}
	err = s.workspace.Checkpoint(ctx, WorkspaceSpec{Key: a.AgentID, Repo: repo, BaseRef: deref(a.BaseRef),
		Branch: deref(a.Branch), Detached: a.Branch == nil}, checkpointRef(a.AgentID, n))
	if err != nil {
		return checkpointFailed{err}
	}
	return nil
}

func isCheckpointFailed(err error) bool {
	var c checkpointFailed
	return errors.As(err, &c)
}

// stoppedEnd saves the end of a's running turn, which a stop just
// interrupted, so it counts as ended before a moves on (saveTurnEnd).
func (s *Service) stoppedEnd(ctx context.Context, a loomstore.Agent) error {
	if a.RunningTurnID == nil {
		return nil
	}
	sess, ref, err := s.current(ctx, a)
	if err != nil {
		return err
	}
	if sess == nil { // an unwired harness: its recorded session
		ref = loomharness.NativeRef{Root: deref(a.HarnessSessionRoot), NativeID: deref(a.HarnessSessionID)}
	}
	return s.saveTurnEnd(ctx, a.AgentID, sess, ref, *a.RunningTurnID)
}

// saveTurnEnd saves the end of running (a turn ID, or an input key until
// turn.started names the turn), which no longer runs on sess at ref: its
// native end if the history has one (sess, nil for an unwired harness),
// else a cancelled end with the EventID
// the native one would have, so a late native end adds no row.
func (s *Service) saveTurnEnd(ctx context.Context, agentID string, sess loomharness.Session, ref loomharness.NativeRef, running string) error {
	var end *loomharness.Event
	var err error
	if sess != nil {
		end, err = nativeEnd(ctx, sess, running)
	}
	if err != nil || end == nil { // an unread history: still count the turn
		end = &loomharness.Event{Type: loomharness.EventTurnCompleted, TurnID: running, StopReason: "cancelled"}
	}
	end.Session = ref // the EventID the feed would give it
	_, err = s.events.Append(ctx, nativeRow(agentID, EventTurnCompleted, *end))
	return err
}
