package trigger

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	"github.com/tysonthomas9/loomcli/internal/modules/execution"
	"github.com/tysonthomas9/loomcli/internal/store"
)

const (
	DefaultAwaitEventReconcileLimit = execution.DefaultReconciliationQueueLimit
)

// AwaitEventDispatcher resolves one admitted Automation event against pending
// Execution awaits.
type AwaitEventDispatcher interface {
	Dispatch(context.Context, string, AwaitDispatchEvent) (*AwaitDispatchResult, error)
}

type workspaceKeyLister interface {
	ListWorkspaceKeys(context.Context) ([]string, error)
}

type awaitTimeoutStore interface {
	Workspaces() store.WorkspaceStore
	Awaits() store.AwaitStore
	DriverRuns() store.DriverRunStore
}

// automationAwaitEventNotifier is the narrow Automation -> Execution bridge
// for the cron fast path. Durability comes from the always-on await-event
// outbox reconciler; this synchronous notification keeps the shipped immediate
// behavior and intentionally shares the same idempotent atomic matcher.
type automationAwaitEventNotifier struct {
	matcher *AwaitMatcher
}

var _ automation.AwaitEventNotifier = (*automationAwaitEventNotifier)(nil)

// NewAutomationAwaitEventNotifier is the retired Store-only compatibility
// constructor. Mutation authority must be injected explicitly.
//
// Deprecated: use NewAutomationAwaitEventNotifierWithResolver.
func NewAutomationAwaitEventNotifier(
	_ store.AwaitStore,
	_ store.DriverRunStore,
) (automation.AwaitEventNotifier, error) {
	return nil, fmt.Errorf("compose automation await notifier without Execution resolver: %w", automation.ErrUnavailable)
}

func NewAutomationAwaitEventNotifierWithResolver(
	awaits store.AwaitStore,
	driverRuns store.DriverRunStore,
	resolver store.AtomicAwaitStore,
) (automation.AwaitEventNotifier, error) {
	if awaits == nil || driverRuns == nil || resolver == nil {
		return nil, fmt.Errorf("compose automation await notifier: %w", automation.ErrUnavailable)
	}
	return &automationAwaitEventNotifier{matcher: NewAwaitMatcherWithResolver(awaits, driverRuns, resolver)}, nil
}

func (notifier *automationAwaitEventNotifier) NotifyAwaitEvent(
	ctx context.Context,
	event automation.AwaitEventNotification,
) error {
	if notifier == nil || notifier.matcher == nil {
		return automation.ErrUnavailable
	}
	if len(event.Payload) > domain.DefaultAwaitResumePayloadCap {
		return nil
	}
	_, err := notifier.matcher.Dispatch(ctx, event.WorkspaceKey, AwaitDispatchEvent{
		EventID: event.EventID, EventType: event.EventType, SubjectRef: event.SubjectRef,
		SourceKind: event.SourceKind, Origin: event.Origin,
		ActorRef: event.ActorRef, Payload: event.Payload,
	})
	return err
}

// AwaitEventReconciler drains the durable trigger-event notification outbox.
// A notification is completed only after atomic await dispatch succeeds. A
// crash or lost response leaves a leased row that is reclaimed and replayed;
// the atomic resolver makes that replay convergent.
type AwaitEventReconciler struct {
	queue        execution.AwaitEventNotificationAPI
	authorities  execution.SystemAuthorityResolver
	dispatcher   AwaitEventDispatcher
	workspace    string
	workspaces   workspaceKeyLister
	componentID  string
	claimPrefix  string
	claimCounter atomic.Uint64
	limit        int
}

func NewAwaitEventReconcilerWithExecution(
	queue execution.AwaitEventNotificationAPI,
	authorities execution.SystemAuthorityResolver,
	dispatcher AwaitEventDispatcher,
	workspace string,
	workspaces workspaceKeyLister,
	componentID string,
) (*AwaitEventReconciler, error) {
	if queue == nil || authorities == nil || dispatcher == nil || strings.TrimSpace(componentID) == "" {
		return nil, fmt.Errorf("await event queue, authority resolver, dispatcher, and component ID are required")
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" && workspaces == nil {
		return nil, fmt.Errorf("await event workspace lister is required for an unscoped reconciler")
	}
	return &AwaitEventReconciler{
		queue: queue, authorities: authorities, dispatcher: dispatcher, workspace: workspace, workspaces: workspaces,
		componentID: componentID,
		claimPrefix: newAwaitEventClaimPrefix(), limit: DefaultAwaitEventReconcileLimit,
	}, nil
}

