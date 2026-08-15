package trigger

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// DefaultDeliverySweepBatch bounds ListDue per workspace per pass when the
// sweeper has no explicit BatchLimit (LOOM_TRIGGER_SWEEP_BATCH in serve).
const DefaultDeliverySweepBatch = 50

// deliverySweepBackoffCap caps the exponential retry backoff (locked
// decision: next_retry_at = now + min(retry_backoff_seconds * 2^(attempt-1),
// 1h)).
const deliverySweepBackoffCap = time.Hour

// Error classes the sweeper stamps on deliveries whose attempt could not even
// reach admission. They are diagnostic only — terminality is always decided
// by the store's retries_exhausted rule, never by the class.
const (
	// DeliveryErrorBindingNotFound marks a delivery terminally dropped because
	// its binding was deleted after dispatch took a snapshot.
	DeliveryErrorBindingNotFound = "binding_not_found"
	// deliverySweepErrorRoute: the original ingress route key could not be
	// reconstructed (the event's attributed binding vanished or lost its
	// route key), so there was nothing to re-dispatch.
	deliverySweepErrorRoute = "sweep_route_unresolved"
	// deliverySweepErrorDispatch: DispatchTriggerRouteV2 itself failed (e.g.
	// the binding was disabled and the route no longer matches anything).
	deliverySweepErrorDispatch = "sweep_dispatch_failed"
	// deliverySweepErrorUnmatched: the re-dispatch succeeded but produced no
	// leg for this delivery's binding (the binding no longer matches the
	// route).
	deliverySweepErrorUnmatched = "sweep_binding_unmatched"
)

// DeliverySweeper is the serve-side retry loop for trigger deliveries,
// modeled on StaleTaskSweeper. Each RunOnce pass lists due deliveries — held
// (queue concurrency policy, promotion rides this sweeper) and retryable
// failed ones — and re-attempts each via DispatchTriggerRouteV2 with the
// ORIGINAL ingress idempotency key. The dispatch path is individually
// idempotent per leg, so the re-dispatch dedups the stored TriggerEvent and
// every already-admitted run, re-creates only the missing legs, and held
// deliveries re-enter the concurrency admission gate and promote to
// dispatched when their subject has freed.
//
// On a non-admitting attempt the delivery's attempt count increments and
// next_retry_at moves to now + min(retry_backoff_seconds * 2^(attempt-1),
// 1h); once the attempt count reaches the binding's retry_max_attempts the
// delivery is terminal failed/retries_exhausted (defaults 5 attempts, 30s
// backoff — domain.DefaultTriggerRetry*).
//
// Note for the re-dispatch inputs: the inline Payload and SubjectAttrs of the
// original ingress are not persisted on the TriggerEvent (only
// RawPayloadRef/Digest are), so a run admitted by the sweeper carries the
// payload by ref and subject keys re-render without adapter attrs (template
// render falls back to the conservative default key). The event itself
// dedups on the idempotency key, so no stored state is lost or duplicated.
//
// Single serve instance today, so no distributed lock is needed. For
// multi-replica later, mirror fleet-db's sweep-lock recipe: SETNX a
// sweep:trigger-delivery lock key with a TTL slightly above the sweep
// interval, run the pass only while holding it, and DEL on completion —
// per-leg idempotency already makes overlapping sweeps safe, the lock only
// avoids duplicate work.
type DeliverySweeper struct {
	Store store.Store
	// WorkspaceKey scopes the sweep to one workspace. Empty sweeps every
	// workspace returned by Store.Workspaces().List (mirrors
	// StaleTaskSweeper/OutboxDispatcher).
	WorkspaceKey string
	// BatchLimit bounds ListDue per workspace per pass. Zero or negative
	// falls back to DefaultDeliverySweepBatch.
	BatchLimit int
	// Now is a clock seam for tests; nil uses time.Now.
	Now func() time.Time
	// Logger receives terminal-drop audit records; nil uses slog.Default.
	Logger *slog.Logger
}

