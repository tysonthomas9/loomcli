package loomagent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// RetentionSweep is the daily R29 sweep of s's agents at now. Working copies
// and history share one deadline (interactive archived, or background
// finished, HistoryRetention ago) but are swept independently, each under
// the agent's lock after re-checking it is still due:
//   - a clean working copy is removed through the Workspace port, keeping its
//     branch; a dirty one is reported and kept, and retried each sweep;
//   - for history (also due once deleted), exactly the agent's recorded
//     native sessions are purged, then the history is marked purged. A failed
//     purge is recorded (history_purge_failed_at, an incomplete expiry) and
//     leaves the agent due, so the next sweep retries it.
func (s *Service) RetentionSweep(ctx context.Context, now time.Time) {
	if err := s.waitReady(ctx); err != nil {
		return
	}
	for _, step := range []struct {
		what string
		due  func(context.Context, time.Time) ([]string, error)
		run  func(context.Context, string, time.Time) error
	}{{"working copy kept", s.store.WorktreesDue, s.removeWorkingCopy}, {"history expiry incomplete", s.store.RetentionDue, s.expire}} {
		ids, err := step.due(ctx, now)
		if err != nil {
			slog.Warn("loomagent: retention sweep", "error", err)
			return
		}
		for _, id := range ids {
			if err := step.run(ctx, id, now); err != nil {
				slog.Warn("loomagent: "+step.what, "agent", id, "error", err)
			}
		}
	}
}

// mine reads id under its lock when it is s's and due by due at now; ok is
// false when it is another workspace's or no longer due (unarchived or sent
// to meanwhile).
func (s *Service) mine(ctx context.Context, id string, now time.Time,
	due func(context.Context, string, time.Time) (bool, error)) (a loomstore.Agent, ok bool, err error) {
	if a, err = s.store.GetAgent(ctx, id); err != nil || a.WorkspaceID != s.workspaceID {
		return a, false, err
	}
	ok, err = due(ctx, id, now)
	return a, ok, err
}

// expire purges id's recorded native sessions and checkpoint refs, then
// marks its history purged.
func (s *Service) expire(ctx context.Context, id string, now time.Time) error {
	defer s.lock(id)()
	a, ok, err := s.mine(ctx, id, now, s.store.Due)
	if err != nil || !ok {
		return err
	}
	owned, err := s.store.NativeSessions(ctx, id)
	if err != nil {
		return err
	}
	if err := errors.Join(s.purge(ctx, a, owned), s.dropCheckpoints(ctx, a)); err != nil {
		return errors.Join(err, s.store.MarkPurgeFailed(ctx, id, now))
	}
	if err := s.store.MarkHistoryPurged(ctx, id, now); err != nil && !errors.Is(err, loomstore.ErrNotDue) {
		return err
	}
	return nil
}

// removeWorkingCopy removes id's working copy through the Workspace port
// when it has no uncommitted work (else unsaved_work), and records it gone.
func (s *Service) removeWorkingCopy(ctx context.Context, id string, now time.Time) error {
	defer s.lock(id)()
	a, ok, err := s.mine(ctx, id, now, s.store.WorktreeDue)
	if err != nil || !ok {
		return err
	}
	spec, err := s.checkUnsaved(ctx, a, "")
	if err != nil || spec == nil {
		return err
	}
	if err := s.workspace.Remove(ctx, *spec); err != nil {
		return err
	}
	return s.store.ClearWorktree(ctx, id)
}

// purgeOwned deletes exactly a's recorded native sessions, each through its
// recorded harness under its recorded root, never re-resolved. A session
// whose owner is not a, or whose harness is not wired, blocks every delete.
func (s *Service) purgeOwned(ctx context.Context, a loomstore.Agent, owned []loomstore.NativeSession) error {
	refs := map[string][]loomharness.NativeRef{}
	for _, n := range owned {
		owner, err := s.store.NativeSessionOwner(ctx, n.Harness, n.NativeRoot, n.NativeID)
		if err != nil || owner != a.AgentID || n.NativeID == "" {
			return fmt.Errorf("loomagent: owner of %s %s unproven: %q %v", n.Harness, n.NativeID, owner, err)
		}
		if s.harnesses[n.Harness] == nil {
			return fmt.Errorf("loomagent: %s is not available to purge %s", n.Harness, n.NativeID)
		}
		refs[n.Harness] = append(refs[n.Harness], loomharness.NativeRef{Root: n.NativeRoot, NativeID: n.NativeID})
	}
	for name, r := range refs {
		if err := s.harnesses[name].Purge(ctx, r); err != nil {
			return err
		}
	}
	return nil
}