// NewAwaitEventReconcilerWithExecutionStores is the composition facade for
// the standard atomic Await matcher. Callers provide only Execution's queue
// and authority ports plus the narrow legacy read stores; trigger matching
// remains an implementation detail of the driver runtime.
func NewAwaitEventReconcilerWithExecutionStores(
	queue execution.AwaitEventNotificationAPI,
	authorities execution.SystemAuthorityResolver,
	awaits store.AwaitStore,
	driverRuns store.DriverRunStore,
	resolver store.AtomicAwaitStore,
	workspace string,
	workspaces workspaceKeyLister,
	componentID string,
) (*AwaitEventReconciler, error) {
	return NewAwaitEventReconcilerWithExecution(
		queue,
		authorities,
		NewAwaitMatcherWithResolver(awaits, driverRuns, resolver),
		workspace,
		workspaces,
		componentID,
	)
}

func (reconciler *AwaitEventReconciler) RunOnce(ctx context.Context, now time.Time) error {
	_, err := reconciler.DrainOnce(ctx, now)
	return err
}

func (reconciler *AwaitEventReconciler) DrainOnce(ctx context.Context, now time.Time) (int, error) {
	if reconciler == nil || reconciler.queue == nil || reconciler.authorities == nil || reconciler.dispatcher == nil {
		return 0, fmt.Errorf("await event reconciler is unavailable")
	}
	if now.IsZero() {
		now = time.Now().UTC()
	} else {
		now = now.UTC()
	}
	workspaces, err := reconciler.workspaceKeys(ctx)
	if err != nil {
		return 0, err
	}
	claimed := 0
	var errs []error
	for _, workspace := range workspaces {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		count, err := reconciler.runWorkspace(ctx, workspace, now)
		claimed += count
		if err != nil {
			errs = append(errs, err)
		}
	}
	return claimed, errors.Join(errs...)
}

func (reconciler *AwaitEventReconciler) workspaceKeys(ctx context.Context) ([]string, error) {
	if reconciler.workspace != "" {
		return []string{reconciler.workspace}, nil
	}
	values, err := reconciler.workspaces.ListWorkspaceKeys(ctx)
	if err != nil {
		return nil, fmt.Errorf("list await event workspaces: %w", err)
	}
	return values, nil
}