// DeliverySweepResult summarizes one RunOnce pass.
type DeliverySweepResult struct {
	// Dispatched counts deliveries whose re-attempt admitted (or healed onto)
	// a run: held promotions and failed-delivery redispatches.
	Dispatched int
	// Rescheduled counts deliveries whose attempt did not admit a run and
	// were re-held with a backed-off next_retry_at.
	Rescheduled int
	// Exhausted counts deliveries forced terminal failed/retries_exhausted
	// because the attempt budget is spent.
	Exhausted int
	// Dropped counts deliveries made terminal because their binding no longer
	// exists. They are recorded as rejected/binding_not_found on the wire.
	Dropped int
}

// RunOnce performs a single sweep over every target workspace. It keeps going
// past per-delivery errors and returns them joined; a non-nil result is
// returned even when some deliveries errored.
func (s *DeliverySweeper) RunOnce(ctx context.Context) (*DeliverySweepResult, error) {
	if s == nil || s.Store == nil {
		return nil, fmt.Errorf("store required: %w", domain.ErrInvalid)
	}
	workspaces, err := s.workspaceKeys(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now()
	out := &DeliverySweepResult{}
	var errs []error
	for _, ws := range workspaces {
		if err := s.sweepWorkspace(ctx, ws, now, out); err != nil {
			errs = append(errs, err)
		}
	}
	return out, errors.Join(errs...)
}

func (s *DeliverySweeper) sweepWorkspace(ctx context.Context, ws string, now time.Time, out *DeliverySweepResult) error {
	due, err := s.Store.TriggerDeliveries().ListDue(ctx, ws, store.TriggerDeliveryDueFilter{
		Now:   now,
		Limit: s.batchLimit(),
	})
	if err != nil {
		return fmt.Errorf("list due trigger deliveries in workspace %q: %w", ws, err)
	}
	var errs []error
	for _, delivery := range due {
		if ctx.Err() != nil {
			errs = append(errs, ctx.Err())
			break
		}
		if delivery == nil {
			continue
		}
		if err := s.sweepDelivery(ctx, ws, delivery.DeliveryID, now, out); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// sweepDelivery re-attempts one due delivery. The delivery is re-read first:
// an earlier re-dispatch in the same pass may already have healed it (sibling
// legs of one event share a re-dispatch), in which case it is skipped.
func (s *DeliverySweeper) sweepDelivery(ctx context.Context, ws, deliveryID string, now time.Time, out *DeliverySweepResult) error {
	delivery, err := s.Store.TriggerDeliveries().Get(ctx, ws, deliveryID)
	if err != nil {
		return fmt.Errorf("get due trigger delivery %q in workspace %q: %w", deliveryID, ws, err)
	}
	if !deliveryStillDue(delivery, now) {
		return nil
	}
	binding, bindingErr := s.Store.TriggerBindings().Get(ctx, ws, delivery.TriggerBindingID)
	if errors.Is(bindingErr, domain.ErrNotFound) {
		return s.dropMissingBinding(ctx, ws, delivery, out)
	}
	if bindingErr != nil {
		binding = nil
	}
	event, err := s.Store.TriggerEvents().Get(ctx, ws, delivery.TriggerEventID)
	if err != nil {
		return s.recordRetry(ctx, ws, delivery, binding, now, deliverySweepErrorRoute, out)
	}
	routeKey := s.resolveRouteKey(ctx, ws, event)
	if routeKey == "" {
		return s.recordRetry(ctx, ws, delivery, binding, now, deliverySweepErrorRoute, out)
	}
	result, err := s.Store.TriggerRoutes().DispatchTriggerRouteV2(ctx, ws, routeKey, redispatchInput(event))
	if err != nil {
		if ctx.Err() != nil {
			// Shutdown, not a delivery failure — do not burn the attempt.
			return ctx.Err()
		}
		return s.recordRetry(ctx, ws, delivery, binding, now, deliverySweepErrorDispatch, out)
	}
	leg, ok := findDeliveryLeg(result, delivery)
	if !ok {
		return s.recordRetry(ctx, ws, delivery, binding, now, deliverySweepErrorUnmatched, out)
	}
	if leg.RunID == "" {
		// The leg resolved without a run: the subject is still busy (held
		// stays held) or the policy refused admission again. Not an error —
		// burn the attempt and back off.
		return s.recordRetry(ctx, ws, delivery, binding, now, "", out)
	}
	// Admission succeeded. The dispatch path's heal lane already promotes a
	// held delivery internally; re-applying dispatched here is idempotent and
	// also covers the failed-delivery lane, which heal reports unchanged.
	if _, err := s.Store.TriggerDeliveries().UpdateResult(ctx, ws, delivery.DeliveryID, store.TriggerDeliveryResultUpdate{
		Status:      domain.TriggerDeliveryDispatched,
		Attempt:     delivery.Attempt + 1,
		DriverRunID: leg.RunID,
	}); err != nil {
		return fmt.Errorf("record dispatched trigger delivery %q in workspace %q: %w", delivery.DeliveryID, ws, err)
	}
	out.Dispatched++
	return nil
}

func (s *DeliverySweeper) dropMissingBinding(ctx context.Context, ws string, delivery *domain.TriggerDelivery, out *DeliverySweepResult) error {
	if _, err := s.Store.TriggerDeliveries().UpdateResult(ctx, ws, delivery.DeliveryID, store.TriggerDeliveryResultUpdate{
		Status: domain.TriggerDeliveryRejected, Attempt: delivery.Attempt, ErrorClass: DeliveryErrorBindingNotFound,
	}); err != nil {
		return fmt.Errorf("drop trigger delivery %q with missing binding in workspace %q: %w", delivery.DeliveryID, ws, err)
	}
	s.logger().WarnContext(ctx, "dropping trigger delivery because binding no longer exists",
		"workspace", ws, "delivery_id", delivery.DeliveryID, "binding_id", delivery.TriggerBindingID)
	out.Dropped++
	return nil
}

// recordRetry records one non-admitting attempt: attempt++, exponential
// backoff via UpdateResult — or terminal failed once the binding's retry
// budget is reached (the store stamps retries_exhausted and clears
// next_retry_at; locked decision).
func (s *DeliverySweeper) recordRetry(ctx context.Context, ws string, delivery *domain.TriggerDelivery, binding *domain.TriggerBinding, now time.Time, errorClass string, out *DeliverySweepResult) error {
	attempt := delivery.Attempt + 1
	update := store.TriggerDeliveryResultUpdate{
		Attempt:    attempt,
		ErrorClass: errorClass,
	}
	exhausted := attempt >= deliveryRetryMaxAttempts(binding)
	if exhausted {
		update.Status = domain.TriggerDeliveryFailed
	} else {
		// A held delivery stays held (queue promotion keeps riding the
		// sweeper); a failed one stays failed.
		update.Status = delivery.Status
		next := now.Add(deliveryRetryBackoff(binding, attempt))
		update.NextRetryAt = &next
	}
	if _, err := s.Store.TriggerDeliveries().UpdateResult(ctx, ws, delivery.DeliveryID, update); err != nil {
		return fmt.Errorf("record retry for trigger delivery %q in workspace %q: %w", delivery.DeliveryID, ws, err)
	}
	if exhausted {
		out.Exhausted++
	} else {
		out.Rescheduled++
	}
	return nil
}

// resolveRouteKey reconstructs the original ingress route key from the
// event's attributed binding (matched[0] of the original dispatch — the exact
// RouteKey owner for every current ingress lane: webhooks require one for
// signature verification and cron dispatches its own binding's route key).
// Empty means unrecoverable.
func (s *DeliverySweeper) resolveRouteKey(ctx context.Context, ws string, event *domain.TriggerEvent) string {
	if event.TriggerBindingID == "" {
		return ""
	}
	binding, err := s.Store.TriggerBindings().Get(ctx, ws, event.TriggerBindingID)
	if err != nil {
		return ""
	}
	return binding.RouteKey
}

// workspaceKeys resolves the sweep targets: the configured workspace, or
// every known workspace when unscoped (mirrors StaleTaskSweeper).
func (s *DeliverySweeper) workspaceKeys(ctx context.Context) ([]string, error) {
	if s.WorkspaceKey != "" {
		return []string{s.WorkspaceKey}, nil
	}
	workspaces, err := s.Store.Workspaces().List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list workspaces for delivery sweep: %w", err)
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

func (s *DeliverySweeper) batchLimit() int {
	if s.BatchLimit > 0 {
		return s.BatchLimit
	}
	return DefaultDeliverySweepBatch
}

func (s *DeliverySweeper) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now().UTC()
}

func (s *DeliverySweeper) logger() *slog.Logger {
	if s.Logger != nil {
		return s.Logger
	}
	return slog.Default()
}

// redispatchInput rebuilds the dispatch input from the persisted event. The
// ORIGINAL idempotency key is the healing handle: the event dedups onto the
// stored record and every leg derives its run/delivery identity from it.
func redispatchInput(event *domain.TriggerEvent) store.TriggerRouteDispatch {
	return store.TriggerRouteDispatch{
		IdempotencyKey:   event.IdempotencyKey,
		SourceEventID:    event.SourceEventID,
		EventType:        event.EventType,
		SubjectRef:       event.SubjectRef,
		ActorRef:         event.ActorRef,
		RawPayloadRef:    event.RawPayloadRef,
		RawPayloadDigest: event.RawPayloadDigest,
		SignatureStatus:  event.SignatureStatus,
		ReplayOfEventID:  event.ReplayOfEventID,
	}
}

// findDeliveryLeg picks this delivery's leg out of the re-dispatch result:
// the leg with the same deterministic delivery id, falling back to the
// binding id when the legacy/fan-out lane changed the id shape since ingress.
func findDeliveryLeg(result *store.TriggerRouteDispatchResult, delivery *domain.TriggerDelivery) (store.TriggerRouteDelivery, bool) {
	if result == nil {
		return store.TriggerRouteDelivery{}, false
	}
	for _, leg := range result.Deliveries {
		if leg.DeliveryID == delivery.DeliveryID {
			return leg, true
		}
	}
	for _, leg := range result.Deliveries {
		if leg.BindingID == delivery.TriggerBindingID {
			return leg, true
		}
	}
	return store.TriggerRouteDelivery{}, false
}

// deliveryStillDue re-checks due-index membership against a fresh read: held
// or retryable-failed, with next_retry_at (nil = immediately due) not after
// now. Mirrors the stores' due predicate.
func deliveryStillDue(d *domain.TriggerDelivery, now time.Time) bool {
	switch d.Status {
	case domain.TriggerDeliveryHeld:
	case domain.TriggerDeliveryFailed:
		if d.ErrorClass == domain.TriggerDeliveryErrorRetriesExhausted {
			return false
		}
	default:
		return false
	}
	return d.NextRetryAt == nil || !d.NextRetryAt.After(now)
}

// deliveryRetryMaxAttempts resolves the binding's retry budget, defaulting
// defensively for vanished bindings or pre-retry records.
func deliveryRetryMaxAttempts(binding *domain.TriggerBinding) int {
	if binding == nil || binding.RetryMaxAttempts <= 0 {
		return domain.DefaultTriggerRetryMaxAttempts
	}
	return binding.RetryMaxAttempts
}

// deliveryRetryBackoff is the delay after the given attempt count:
// retry_backoff_seconds * 2^(attempt-1), capped at one hour. Attempt 1 (the
// dispatch path's initial held suspend) is the base backoff, so the sweeper's
// first re-suspend doubles it.
func deliveryRetryBackoff(binding *domain.TriggerBinding, attempt int) time.Duration {
	seconds := domain.DefaultTriggerRetryBackoffSeconds
	if binding != nil && binding.RetryBackoffSeconds > 0 {
		seconds = binding.RetryBackoffSeconds
	}
	delay := time.Duration(seconds) * time.Second
	for i := 1; i < attempt && delay < deliverySweepBackoffCap; i++ {
		delay *= 2
	}
	if delay > deliverySweepBackoffCap {
		delay = deliverySweepBackoffCap
	}
	return delay
}
