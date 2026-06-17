package driver

// Await deadline sweeper (ARCHITECTURE-PROPOSAL §7 step 8, chunk AW8).
//
// AwaitTimeoutSweeper is the server-side RULE 5 enforcement loop: every await
// carries a mandatory deadline (validated at registration, indexed by the
// stores' deadline feed), and this sweeper guarantees a past-deadline
// instance is freed and its run reaches a terminal arm in bounded time. Per
// due instance it emits a synthetic timeout event — deterministic ID
// domain.AwaitTimeoutEventID(instanceKey), actor domain.AwaitTimeoutActor,
// subject key exactly the original awaited pattern — through the normal
// dispatch-time matcher (AW7), which resolves the row as timed_out
// (resume-with-timeout-event decision: stores classify the
// "await-timeout-" event ID) and re-queues the run.
//
// The run is NEVER terminalized by the sweeper itself: it resumes on its
// timeout arm with the timeout payload ({timeout:true, the
// "{patternType}.timeout" event type, instanceKey, deadline}) plus the
// replayed timed_out row, and the workflow's arm decides the terminal outcome
// (needs_review/suspended per RULE 5's suspended arm; agent-flows A2 shape —
// direct terminalization was considered and rejected). RULE 3 holds end to
// end: the synthetic event targets exactly one instanceKey, and the matcher
// skips every co-waiter sharing the pattern — a timeout never resolves a
// pattern broadly. RULE 4 holds via the matcher's sweeper-lane carve-out:
// system:timeout is allowed for its own instance only, never as a general
// system bypass.
//
// Idempotency: a sweep races real events safely. An instance satisfied
// between the deadline scan and the dispatch is a recorded no-op (the
// matcher finds no pending candidate, or ResolveAwait replays Resume=false);
// repeated RunOnce passes emit nothing for an already-timed-out instance
// because it left the pending deadline feed. A backlog (sweeper down for an
// hour) is drained page by page — instances resolve late, never get missed.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
)

// DefaultAwaitTimeoutSweepBatch caps each per-workspace
// ListDueAwaitDeadlines page when no BatchLimit is configured (env knob
// LOOM_AWAIT_SWEEP_BATCH in loom serve).
const DefaultAwaitTimeoutSweepBatch = 50

// maxAwaitTimeoutSweepPasses bounds the per-workspace backlog drain loop in
// one RunOnce — defense in depth against a store that keeps returning rows
// the sweep cannot retire; the next tick continues where this one stopped.
const maxAwaitTimeoutSweepPasses = 100

// AwaitTimeoutSweeper scans past-deadline await instances and resumes their
// runs with a synthetic timeout event. Follow the StaleTaskSweeper shape:
// Store plus zero values is ready; loom serve drives RunOnce on a ticker.
type AwaitTimeoutSweeper struct {
	Store store.Store
	// WorkspaceKey scopes the sweep to one workspace. Empty sweeps every
	// workspace returned by Store.Workspaces().List.
	WorkspaceKey string
	// BatchLimit caps each ListDueAwaitDeadlines page; zero or negative
	// selects DefaultAwaitTimeoutSweepBatch.
	BatchLimit int
	// Logger feeds the dispatch matcher's audit records; slog.Default when
	// nil.
	Logger *slog.Logger
	// Now is a clock seam for tests; nil uses time.Now (UTC).
	Now func() time.Time
	// ResumeRetries / ResumeRetryDelay pass through to the matcher's
	// pending->suspend-window retry budget (tests).
	ResumeRetries    int
	ResumeRetryDelay time.Duration
}

// AwaitTimeoutSweepResult aggregates one RunOnce pass.
type AwaitTimeoutSweepResult struct {
	// TimedOut counts instances this pass resolved timed_out AND whose runs
	// it re-queued onto their timeout arm.
	TimedOut int
	// AlreadySatisfied counts due instances a real event won between the
	// deadline scan and the timeout dispatch — the recorded no-op losers.
	AlreadySatisfied int
	// ResumeDeferred counts instances resolved timed_out whose run
	// transition was owned elsewhere (resume race, pending->suspend window,
	// terminal run).
	ResumeDeferred int
	// Failed counts instances whose sweep errored; they stay in the deadline
	// feed for the next tick.
	Failed               int
	TimedOutInstanceKeys []string
}