func (reconciler *AwaitEventReconciler) runWorkspace(ctx context.Context, workspace string, now time.Time) (int, error) { //nolint:cyclop,funlen,gocognit // Durable claim processing must classify, complete, or retry every notification in order.
	claimID := fmt.Sprintf("%s-%d", reconciler.claimPrefix, reconciler.claimCounter.Add(1))
	auth, err := reconciler.authorities.ResolveExecutionSystemAuthority(
		ctx, workspace, execution.ActionClaimAwaitEventNotifications, reconciler.componentID,
	)
	if err != nil {
		return 0, fmt.Errorf("resolve await event notification claim authority in %q: %w", workspace, err)
	}
	notifications, err := reconciler.queue.ClaimAwaitEventNotifications(ctx, auth, execution.ClaimAwaitEventNotificationsCommand{
		WorkspaceKey: workspace, ClaimID: claimID, Before: now, Limit: reconciler.limit,
	})
	if err != nil {
		return 0, fmt.Errorf("claim await event notifications in %q: %w", workspace, err)
	}
	var errs []error
	for _, notification := range notifications {
		event := notification.Event
		durableID := awaitEventNotificationDurableID(notification)
		canonicalID, canonicalIdentity := event.CanonicalEventID()
		if !canonicalIdentity || (notification.DurableEventID != "" &&
			(strings.TrimSpace(notification.DurableEventID) != notification.DurableEventID || notification.DurableEventID != event.EventID)) {
			cause := fmt.Errorf("await event notification durable identity %q does not match event %q",
				notification.DurableEventID, event.EventID)
			errs = append(errs, cause)
			if quarantineErr := reconciler.quarantine(ctx, workspace, claimID, durableID, now); quarantineErr != nil {
				errs = append(errs, quarantineErr)
			}
			continue
		}
		if notification.CanonicalEventID != "" &&
			(strings.TrimSpace(notification.CanonicalEventID) != notification.CanonicalEventID || notification.CanonicalEventID != canonicalID) {
			cause := fmt.Errorf("await event notification canonical identity %q does not match event %q",
				notification.CanonicalEventID, canonicalID)
			errs = append(errs, cause)
			if quarantineErr := reconciler.quarantine(ctx, workspace, claimID, durableID, now); quarantineErr != nil {
				errs = append(errs, quarantineErr)
			}
			continue
		}
		if event.WorkspaceKey != workspace || durableID == "" || canonicalID == "" ||
			strings.TrimSpace(event.EventType) == "" {
			cause := fmt.Errorf("invalid persisted await event notification %q", event.EventID)
			errs = append(errs, cause)
			if quarantineErr := reconciler.quarantine(ctx, workspace, claimID, durableID, now); quarantineErr != nil {
				errs = append(errs, quarantineErr)
			}
			continue
		}
		if domain.IsAwaitTimeoutEventID(canonicalID) {
			// Timeout IDs are reserved to the non-journaled deadline-sweeper
			// lane. Quarantine an old/poisoned row by completing it without
			// dispatch, while surfacing the invariant violation once.
			cause := fmt.Errorf("await event notification %q uses reserved timeout identity %q", event.EventID, canonicalID)
			errs = append(errs, cause)
			if completeErr := reconciler.complete(ctx, workspace, claimID, durableID, now); completeErr != nil {
				errs = append(errs, completeErr)
			}
			continue
		}
		if !automation.EligibleForAwait(
			event.EventType, event.Origin, event.SourceKind,
			event.ActorRef, canonicalID,
		) {
			// Provenance is immutable, so retry cannot heal this notification.
			// Audit and complete the historical/forged row as a successful no-op;
			// a returned error would cause caller backoff even though a later
			// genuine outcome remains eligible.
			slog.Default().Warn("await event notification skipped: reserved provenance",
				"workspace", workspace,
				"event_id", event.EventID,
				"canonical_event_id", canonicalID,
				"event_type", event.EventType,
				"source_kind", event.SourceKind,
				"origin", event.Origin,
				"actor_ref", event.ActorRef,
			)
			if completeErr := reconciler.complete(ctx, workspace, claimID, durableID, now); completeErr != nil {
				errs = append(errs, completeErr)
			}
			continue
		}
		if notification.PayloadSize < 0 {
			cause := fmt.Errorf("await event notification %q has invalid payload size %d",
				event.EventID, notification.PayloadSize)
			errs = append(errs, cause)
			if retryErr := reconciler.retry(ctx, workspace, claimID, notification, now, cause); retryErr != nil {
				errs = append(errs, retryErr)
			}
			continue
		}
		if !notification.PayloadOversized && notification.PayloadSize > 0 &&
			notification.PayloadSize <= domain.DefaultAwaitResumePayloadCap &&
			notification.PayloadSize != len(event.Payload) {
			// Fleet reports the original byte size explicitly. Never resolve an
			// await from a truncated or otherwise inconsistent response body;
			// retry the immutable notification so a clean read can recover.
			cause := fmt.Errorf("await event notification %q payload size %d does not match body size %d",
				event.EventID, notification.PayloadSize, len(event.Payload))
			errs = append(errs, cause)
			if retryErr := reconciler.retry(ctx, workspace, claimID, notification, now, cause); retryErr != nil {
				errs = append(errs, retryErr)
			}
			continue
		}
		if notification.PayloadOversized || notification.PayloadSize > domain.DefaultAwaitResumePayloadCap ||
			len(event.Payload) > domain.DefaultAwaitResumePayloadCap {
			// The event remains available for audit, but its payload is not an
			// eligible resume value. Complete this notification so it cannot
			// poison the queue; a later valid event can still satisfy the await.
			payloadSize := notification.PayloadSize
			if payloadSize <= 0 {
				payloadSize = len(event.Payload)
			}
			slog.Default().Warn("await event notification skipped: payload exceeds await resume cap",
				"workspace", workspace,
				"event_id", event.EventID,
				"canonical_event_id", canonicalID,
				"payload_size", payloadSize,
				"payload_cap", domain.DefaultAwaitResumePayloadCap,
			)
			if completeErr := reconciler.complete(ctx, workspace, claimID, durableID, now); completeErr != nil {
				errs = append(errs, completeErr)
			}
			continue
		}
		_, dispatchErr := reconciler.dispatcher.Dispatch(ctx, workspace, AwaitDispatchEvent{
			EventID: canonicalID, EventType: event.EventType, SubjectRef: event.SubjectRef,
			SourceKind: event.SourceKind, Origin: automation.EventOrigin(event.Origin),
			ActorRef: event.ActorRef, Payload: event.Payload,
		})
		if dispatchErr != nil {
			errs = append(errs, fmt.Errorf("dispatch durable await event %q: %w", canonicalID, dispatchErr))
			if retryErr := reconciler.retry(ctx, workspace, claimID, notification, now, dispatchErr); retryErr != nil {
				errs = append(errs, retryErr)
			}
			continue
		}
		if completeErr := reconciler.complete(ctx, workspace, claimID, durableID, now); completeErr != nil {
			errs = append(errs, completeErr)
		}
	}
	return len(notifications), errors.Join(errs...)
}

