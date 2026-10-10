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
// unless that ref exists. Turn N is a's TurnsEnded, which Loom counts in the
// same transaction as each turn end it decides (0 before any), so a crash at
// any point leaves the ref owed until a capture makes it, once. No turn may run: dispatch
// captures before every hand-over, which gates the next turn on the ref. An
// archived agent keeps its working copy, so it still captures a ref owed;
// one with no working copy, or being deleted, owes none.
func (s *Service) checkpoint(ctx context.Context, a loomstore.Agent) error {
	if a.RunningTurnID != nil || a.WorktreePath == nil || a.DeleteRequested {
		return nil
	}
	cur, err := s.store.GetAgent(ctx, a.AgentID) // turns_ended as committed
	if err != nil {
		return checkpointFailed{err}
	}
	repo, err := s.repoPath(ctx, a.Repo)
	if err != nil {
		return checkpointFailed{err}
	}
	err = s.workspace.Checkpoint(ctx, WorkspaceSpec{Key: a.AgentID, Repo: repo, BaseRef: deref(a.BaseRef),
		Branch: deref(a.Branch), Detached: a.Branch == nil}, checkpointRef(a.AgentID, int(cur.TurnsEnded)))
	if err != nil {
		return checkpointFailed{err}
	}
	return nil
}

// TurnDiff is the change agentID's turn n made: its working copy from the
// ref of turn n-1 (turn/0, the baseline, for turn 1) to the ref of turn n.
// A turn with no ref on either side is turn_not_found; a purged history is
// history_expired.
func (s *Service) TurnDiff(ctx context.Context, agentID string, n int) (CheckpointDiff, error) {
	a, err := s.agent(ctx, agentID)
	if err != nil {
		return CheckpointDiff{}, err
	}
	if a.HistoryPurgedAt != nil {
		return CheckpointDiff{}, &Error{Code: CodeHistoryExpired, Message: agentID + " history was purged"}
	}
	notFound := &Error{Code: CodeTurnNotFound, Message: fmt.Sprintf("%s has no checkpoint for turn %d", agentID, n)}
	if n < 1 || s.workspace == nil || a.Repo == "" {
		return CheckpointDiff{}, notFound
	}
	repo, err := s.repoPath(ctx, a.Repo)
	if err != nil {
		return CheckpointDiff{}, err
	}
	d, err := s.workspace.CheckpointDiff(ctx, repo, checkpointRef(agentID, n-1), checkpointRef(agentID, n))
	if errors.Is(err, ErrNoCheckpoint) {
		return CheckpointDiff{}, notFound
	}
	return d, err
}

// dropCheckpoints deletes every checkpoint ref of a, for Delete and the
// history purge. A repo the resolver refuses, such as a removed clone, is
// dropped at its recorded path, where the Workspace finds nothing to drop.
func (s *Service) dropCheckpoints(ctx context.Context, a loomstore.Agent) error {
	if s.workspace == nil || a.Repo == "" {
		return nil
	}
	repo, err := s.repoPath(ctx, a.Repo)
	if err != nil {
		repo = a.Repo
	}
	return s.workspace.DropCheckpoints(ctx, repo, "refs/loom/checkpoints/"+a.AgentID+"/")
}

func isCheckpointFailed(err error) bool {
	var c checkpointFailed
	return errors.As(err, &c)
}
