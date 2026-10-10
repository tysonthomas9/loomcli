package loomagent

import (
	"context"
	"errors"
	"fmt"

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
// agent with no working copy, archived or being deleted, owes none.
func (s *Service) checkpoint(ctx context.Context, a loomstore.Agent) error {
	if a.RunningTurnID != nil || a.WorktreePath == nil || a.State == StateArchived || a.DeleteRequested {
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
