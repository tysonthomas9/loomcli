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

// RetentionSweep is the daily R29 sweep of s's agents at now. For each agent
// due (interactive archived, or background finished, HistoryRetention ago,
// or deleted), under its lock, it re-checks the deadline, removes a clean
// working copy (keeping the branch; a dirty one is reported and kept),
// purges exactly the agent's recorded native sessions, then marks its
// history purged. A failed purge leaves the agent due, visible as an
// incomplete expiry, and the next sweep retries it.
func (s *Service) RetentionSweep(ctx context.Context, now time.Time) {
	if err := s.waitReady(ctx); err != nil {
		return
	}
	ids, err := s.store.RetentionDue(ctx, now)
	if err != nil {
		slog.Warn("loomagent: retention sweep", "error", err)
		return
	}
	for _, id := range ids {
		if err := s.expire(ctx, id, now); err != nil {
			slog.Warn("loomagent: history expiry incomplete", "agent", id, "error", err)
		}
	}
}

// expire runs the sweep for one agent; an agent of another workspace, or
// no longer due (unarchived or sent to meanwhile), is left alone.
func (s *Service) expire(ctx context.Context, id string, now time.Time) error {
	defer s.lock(id)()
	a, err := s.store.GetAgent(ctx, id)
	if err != nil || a.WorkspaceID != s.workspaceID {
		return err
	}
	if due, err := s.store.Due(ctx, id, now); err != nil || !due {
		return err
	}
	if a.DeletedAt == nil {
		s.removeWorkingCopy(ctx, a)
	}
	owned, err := s.store.NativeSessions(ctx, id)
	if err != nil {
		return err
	}
	if err := s.purge(ctx, a, owned); err != nil {
		return err
	}
	if err := s.store.MarkHistoryPurged(ctx, id, now); err != nil && !errors.Is(err, loomstore.ErrNotDue) {
		return err
	}
	return nil
}

// removeWorkingCopy removes a's working copy through the Workspace port when
// it has no uncommitted work; otherwise it reports and keeps it.
func (s *Service) removeWorkingCopy(ctx context.Context, a loomstore.Agent) {
	spec, err := s.checkUnsaved(ctx, a, "")
	if err == nil && spec != nil {
		err = s.workspace.Remove(ctx, *spec)
	}
	if err != nil {
		slog.Warn("loomagent: working copy kept", "agent", a.AgentID, "error", err)
	}
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
