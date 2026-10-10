package loomagent

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/loomharness"
	"github.com/tysonthomas9/loomcli/internal/loomstore"
)

// Usage-limit auto-resume (OR7), opt-in per workspace and off by default.
// When a persistent agent's turn ends on a usage limit (any harness's
// Failure class usage_limit), the dispatcher sends it "Continue where you
// left off." from loom:limit-resume after the next delay of
// limitResumeSchedule. A resumed turn that hits the limit again waits the
// next delay; after the last no resume is sent, and the agent stays idle
// showing the failure. Any other turn end, and any other Send, ends the
// episode. Cost policy: at most len(limitResumeSchedule) one-line messages
// per episode, none unless the workspace opted in. No harness reports when
// its limit resets yet (OR9); once one does, a resume is due at the later of
// the schedule and that reset.
var limitResumeSchedule = []time.Duration{15 * time.Minute, 30 * time.Minute, time.Hour, time.Hour, time.Hour, 2 * time.Hour}

// limitResumeText is a resume's message.
const limitResumeText = "Continue where you left off."

// limitResumeActor is a resume's sender.
var limitResumeActor = ActorRef{Kind: "loom", ID: "limit-resume"}

var limitResumeSender = senderOf(limitResumeActor)

// LimitResume reports whether this workspace opted in to usage-limit auto-resume.
func (s *Service) LimitResume(ctx context.Context) (bool, error) {
	return s.store.LimitResumeOn(ctx, s.workspaceID)
}

// SetLimitResume sets this workspace's usage-limit auto-resume opt-in.
func (s *Service) SetLimitResume(ctx context.Context, on bool) error {
	return s.store.SetLimitResumeOn(ctx, s.workspaceID, on)
}

// limitTurnEnded records what a's ended turn e owes under the agent lock,
// before the turn's end commits, so a crash in between replays the same
// write: a persistent agent's turn that failed on a usage limit owes a
// resume after the next delay (when the workspace opted in and the cap is
// not reached); any other end owes none.
func (s *Service) limitTurnEnded(ctx context.Context, a loomstore.Agent, e loomharness.Event) error {
	if e.Failure == nil || e.Failure.Class != loomharness.FailureUsageLimit || a.Mode == "single_task" {
		return s.store.DropLimitResume(ctx, a.AgentID)
	}
	on, err := s.store.LimitResumeOn(ctx, s.workspaceID)
	if err != nil || !on {
		return errors.Join(err, s.store.DropLimitResume(ctx, a.AgentID))
	}
	attempt := int64(1)
	prev, err := s.store.GetLimitResume(ctx, a.AgentID)
	switch {
	case err == nil && prev.TurnID == e.TurnID: // a replay of this end
		return nil
	case err == nil && prev.DueAt == "": // this turn was the resume
		attempt = prev.Attempt + 1
	case err != nil && !errors.Is(err, loomstore.ErrNotFound):
		return err
	}
	if attempt > int64(len(limitResumeSchedule)) {
		return s.store.DropLimitResume(ctx, a.AgentID)
	}
	return s.store.PutLimitResume(ctx, loomstore.LimitResume{AgentID: a.AgentID, TurnID: e.TurnID, Attempt: attempt,
		Session: deref(a.HarnessSessionID), DueAt: loomstore.Stamp(s.now().Add(limitResumeSchedule[attempt-1]))})
}

// sweepLimitResumes sends every usage-limit resume that is due. The
// dispatcher runs it on the resync clock.
func (s *Service) sweepLimitResumes(ctx context.Context) {
	due, err := s.store.DueLimitResumes(ctx, s.workspaceID, s.now())
	for _, r := range due {
		err = errors.Join(err, s.limitResume(ctx, r.AgentID))
	}
	if err != nil {
		slog.Warn("loomagent: usage-limit resume", "error", err)
	}
}

// limitResume sends agentID's due resume under its lock, after checking
// again: the resume is still owed and due, the workspace still opts in, and
// the agent is a persistent one not archived, stopping or deleted, still on
// the session whose turn hit the limit (else the resume is dropped) with no turn running or message waiting (else that
// turn's end decides). The Send marks the resume sent in its own
// transaction, and checks again there that it is owed and opted in; its
// request ID names the agent, turn and attempt, so a retry is answered by
// its receipt.
func (s *Service) limitResume(ctx context.Context, agentID string) error {
	defer s.lock(agentID)()
	r, err := s.store.GetLimitResume(ctx, agentID)
	if errors.Is(err, loomstore.ErrNotFound) || (err == nil && (r.DueAt == "" || r.DueAt > loomstore.Stamp(s.now()))) {
		return nil
	} else if err != nil {
		return err
	}
	on, err := s.store.LimitResumeOn(ctx, s.workspaceID)
	if err != nil {
		return err
	}
	a, err := s.store.GetAgent(ctx, agentID)
	if errors.Is(err, loomstore.ErrNotFound) || (err == nil && (!on || a.DeletedAt != nil || a.Mode == "single_task" ||
		a.State == StateArchived || a.State == StateStopping || deref(a.HarnessSessionID) != r.Session)) {
		return s.store.DropLimitResume(ctx, agentID)
	} else if err != nil {
		return err
	}
	slots, err := s.store.Slots(ctx, agentID)
	if err != nil || a.RunningTurnID != nil || a.State == StateCreating || pending(slots) {
		return err
	}
	_, err = s.sendLocked(ctx, SendRequest{Envelope: Envelope{RequestID: "limit-resume:" + agentID + ":" + r.TurnID + ":" +
		strconv.FormatInt(r.Attempt, 10)}, AgentID: agentID, Text: limitResumeText, Source: "system", Actor: limitResumeActor})
	if errors.Is(err, loomstore.ErrLimitResumeGone) { // opted out, or resumed, since the checks above
		return nil
	}
	return err
}

// pending reports whether a slot waits or is handed over.
func pending(slots []loomstore.Slot) bool {
	for _, sl := range slots {
		if sl.State == loomstore.SlotWaiting || sl.State == loomstore.SlotHanded {
			return true
		}
	}
	return false
}