func (reconciler *AwaitEventReconciler) quarantine(
	ctx context.Context,
	workspace, claimID, durableID string,
	now time.Time,
) error {
	if durableID == "" {
		return fmt.Errorf("quarantine await event notification: durable event identity is missing")
	}
	return reconciler.complete(ctx, workspace, claimID, durableID, now)
}

func (reconciler *AwaitEventReconciler) complete(
	ctx context.Context,
	workspace, claimID, eventID string,
	now time.Time,
) error {
	auth, err := reconciler.authorities.ResolveExecutionSystemAuthority(
		ctx, workspace, execution.ActionCompleteAwaitEventNotification, reconciler.componentID,
	)
	if err != nil {
		return fmt.Errorf("resolve await event notification completion authority: %w", err)
	}
	if err := reconciler.queue.CompleteAwaitEventNotification(ctx, auth, execution.CompleteAwaitEventNotificationCommand{
		WorkspaceKey: workspace, EventID: eventID, ClaimID: claimID, CompletedAt: now,
	}); err != nil {
		return fmt.Errorf("complete await event notification %q: %w", eventID, err)
	}
	return nil
}

func (reconciler *AwaitEventReconciler) retry(
	ctx context.Context,
	workspace, claimID string,
	notification execution.AwaitEventNotification,
	now time.Time,
	cause error,
) error {
	auth, err := reconciler.authorities.ResolveExecutionSystemAuthority(
		ctx, workspace, execution.ActionRetryAwaitEventNotification, reconciler.componentID,
	)
	if err != nil {
		return fmt.Errorf("resolve await event notification retry authority: %w", err)
	}
	if err := reconciler.queue.RetryAwaitEventNotification(ctx, auth, execution.RetryAwaitEventNotificationCommand{
		WorkspaceKey: workspace, EventID: awaitEventNotificationDurableID(notification), ClaimID: claimID,
		Attempt: notification.Attempt, FailedAt: now, Cause: cause.Error(),
	}); err != nil {
		return fmt.Errorf("schedule await event notification %q retry: %w", notification.Event.EventID, err)
	}
	return nil
}

func awaitEventNotificationDurableID(notification execution.AwaitEventNotification) string {
	if durableID := strings.TrimSpace(notification.DurableEventID); durableID != "" {
		return durableID
	}
	return strings.TrimSpace(notification.Event.EventID)
}