// RunOnce performs a single sweep: list due await deadlines in each target
// workspace and emit each instance's timeout event through the matcher.
// Per-instance failures are joined, never abort the pass.
func (s *AwaitTimeoutSweeper) RunOnce(ctx context.Context) (*AwaitTimeoutSweepResult, error) {
	if s == nil || s.Store == nil {
		return nil, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	now := s.now()
	workspaces, err := s.workspaceKeys(ctx)
	if err != nil {
		return nil, err
	}
	out := &AwaitTimeoutSweepResult{}
	var errs []error
	for _, ws := range workspaces {
		if err := s.sweepWorkspace(ctx, ws, now, out); err != nil {
			errs = append(errs, err)
		}
	}
	return out, errors.Join(errs...)
}

// sweepWorkspace drains one workspace's due-deadline backlog page by page: a
// retired instance leaves the pending feed, so each pass lists the next
// slice. A short page means drained; a pass that made no progress would
// re-list the same failing instances forever, so it defers them to the next
// tick instead.
func (s *AwaitTimeoutSweeper) sweepWorkspace(ctx context.Context, ws string, now time.Time, out *AwaitTimeoutSweepResult) error {
	matcher := s.matcher()
	batch := s.batchLimit()
	var errs []error
	for pass := 0; pass < maxAwaitTimeoutSweepPasses; pass++ {
		due, err := s.Store.Awaits().ListDueAwaitDeadlines(ctx, ws, now, batch)
		if errors.Is(err, errors.ErrUnsupported) {
			return nil // backend without await support: structural no-op
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("list due await deadlines in workspace %q: %w", ws, err))
			break
		}
		progressed := 0
		for _, inst := range due {
			if inst == nil {
				continue
			}
			if err := s.sweepInstance(ctx, matcher, ws, inst, out); err != nil {
				errs = append(errs, err)
			} else {
				progressed++
			}
		}
		if len(due) < batch || progressed == 0 {
			break
		}
	}
	return errors.Join(errs...)
}

// sweepInstance emits one due instance's synthetic timeout event through the
// dispatch matcher and folds the per-instance record into the result.
func (s *AwaitTimeoutSweeper) sweepInstance(ctx context.Context, matcher *trigger.AwaitMatcher, ws string, inst *domain.AwaitInstance, out *AwaitTimeoutSweepResult) error {
	ev, err := awaitTimeoutDispatchEvent(inst)
	if err != nil {
		out.Failed++
		return err
	}
	res, err := matcher.Dispatch(ctx, ws, ev)
	if err != nil {
		out.Failed++
		return fmt.Errorf("await timeout sweep %q: %w", inst.InstanceKey, err)
	}
	record, found := awaitTimeoutRecord(res, inst.InstanceKey)
	switch {
	case !found:
		// The instance left the pending index between the deadline scan and
		// this dispatch: a real event won the race. The timeout emission is
		// the recorded no-op (idempotency; resolution untouched).
		out.AlreadySatisfied++
	case record.Outcome == trigger.AwaitMatchResolved:
		out.TimedOut++
		out.TimedOutInstanceKeys = append(out.TimedOutInstanceKeys, inst.InstanceKey)
	case record.Outcome == trigger.AwaitMatchAlreadyResolved:
		out.AlreadySatisfied++
	case record.Outcome == trigger.AwaitMatchResumeDeferred:
		// The row is timed_out; the run transition is owned elsewhere
		// (resume race, pending->suspend window, terminal run).
		out.ResumeDeferred++
	default:
		// actor_rejected here would mean the sweeper-lane carve-out broke.
		out.Failed++
		return fmt.Errorf("await timeout sweep %q: dispatch outcome %s (%s): %w",
			inst.InstanceKey, record.Outcome, record.Reason, domain.ErrInvalid)
	}
	return nil
}

