package loomagent

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomstore"
	"github.com/tysonthomas9/loomcli/internal/prwatch"
)

// PR-watch wakes (OR8). Every prWatchInterval the dispatcher reads each PR
// an agent watches through the host GitHub connector, and tells the agent
// what changed since its watch last told it (prwatch.Decide) with a Send
// from loom:pr-watch. The Send's transaction saves the watch's new cursor
// with its receipt, and its request ID names the cursors it moves between,
// so a crash or restart never tells the same news twice. A failed read
// tells nothing and moves nothing. A merged PR ends its watch quietly.
var prWatchInterval = 2 * time.Minute // checks take minutes; a faster pass mostly spends the rate limit

var prWatchActor = ActorRef{Kind: "loom", ID: "pr-watch"}

var prWatchSender = senderOf(prWatchActor)

// sweepPRWatches runs every PR watch of the workspace when one is due. The
// dispatcher runs it on the resync clock.
func (s *Service) sweepPRWatches(ctx context.Context) {
	if s.prHost == nil || s.now().Before(s.prDue) {
		return
	}
	s.prDue = s.now().Add(prWatchInterval)
	err := s.store.DropDeletedPRWatches(ctx, s.workspaceID)
	watches, lerr := s.store.PRWatches(ctx, s.workspaceID)
	err = errors.Join(err, lerr)
	for _, w := range watches {
		err = errors.Join(err, s.prWatch(ctx, w))
	}
	if err != nil {
		slog.Warn("loomagent: PR watch", "error", err)
	}
}

// prWatch reads w's PR, then under its agent's lock tells the agent any
// news. It waits, telling nothing, while the agent can't take a message
// or its previous wake is not yet handed over; the next sweep then tells
// all the news since.
func (s *Service) prWatch(ctx context.Context, w loomstore.PRWatch) error {
	snap, err := prwatch.Observe(ctx, s.prHost, s.workspaceID, w.Owner, w.Repo, w.Number)
	if err != nil {
		return err
	}
	defer s.lock(w.AgentID)()
	if snap.Merged {
		_, err := s.store.UnregisterPRWatch(ctx, w.PRWatchKey)
		return err
	}
	wake, ok := prwatch.Decide(w, snap)
	if !ok {
		return nil
	}
	a, err := s.store.GetAgent(ctx, w.AgentID)
	if err != nil || a.DeletedAt != nil || a.State == StateFinished || sendable(a) != nil {
		return err // a finished single task is not reopened by a watch
	}
	slots, err := s.store.Slots(ctx, w.AgentID)
	if err != nil || slices.ContainsFunc(slots, func(sl loomstore.Slot) bool {
		return sl.Sender == prWatchSender && (sl.State == loomstore.SlotWaiting || sl.State == loomstore.SlotHanded)
	}) {
		return err
	}
	_, err = s.sendLocked(ctx, SendRequest{Envelope: Envelope{RequestID: wake.RequestID}, AgentID: w.AgentID,
		Text: wake.Text, Source: "system", Actor: prWatchActor, prWatch: &wake.Change})
	if errors.Is(err, loomstore.ErrPRWatchGone) { // unwatched since the read
		return nil
	}
	return err
}