func newAwaitEventClaimPrefix() string {
	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err == nil {
		return "await-event-" + hex.EncodeToString(bytes[:])
	}
	return fmt.Sprintf("await-event-%d", time.Now().UTC().UnixNano())
}

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
	Store awaitTimeoutStore
	// Resolver is the required, injected Execution-owned atomic mutation port.
	// RunOnce fails closed when it is unavailable and never derives it from Store.
	Resolver store.AtomicAwaitStore
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

// RuntimeSummary exposes the bounded fields needed by the platform-runtime
// adapter without coupling that adapter to this infrastructure package.
func (result *AwaitTimeoutSweepResult) RuntimeSummary() (timedOut, resumeDeferred int, instanceKeys []string) {
	if result == nil {
		return 0, 0, nil
	}
	return result.TimedOut, result.ResumeDeferred, append([]string(nil), result.TimedOutInstanceKeys...)
}

// RunOnce performs a single sweep: list due await deadlines in each target
// workspace and emit each instance's timeout event through the matcher.
// Per-instance failures are joined, never abort the pass.
func (s *AwaitTimeoutSweeper) RunOnce(ctx context.Context) (*AwaitTimeoutSweepResult, error) {
	if s == nil || s.Store == nil {
		return nil, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	if s.Resolver == nil {
		return nil, fmt.Errorf("execution await resolver is unavailable: %w", execution.ErrUnavailable)
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
func (s *AwaitTimeoutSweeper) sweepInstance(ctx context.Context, matcher *AwaitMatcher, ws string, inst *domain.AwaitInstance, out *AwaitTimeoutSweepResult) error {
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
	case record.Outcome == AwaitMatchResolved:
		out.TimedOut++
		out.TimedOutInstanceKeys = append(out.TimedOutInstanceKeys, inst.InstanceKey)
	case record.Outcome == AwaitMatchAlreadyResolved:
		out.AlreadySatisfied++
	case record.Outcome == AwaitMatchResumeDeferred:
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
func awaitTimeoutDispatchEvent(inst *domain.AwaitInstance) (AwaitDispatchEvent, error) {
	eventType, subjectRef, ok := strings.Cut(inst.Pattern, ":")
	if !ok || eventType == "" || subjectRef == "" {
		// Registration validated RULE 1, so this is a corrupted row; leave it
		// to operator attention rather than guessing a key.
		return AwaitDispatchEvent{}, fmt.Errorf("await timeout sweep: instance %q pattern %q: %w",
			inst.InstanceKey, inst.Pattern, domain.ErrAwaitPatternUnscoped)
	}
	payload, err := json.Marshal(awaitTimeoutPayload{
		Timeout:     true,
		EventType:   eventType + ".timeout",
		InstanceKey: inst.InstanceKey,
		Deadline:    inst.Deadline.UTC(),
	})
	if err != nil {
		return AwaitDispatchEvent{}, fmt.Errorf("encode await timeout payload for %q: %w", inst.InstanceKey, err)
	}
	return AwaitDispatchEvent{
		EventID:    domain.AwaitTimeoutEventID(inst.InstanceKey),
		EventType:  eventType,
		SourceKind: "timeout",
		Origin:     automation.EventOriginSystem,
		SubjectRef: subjectRef,
		ActorRef:   domain.AwaitTimeoutActor,
		Payload:    payload,
	}, nil
}

// awaitTimeoutRecord finds the dispatched instance's own match record (RULE 3
// filtering means it is the only one the matcher may produce).
func awaitTimeoutRecord(res *AwaitDispatchResult, instanceKey string) (AwaitMatchRecord, bool) {
	if res == nil {
		return AwaitMatchRecord{}, false
	}
	for _, rec := range res.Records {
		if rec.InstanceKey == instanceKey {
			return rec, true
		}
	}
	return AwaitMatchRecord{}, false
}

// matcher builds the sweeper's dispatch lane: the only AwaitMatcher in the
// process with the SystemTimeoutLane carve-out enabled.
func (s *AwaitTimeoutSweeper) matcher() *AwaitMatcher {
	return &AwaitMatcher{
		Store:             s.Store,
		AtomicResolver:    s.Resolver,
		Logger:            s.Logger,
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
	for _, workspace := range workspaces {
		if workspace != nil {
			keys = append(keys, workspace.Key)
		}
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