// awaitTimeoutPayload is the camelCase resume payload a timed-out await
// resumes its run with: timeout=true plus the "{patternType}.timeout" event
// type, so the workflow's timeout arm can branch on the event type as well
// as on the replayed row's timed_out status.
type awaitTimeoutPayload struct {
	Timeout     bool      `json:"timeout"`
	EventType   string    `json:"eventType"`
	InstanceKey string    `json:"instanceKey"`
	Deadline    time.Time `json:"deadline"`
}

// awaitTimeoutDispatchEvent builds the synthetic timeout event for one due
// instance. The event's type/subject are the awaited pattern's own segments,
// so domain.AwaitEventKey re-renders exactly the registered pattern (subject
// key = the original awaited pattern); the ".timeout" suffix rides the
// payload and the timed_out row status, never the matching key.
func awaitTimeoutDispatchEvent(inst *domain.AwaitInstance) (trigger.AwaitDispatchEvent, error) {
	eventType, subjectRef, ok := strings.Cut(inst.Pattern, ":")
	if !ok || eventType == "" || subjectRef == "" {
		// Registration validated RULE 1, so this is a corrupted row; leave it
		// to operator attention rather than guessing a key.
		return trigger.AwaitDispatchEvent{}, fmt.Errorf("await timeout sweep: instance %q pattern %q: %w",
			inst.InstanceKey, inst.Pattern, domain.ErrAwaitPatternUnscoped)
	}
	payload, err := json.Marshal(awaitTimeoutPayload{
		Timeout:     true,
		EventType:   eventType + ".timeout",
		InstanceKey: inst.InstanceKey,
		Deadline:    inst.Deadline.UTC(),
	})
	if err != nil {
		return trigger.AwaitDispatchEvent{}, fmt.Errorf("encode await timeout payload for %q: %w", inst.InstanceKey, err)
	}
	return trigger.AwaitDispatchEvent{
		EventID:    domain.AwaitTimeoutEventID(inst.InstanceKey),
		EventType:  eventType,
		SubjectRef: subjectRef,
		ActorRef:   domain.AwaitTimeoutActor,
		Payload:    payload,
	}, nil
}

// awaitTimeoutRecord finds the dispatched instance's own match record (RULE 3
// filtering means it is the only one the matcher may produce).
func awaitTimeoutRecord(res *trigger.AwaitDispatchResult, instanceKey string) (trigger.AwaitMatchRecord, bool) {
	if res == nil {
		return trigger.AwaitMatchRecord{}, false
	}
	for _, rec := range res.Records {
		if rec.InstanceKey == instanceKey {
			return rec, true
		}
	}
	return trigger.AwaitMatchRecord{}, false
}

// matcher builds the sweeper's dispatch lane: the only AwaitMatcher in the
// process with the SystemTimeoutLane carve-out enabled.
func (s *AwaitTimeoutSweeper) matcher() *trigger.AwaitMatcher {
	return &trigger.AwaitMatcher{
		Store:             s.Store,
		Logger:            s.Logger,
		ResumeRetries:     s.ResumeRetries,
		ResumeRetryDelay:  s.ResumeRetryDelay,
		SystemTimeoutLane: true,
	}
}

// workspaceKeys resolves the sweep targets: the configured workspace, or
// every known workspace when unscoped (mirrors StaleTaskSweeper).
func (s *AwaitTimeoutSweeper) workspaceKeys(ctx context.Context) ([]string, error) {
	if s.WorkspaceKey != "" {
		return []string{s.WorkspaceKey}, nil
	}
	workspaces, err := s.Store.Workspaces().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list workspaces for await timeout sweep: %w", err)
	}
	keys := make([]string, 0, len(workspaces))
	for _, ws := range workspaces {
		if ws == nil {
			continue
		}
		keys = append(keys, ws.Key)
	}
	return keys, nil
}

func (s *AwaitTimeoutSweeper) batchLimit() int {
	if s.BatchLimit > 0 {
		return s.BatchLimit
	}
	return DefaultAwaitTimeoutSweepBatch
}

func (s *AwaitTimeoutSweeper) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}
