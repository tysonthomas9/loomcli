package memstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
)

// triggerEventStore is the in-memory TriggerEvent repository. It dedups on
// idempotency key the same way fleet-db's storage layer does: a create with a
// previously-seen key returns the existing event instead of inserting again.
type triggerEventStore struct {
	mu            sync.RWMutex
	items         map[string]map[string]*domain.TriggerEvent // ws -> eventID -> event
	idempo        map[string]map[string]string               // ws -> idempotencyKey -> eventID
	notifications map[string]map[string]*awaitEventNotificationRow
	seq           int64
}

func newTriggerEventStore() *triggerEventStore {
	return &triggerEventStore{
		items:         make(map[string]map[string]*domain.TriggerEvent),
		idempo:        make(map[string]map[string]string),
		notifications: make(map[string]map[string]*awaitEventNotificationRow),
	}
}

var (
	_ store.TriggerEventStore    = (*triggerEventStore)(nil)
	_ store.TriggerEventAppender = (*triggerEventStore)(nil)
)

func (s *triggerEventStore) Get(_ context.Context, ws, eventID string) (*domain.TriggerEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	event, ok := s.items[ws][eventID]
	if !ok {
		return nil, fmt.Errorf("trigger event %q in workspace %q: %w", eventID, ws, domain.ErrNotFound)
	}
	out := *event
	return &out, nil
}

func (s *triggerEventStore) List(_ context.Context, ws string, filter store.TriggerEventFilter) ([]*domain.TriggerEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.TriggerEvent, 0, len(s.items[ws]))
	for _, event := range s.items[ws] {
		if filter.SourceKind != "" && event.SourceKind != filter.SourceKind {
			continue
		}
		if filter.TriggerBindingID != "" && event.TriggerBindingID != filter.TriggerBindingID {
			continue
		}
		clone := *event
		out = append(out, &clone)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ReceivedAt.After(out[j].ReceivedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

// AppendTriggerEvent is the store.TriggerEventAppender capability: journal
// one server-stamped event without route dispatch (the run.finished lifecycle
// lane, AW6). Unlike create it preserves the caller's deterministic EventID
// and is idempotent on it; the idempotency-key index is shared with the
// dispatch lane, so a later loopback dispatch of the same emission dedups
// against the journaled record instead of double-writing. Appends take the
// journal mutex, so an await registration scan (platform_await.go) either
// sees this event or registers pending strictly before it (RULE 2 — no lost wakeup).
func (s *triggerEventStore) AppendTriggerEvent(_ context.Context, event *domain.TriggerEvent) (*domain.TriggerEvent, error) {
	if event == nil || event.WorkspaceKey == "" || event.EventID == "" || event.EventType == "" {
		return nil, fmt.Errorf("trigger event append requires workspace, event id and event type: %w", domain.ErrInvalid)
	}
	canonicalID, canonical := event.CanonicalEventID()
	if !canonical || domain.IsAwaitTimeoutEventID(canonicalID) {
		return nil, fmt.Errorf("trigger event append requires a canonical non-reserved identity: %w", domain.ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[event.WorkspaceKey] == nil {
		s.items[event.WorkspaceKey] = make(map[string]*domain.TriggerEvent)
		s.idempo[event.WorkspaceKey] = make(map[string]string)
	}
	if existing, ok := s.items[event.WorkspaceKey][event.EventID]; ok {
		out := *existing
		return &out, nil
	}
	if event.IdempotencyKey != "" {
		if existingID, ok := s.idempo[event.WorkspaceKey][event.IdempotencyKey]; ok {
			out := *s.items[event.WorkspaceKey][existingID]
			return &out, nil
		}
	}
	stored := *event
	s.items[event.WorkspaceKey][event.EventID] = &stored
	s.enqueueAwaitEventNotificationLocked(&stored)
	if event.IdempotencyKey != "" {
		s.idempo[event.WorkspaceKey][event.IdempotencyKey] = event.EventID
	}
	out := stored
	return &out, nil
}

// create inserts the event, deduping on idempotency key. The returned bool is
// true when an existing event was returned instead of inserting a new one.
func (s *triggerEventStore) create(event *domain.TriggerEvent) (*domain.TriggerEvent, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[event.WorkspaceKey] == nil {
		s.items[event.WorkspaceKey] = make(map[string]*domain.TriggerEvent)
		s.idempo[event.WorkspaceKey] = make(map[string]string)
	}
	if event.IdempotencyKey != "" {
		if existingID, ok := s.idempo[event.WorkspaceKey][event.IdempotencyKey]; ok {
			existing := *s.items[event.WorkspaceKey][existingID]
			return &existing, true
		}
	}
	s.seq++
	event.EventID = fmt.Sprintf("event-%d", s.seq)
	stored := *event
	s.items[event.WorkspaceKey][event.EventID] = &stored
	s.enqueueAwaitEventNotificationLocked(&stored)
	if event.IdempotencyKey != "" {
		s.idempo[event.WorkspaceKey][event.IdempotencyKey] = event.EventID
	}
	out := stored
	return &out, false
}

// triggerDeliveryStore is the in-memory TriggerDelivery repository. bindings
// resolves each delivery's retry budget on UpdateResult (mirrors fleet-db's
// per-update binding fetch).
type triggerDeliveryStore struct {
	mu       sync.RWMutex
	items    map[string]map[string]*domain.TriggerDelivery // ws -> deliveryID -> delivery
	bindings *triggerBindingStore
	// failCreate, when set, lets tests inject delivery write failures to
	// exercise per-leg redelivery healing (mirrors fleet-db's fake-store
	// hook). Always nil in production wiring.
	failCreate func(*domain.TriggerDelivery) error
}

func newTriggerDeliveryStore(bindings *triggerBindingStore) *triggerDeliveryStore {
	return &triggerDeliveryStore{
		items:    make(map[string]map[string]*domain.TriggerDelivery),
		bindings: bindings,
	}
}

var _ store.TriggerDeliveryStore = (*triggerDeliveryStore)(nil)

func (s *triggerDeliveryStore) Get(_ context.Context, ws, deliveryID string) (*domain.TriggerDelivery, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	delivery, ok := s.items[ws][deliveryID]
	if !ok {
		return nil, fmt.Errorf("trigger delivery %q in workspace %q: %w", deliveryID, ws, domain.ErrNotFound)
	}
	return cloneTriggerDelivery(delivery), nil
}

func (s *triggerDeliveryStore) List(_ context.Context, ws string, filter store.TriggerDeliveryFilter) ([]*domain.TriggerDelivery, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.TriggerDelivery, 0, len(s.items[ws]))
	for _, delivery := range s.items[ws] {
		if filter.TriggerEventID != "" && delivery.TriggerEventID != filter.TriggerEventID {
			continue
		}
		if filter.TriggerBindingID != "" && delivery.TriggerBindingID != filter.TriggerBindingID {
			continue
		}
		if filter.Status != "" && delivery.Status != filter.Status {
			continue
		}
		out = append(out, cloneTriggerDelivery(delivery))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

// ListDue is the in-memory functional twin of fleet-db's retry due-index
// ZSET: a mutex-guarded scan over held / retryable-failed deliveries whose
// due score (NextRetryAt unix seconds, nil = 0 = immediately due) is <= Now,
// ordered like ZRANGEBYSCORE — ascending score, deliveryID for ties.
func (s *triggerDeliveryStore) ListDue(_ context.Context, ws string, filter store.TriggerDeliveryDueFilter) ([]*domain.TriggerDelivery, error) {
	now := filter.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*domain.TriggerDelivery, 0, len(s.items[ws]))
	for _, delivery := range s.items[ws] {
		if !triggerDeliveryInDueIndex(delivery) {
			continue
		}
		if triggerDeliveryDueScore(delivery.NextRetryAt) > now.Unix() {
			continue
		}
		out = append(out, cloneTriggerDelivery(delivery))
	}
	sort.Slice(out, func(i, j int) bool {
		si, sj := triggerDeliveryDueScore(out[i].NextRetryAt), triggerDeliveryDueScore(out[j].NextRetryAt)
		if si != sj {
			return si < sj
		}
		return out[i].DeliveryID < out[j].DeliveryID
	})
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

// UpdateResult records one retry-sweeper attempt outcome, mirroring
// fleet-db's UpdateTriggerDeliveryResult: a failed result whose attempt
// count reaches the binding's retry budget is forced terminal
// (failed/retries_exhausted, NextRetryAt cleared) and final deliveries
// reject transitions to a different status.
func (s *triggerDeliveryStore) UpdateResult(ctx context.Context, ws, deliveryID string, update store.TriggerDeliveryResultUpdate) (*domain.TriggerDelivery, error) {
	// The binding lookup happens before the write lock: the binding store
	// guards itself, and ordering the locks this way keeps the stores free
	// of nested-lock deadlocks.
	maxAttempts, err := s.resultMaxAttempts(ctx, ws, deliveryID)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delivery, ok := s.items[ws][deliveryID]
	if !ok {
		return nil, fmt.Errorf("trigger delivery %q in workspace %q: %w", deliveryID, ws, domain.ErrNotFound)
	}
	if err := applyTriggerDeliveryResult(delivery, update, maxAttempts, time.Now().UTC()); err != nil {
		return nil, err
	}
	return cloneTriggerDelivery(delivery), nil
}

// resultMaxAttempts resolves the retry budget for a delivery's binding. A
// vanished binding (or a pre-retry record with budget 0) falls back to the
// write-time default so sweeping never stalls — mirrors fleet-db.
func (s *triggerDeliveryStore) resultMaxAttempts(ctx context.Context, ws, deliveryID string) (int, error) {
	s.mu.RLock()
	delivery, ok := s.items[ws][deliveryID]
	bindingID := ""
	if ok {
		bindingID = delivery.TriggerBindingID
	}
	s.mu.RUnlock()
	if !ok {
		return 0, fmt.Errorf("trigger delivery %q in workspace %q: %w", deliveryID, ws, domain.ErrNotFound)
	}
	binding, err := s.bindings.Get(ctx, ws, bindingID)
	if errors.Is(err, domain.ErrNotFound) {
		return domain.DefaultTriggerRetryMaxAttempts, nil
	}
	if err != nil {
		return 0, err
	}
	if binding.RetryMaxAttempts <= 0 {
		return domain.DefaultTriggerRetryMaxAttempts, nil
	}
	return binding.RetryMaxAttempts, nil
}

// applyTriggerDeliveryResult mutates the delivery with one attempt outcome
// (mirrors fleet-db's applyTriggerDeliveryResult). Final deliveries reject
// transitions to a different status; re-applying the same status stays
// idempotent.
func applyTriggerDeliveryResult(d *domain.TriggerDelivery, update store.TriggerDeliveryResultUpdate, maxAttempts int, now time.Time) error {
	if !update.Status.IsValid() {
		return fmt.Errorf("update trigger delivery result: delivery status %q: %w", update.Status, domain.ErrInvalid)
	}
	if triggerDeliveryResultFinal(d) && update.Status != d.Status && !triggerDeliverySupersedeTransition(d.Status, update.Status) {
		return fmt.Errorf("update trigger delivery result: delivery already %s: %w", d.Status, domain.ErrInvalidTransition)
	}
	d.Status = update.Status
	if update.Attempt != 0 {
		d.Attempt = update.Attempt
	}
	d.NextRetryAt = clonePtr(update.NextRetryAt)
	d.ErrorClass = update.ErrorClass
	if update.DriverRunID != "" {
		d.DriverRunID = update.DriverRunID
	}
	if d.Status == domain.TriggerDeliveryFailed && d.Attempt >= maxAttempts {
		d.ErrorClass = domain.TriggerDeliveryErrorRetriesExhausted
		d.NextRetryAt = nil
	}
	d.UpdatedAt = now
	return nil
}

// triggerDeliveryInDueIndex reports whether the delivery is retry-sweeper
// work: held deliveries (queue-policy promotion rides the sweeper) and
// retryable failures. Terminal failures carry error class retries_exhausted
// and stay out; every other status is not sweeper work. Mirrors fleet-db's
// due-index membership predicate.
func triggerDeliveryInDueIndex(d *domain.TriggerDelivery) bool {
	switch d.Status {
	case domain.TriggerDeliveryHeld:
		return true
	case domain.TriggerDeliveryFailed:
		return d.ErrorClass != domain.TriggerDeliveryErrorRetriesExhausted
	default:
		return false
	}
}

// triggerDeliverySupersedeTransition permits the one out-of-final transition
// the replace concurrency policy needs (mirrors fleet-db): a dispatched
// delivery whose queued run is later superseded by a newer event for the
// same subject.
func triggerDeliverySupersedeTransition(from, to domain.TriggerDeliveryStatus) bool {
	return from == domain.TriggerDeliveryDispatched && to == domain.TriggerDeliverySuperseded
}

// triggerDeliveryResultFinal reports whether the delivery reached a state
// the retry sweeper must not move it out of.
func triggerDeliveryResultFinal(d *domain.TriggerDelivery) bool {
	switch d.Status {
	case domain.TriggerDeliveryDispatched, domain.TriggerDeliveryRejected,
		domain.TriggerDeliveryDuplicate, domain.TriggerDeliverySuperseded,
		domain.TriggerDeliveryReplayed:
		return true
	case domain.TriggerDeliveryFailed:
		return d.ErrorClass == domain.TriggerDeliveryErrorRetriesExhausted
	default:
		return false
	}
}

// triggerDeliveryDueScore is the functional twin of fleet-db's ZSET score:
// NextRetryAt unix seconds, with nil scoring 0 (immediately due).
func triggerDeliveryDueScore(nextRetryAt *time.Time) int64 {
	if nextRetryAt == nil {
		return 0
	}
	return nextRetryAt.Unix()
}

// cloneTriggerDelivery deep-copies a delivery, including its optional
// retry timestamp, so callers can never mutate stored state.
func cloneTriggerDelivery(d *domain.TriggerDelivery) *domain.TriggerDelivery {
	out := *d
	out.NextRetryAt = clonePtr(d.NextRetryAt)
	return &out
}

// create inserts a delivery, returning domain.ErrAlreadyExists when one with
// the same ID is already present (so replays are idempotent).
func (s *triggerDeliveryStore) create(delivery *domain.TriggerDelivery) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[delivery.WorkspaceKey] == nil {
		s.items[delivery.WorkspaceKey] = make(map[string]*domain.TriggerDelivery)
	}
	if _, ok := s.items[delivery.WorkspaceKey][delivery.DeliveryID]; ok {
		return domain.ErrAlreadyExists
	}
	if s.failCreate != nil {
		if err := s.failCreate(delivery); err != nil {
			return err
		}
	}
	stored := *delivery
	s.items[delivery.WorkspaceKey][delivery.DeliveryID] = &stored
	return nil
}

// triggerRouteStore implements the dispatch path against the in-memory stores,
// mirroring fleet-db's dispatchTriggerRouteRun: compute the matched binding set
// (exact RouteKey owner ∪ enabled pattern matches), persist ONE TriggerEvent,
// then per matched binding enqueue a queued DriverRun and record a
// TriggerDelivery. The writes are individually idempotent but not a single
// transaction (see store.TriggerRouteDispatcher); a failure after earlier legs
// are durable returns an error and redelivery heals each leg independently.
type triggerRouteStore struct {
	bindings   *triggerBindingStore
	events     *triggerEventStore
	deliveries *triggerDeliveryStore
	runs       *driverRunStore
	seq        int64
	mu         sync.Mutex
}

var _ store.TriggerRouteDispatcher = (*triggerRouteStore)(nil)

// DispatchTriggerRoute is the legacy single-run lane: same fan-out dispatch,
// returning only the primary run (kept for the webhooks module until it moves
// to the deliveries[] wire).
func (s *triggerRouteStore) DispatchTriggerRoute(ctx context.Context, ws, routeKey string, in store.TriggerRouteDispatch) (*domain.DriverRun, error) {
	result, err := s.DispatchTriggerRouteV2(ctx, ws, routeKey, in)
	if err != nil {
		return nil, err
	}
	return result.PrimaryRun, nil
}

func (s *triggerRouteStore) DispatchTriggerRouteV2(ctx context.Context, ws, routeKey string, in store.TriggerRouteDispatch) (*store.TriggerRouteDispatchResult, error) {
	matched, exact, err := s.matchTriggerRouteBindings(ctx, ws, routeKey)
	if err != nil {
		return nil, err
	}
	if len(matched) == 0 {
		// Zero matches (including a disabled exact binding) keeps the legacy
		// not-found contract for the route.
		return nil, fmt.Errorf("trigger binding route %q in workspace %q: %w", routeKey, ws, domain.ErrNotFound)
	}
	now := time.Now().UTC()
	// One event per ingress, attributed to the first matched binding (the
	// exact RouteKey owner when present); deliveries link the other legs.
	event, _ := s.events.create(dispatchTriggerEvent(ws, matched[0], in, now))

	// Legacy single-binding exact path: keep the bare idempotency key, the
	// caller-supplied run id and the delivery-{event} id so pre-fan-out
	// consumers stay stable (locked decision). Fan-out legs instead derive
	// deterministic ids from the (deduped) event id plus the binding id, and
	// a composite {idempotencyKey}#{bindingID} run key, so redelivery heals
	// each leg independently.
	legacy := exact != nil && len(matched) == 1 && matched[0].BindingID == exact.BindingID
	result := &store.TriggerRouteDispatchResult{}
	for _, binding := range matched {
		leg := triggerRouteLeg{
			RunID:          "run-" + shortTriggerHash(event.EventID, binding.BindingID),
			DeliveryID:     "delivery-" + event.EventID + "-" + binding.BindingID,
			IdempotencyKey: compositeTriggerIdempotencyKey(in.IdempotencyKey, binding.BindingID),
		}
		if legacy {
			leg = triggerRouteLeg{
				// runID() is monotonic when in.RunID is empty; the run create
				// still dedups on the bare idempotency key on redelivery.
				RunID:          s.runID(in.RunID),
				DeliveryID:     "delivery-" + event.EventID,
				IdempotencyKey: in.IdempotencyKey,
			}
		}
		outcome, err := s.dispatchTriggerRouteLeg(ctx, ws, binding, event, leg, in, now)
		if err != nil {
			return nil, err
		}
		// PrimaryRun is the first leg that HAS a run — a forbid/queue-gated
		// leg resolves without one and cannot back the legacy single-run wire.
		if result.PrimaryRun == nil && outcome.run != nil {
			result.PrimaryRun = outcome.run
		}
		result.Deliveries = append(result.Deliveries, outcome.delivery)
	}
	return result, nil
}

// triggerRouteLeg carries the per-binding identifiers for one fan-out leg.
type triggerRouteLeg struct {
	RunID          string
	DeliveryID     string
	IdempotencyKey string
}

// triggerRouteLegOutcome is one leg's dispatch resolution: the admitted run
// (nil when the concurrency policy resolved the leg without one) and its wire
// delivery, mirroring fleet-db's triggerRouteLegOutcome.
type triggerRouteLegOutcome struct {
	run      *domain.DriverRun
	delivery store.TriggerRouteDelivery
}

// triggerRouteLegResult shapes one leg's wire delivery (fleet-db's
// triggerRouteLegResult, on loomcli's camelCase-tagged store type).
func triggerRouteLegResult(leg triggerRouteLeg, bindingID, runID string, status domain.TriggerDeliveryStatus, rejectionReason string) store.TriggerRouteDelivery {
	return store.TriggerRouteDelivery{
		DeliveryID:      leg.DeliveryID,
		BindingID:       bindingID,
		RunID:           runID,
		Status:          status,
		RejectionReason: rejectionReason,
	}
}

// triggerRejectionConcurrencyForbid is the rejection_reason recorded on a
// delivery refused by the forbid concurrency policy (same wire constant as
// fleet-db).
const triggerRejectionConcurrencyForbid = "concurrency_forbid"

// matchTriggerRouteBindings computes the fan-out set for a route key: the
// exact-RouteKey binding (legacy single-binding lane) unioned with enabled
// bindings whose event_type_patterns match the key. The exact binding
// dispatches first; pattern matches follow in binding-id order. A disabled
// exact binding is skipped, not an error — zero total matches is the caller's
// not-found.
func (s *triggerRouteStore) matchTriggerRouteBindings(ctx context.Context, ws, routeKey string) ([]*domain.TriggerBinding, *domain.TriggerBinding, error) {
	exact, err := s.bindings.GetByRouteKey(ctx, ws, routeKey)
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) {
			return nil, nil, err
		}
		exact = nil
	}
	matched := make([]*domain.TriggerBinding, 0, 1)
	if exact != nil && exact.Enabled {
		matched = append(matched, exact)
	}
	enabled := true
	all, err := s.bindings.List(ctx, ws, store.TriggerBindingFilter{Enabled: &enabled})
	if err != nil {
		return nil, nil, err
	}
	patternMatched := make([]*domain.TriggerBinding, 0, len(all))
	for _, binding := range all {
		if exact != nil && binding.BindingID == exact.BindingID {
			continue
		}
		if trigger.MatchAny(binding.EventTypePatterns, routeKey) {
			patternMatched = append(patternMatched, binding)
		}
	}
	sort.Slice(patternMatched, func(i, j int) bool { return patternMatched[i].BindingID < patternMatched[j].BindingID })
	return append(matched, patternMatched...), exact, nil
}

// dispatchTriggerRouteLeg admits the queued run and records the delivery for
// one matched binding, enforcing the binding's concurrency policy against the
// rendered subject key (mirrors fleet-db's dispatchTriggerRouteLeg): allow
// and one_active_per_epic pass through, forbid/queue gate admission BEFORE
// any run exists, and replace supersedes queued siblings AFTER the new run is
// durable. The run create dedups on the leg's idempotency key and the
// delivery create is first-writer-wins on the deterministic delivery id, so a
// retry after partial failure heals the leg. The route store's mutex
// serializes legs end-to-end — the in-memory twin of the atomicity fleet-db's
// Lua subject gate provides, closing the race where two concurrent dispatches
// both observe a free subject.
func (s *triggerRouteStore) dispatchTriggerRouteLeg(ctx context.Context, ws string, binding *domain.TriggerBinding, event *domain.TriggerEvent, leg triggerRouteLeg, in store.TriggerRouteDispatch, now time.Time) (*triggerRouteLegOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	subjectKey := renderTriggerSubjectKey(binding, event, in.SubjectAttrs)
	if outcome, handled, err := s.gateTriggerLegConcurrency(ctx, ws, binding, event, leg, subjectKey, now); handled || err != nil {
		return outcome, err
	}
	run, err := s.runs.Create(ctx, store.DriverRunCreate{
		WorkspaceKey:     ws,
		RunID:            leg.RunID,
		DriverID:         binding.DriverID,
		DriverVersionID:  binding.DriverVersionID,
		Entrypoint:       binding.TargetEntrypoint,
		SourceKind:       binding.SourceKind,
		SourceRef:        event.EventID,
		EpicID:           in.EpicID,
		TriggerBindingID: binding.BindingID,
		IdempotencyKey:   leg.IdempotencyKey,
		Payload:          in.Payload,
	})
	if err != nil {
		return nil, err
	}
	deliveryErr := s.deliveries.create(&domain.TriggerDelivery{
		WorkspaceKey:     ws,
		DeliveryID:       leg.DeliveryID,
		TriggerEventID:   event.EventID,
		TriggerBindingID: binding.BindingID,
		SubjectKey:       subjectKey,
		Status:           domain.TriggerDeliveryDispatched,
		DriverRunID:      run.RunID,
		Attempt:          1,
		CreatedAt:        now,
		UpdatedAt:        now,
	})
	if deliveryErr != nil {
		if errors.Is(deliveryErr, domain.ErrAlreadyExists) {
			return s.healTriggerRouteLeg(ctx, ws, binding, leg, run), nil
		}
		return nil, deliveryErr
	}
	// Subject-level supersede only fires when the delivery was freshly
	// written — an idempotent redelivery must not re-collapse queued siblings.
	status := domain.TriggerDeliveryDispatched
	if binding.ConcurrencyPolicy == domain.TriggerBindingConcurrencyReplace {
		status = s.applyTriggerReplacePolicy(ctx, ws, binding, run, subjectKey)
	}
	return &triggerRouteLegOutcome{run: run, delivery: triggerRouteLegResult(leg, binding.BindingID, run.RunID, status, "")}, nil
}

// gateTriggerLegConcurrency enforces the forbid/queue admission policies for
// one leg before any run exists (fleet-db's gateTriggerLegConcurrency; the
// caller's route mutex stands in for the atomic Redis subject gate).
// handled=true means the leg was fully resolved here without a run: forbid
// writes a rejected delivery (rejection_reason concurrency_forbid), queue
// holds a delivery with next_retry_at = now + retry_backoff_seconds so
// the retry sweeper re-attempts admission once the subject frees. An
// idempotency-key hit on an existing run bypasses the gate entirely — the leg
// was already admitted and the run/delivery creates heal it. Legs with no
// subject key or other policies pass through unchanged.
func (s *triggerRouteStore) gateTriggerLegConcurrency(ctx context.Context, ws string, binding *domain.TriggerBinding, event *domain.TriggerEvent, leg triggerRouteLeg, subjectKey string, now time.Time) (outcome *triggerRouteLegOutcome, handled bool, err error) {
	policy := binding.ConcurrencyPolicy
	if policy != domain.TriggerBindingConcurrencyForbid && policy != domain.TriggerBindingConcurrencyQueue {
		return nil, false, nil
	}
	if subjectKey == "" {
		return nil, false, nil
	}
	if leg.IdempotencyKey != "" && s.runs.hasIdempotencyKey(ws, leg.IdempotencyKey) {
		return nil, false, nil
	}
	if busy := s.busySubjectRun(ctx, ws, binding.BindingID, subjectKey, leg.RunID); busy == "" {
		return nil, false, nil
	}
	delivery := &domain.TriggerDelivery{
		WorkspaceKey:     ws,
		DeliveryID:       leg.DeliveryID,
		TriggerEventID:   event.EventID,
		TriggerBindingID: binding.BindingID,
		SubjectKey:       subjectKey,
		Attempt:          1,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if policy == domain.TriggerBindingConcurrencyForbid {
		delivery.Status = domain.TriggerDeliveryRejected
		delivery.RejectionReason = triggerRejectionConcurrencyForbid
	} else {
		delivery.Status = domain.TriggerDeliveryHeld
		next := now.Add(triggerBindingRetryBackoff(binding))
		delivery.NextRetryAt = &next
	}
	if err := s.deliveries.create(delivery); err != nil {
		if errors.Is(err, domain.ErrAlreadyExists) {
			// Redelivery of a leg already held/rejected (or dispatched
			// before the subject got busy again): report the recorded state.
			if existing, gerr := s.deliveries.Get(ctx, ws, leg.DeliveryID); gerr == nil {
				return &triggerRouteLegOutcome{delivery: triggerRouteLegResult(leg, binding.BindingID, existing.DriverRunID, existing.Status, existing.RejectionReason)}, true, nil
			}
		}
		return nil, true, err
	}
	return &triggerRouteLegOutcome{delivery: triggerRouteLegResult(leg, binding.BindingID, "", delivery.Status, delivery.RejectionReason)}, true, nil
}

// busySubjectRun reports the run keeping a (binding, subject key) busy: a
// dispatched delivery for the pair whose run is still queued or running. The
// delivery is the only record carrying the rendered subject key, so busy
// detection walks deliveries — the functional twin of fleet-db's subject
// queued-ZSET + active-run check.
func (s *triggerRouteStore) busySubjectRun(ctx context.Context, ws, bindingID, subjectKey, excludeRunID string) string {
	dispatched, err := s.deliveries.List(ctx, ws, store.TriggerDeliveryFilter{
		TriggerBindingID: bindingID,
		Status:           domain.TriggerDeliveryDispatched,
	})
	if err != nil {
		return ""
	}
	for _, delivery := range dispatched {
		if delivery.SubjectKey != subjectKey || delivery.DriverRunID == "" || delivery.DriverRunID == excludeRunID {
			continue
		}
		run, err := s.runs.Get(ctx, ws, delivery.DriverRunID)
		if err != nil {
			continue
		}
		if run.Status == domain.DriverRunQueued || run.Status == domain.DriverRunRunning {
			return run.RunID
		}
	}
	return ""
}

// healTriggerRouteLeg resolves a leg whose delivery already exists (fleet-db's
// healTriggerRouteLeg): an idempotent redelivery of a dispatched (or
// since-superseded) leg reports the recorded status unchanged, while a held
// queue-policy delivery whose subject has freed is promoted to dispatched
// against the newly admitted run — the promotion path the retry sweeper
// drives.
func (s *triggerRouteStore) healTriggerRouteLeg(ctx context.Context, ws string, binding *domain.TriggerBinding, leg triggerRouteLeg, run *domain.DriverRun) *triggerRouteLegOutcome {
	existing, err := s.deliveries.Get(ctx, ws, leg.DeliveryID)
	if err != nil {
		// The conflicting delivery vanished between writes; the run is
		// durable either way, report the dispatched leg.
		return &triggerRouteLegOutcome{run: run, delivery: triggerRouteLegResult(leg, binding.BindingID, run.RunID, domain.TriggerDeliveryDispatched, "")}
	}
	if existing.Status == domain.TriggerDeliveryHeld {
		promoted, perr := s.deliveries.UpdateResult(ctx, ws, leg.DeliveryID, store.TriggerDeliveryResultUpdate{
			Status:      domain.TriggerDeliveryDispatched,
			Attempt:     existing.Attempt + 1,
			DriverRunID: run.RunID,
		})
		if perr == nil {
			return &triggerRouteLegOutcome{run: run, delivery: triggerRouteLegResult(leg, binding.BindingID, run.RunID, promoted.Status, "")}
		}
		slog.Warn("queue policy: held delivery promotion failed", "err", perr, "delivery", leg.DeliveryID, "run", run.RunID)
	}
	return &triggerRouteLegOutcome{run: run, delivery: triggerRouteLegResult(leg, binding.BindingID, run.RunID, existing.Status, existing.RejectionReason)}
}

// applyTriggerReplacePolicy collapses the subject's queued runs to the newest
// one after a fresh replace-policy admission (fleet-db's
// applyTriggerReplacePolicy + SupersedeTriggerSubjectRuns), so a push storm
// leaves one queued run instead of N stale reviews. Candidates come from the
// rendered subject key on dispatched deliveries — never from event-subject
// matching. Losers are cancelled (queued only — running work finishes) and
// their deliveries transitioned to superseded for audit. Best-effort and
// post-admission: the new run is already durable, so a supersede failure
// never fails dispatch. Returns the status of THIS leg's delivery: superseded
// when a concurrent newer dispatch out-raced the freshly admitted run,
// dispatched otherwise.
func (s *triggerRouteStore) applyTriggerReplacePolicy(ctx context.Context, ws string, binding *domain.TriggerBinding, admitted *domain.DriverRun, subjectKey string) domain.TriggerDeliveryStatus {
	if subjectKey == "" {
		return domain.TriggerDeliveryDispatched
	}
	queued := s.queuedSubjectRuns(ctx, ws, binding.BindingID, subjectKey)
	if len(queued) == 0 {
		return domain.TriggerDeliveryDispatched
	}
	// Winner = newest queued run for the subject by persisted trigger-event
	// order. Dispatches can interleave around event creation, so run CreatedAt is
	// not a reliable freshness signal.
	winner := queued[0]
	for _, candidate := range queued[1:] {
		if triggerEventBefore(winner.event, candidate.event) {
			winner = candidate
		}
	}
	status := domain.TriggerDeliveryDispatched
	for _, candidate := range queued {
		run := candidate.run
		if run.RunID == winner.run.RunID {
			continue
		}
		if !s.runs.cancelQueuedForSupersede(ws, run.RunID, "superseded by "+winner.run.RunID+" for "+subjectKey) {
			continue // claimed or removed between list and cancel — fine
		}
		s.markTriggerRunDeliveriesSuperseded(ctx, ws, run)
		if run.RunID == admitted.RunID {
			status = domain.TriggerDeliverySuperseded
		}
	}
	return status
}

// queuedSubjectRuns resolves the still-queued runs dispatched for one
// (binding, rendered subject key), via the deliveries that carry the key.
type queuedSubjectRun struct {
	run   *domain.DriverRun
	event *domain.TriggerEvent
}

func (s *triggerRouteStore) queuedSubjectRuns(ctx context.Context, ws, bindingID, subjectKey string) []queuedSubjectRun {
	dispatched, err := s.deliveries.List(ctx, ws, store.TriggerDeliveryFilter{
		TriggerBindingID: bindingID,
		Status:           domain.TriggerDeliveryDispatched,
	})
	if err != nil {
		return nil
	}
	seen := make(map[string]bool, len(dispatched))
	queued := make([]queuedSubjectRun, 0, len(dispatched))
	for _, delivery := range dispatched {
		if delivery.SubjectKey != subjectKey || delivery.DriverRunID == "" || seen[delivery.DriverRunID] {
			continue
		}
		seen[delivery.DriverRunID] = true
		run, err := s.runs.Get(ctx, ws, delivery.DriverRunID)
		if err != nil || run.Status != domain.DriverRunQueued {
			continue
		}
		event, err := s.events.Get(ctx, ws, delivery.TriggerEventID)
		if err != nil {
			continue
		}
		queued = append(queued, queuedSubjectRun{run: run, event: event})
	}
	return queued
}

// markTriggerRunDeliveriesSuperseded transitions the deliveries that
// dispatched a now-superseded run to status superseded, keeping the audit
// trail consistent with the cancelled run (fleet-db's
// markTriggerRunDeliveriesSuperseded). lost.SourceRef is the trigger event
// id, the same linkage fleet-db filters on.
func (s *triggerRouteStore) markTriggerRunDeliveriesSuperseded(ctx context.Context, ws string, lost *domain.DriverRun) {
	deliveries, err := s.deliveries.List(ctx, ws, store.TriggerDeliveryFilter{TriggerEventID: lost.SourceRef})
	if err != nil {
		return
	}
	for _, delivery := range deliveries {
		if delivery.DriverRunID != lost.RunID || delivery.Status == domain.TriggerDeliverySuperseded {
			continue
		}
		if _, err := s.deliveries.UpdateResult(ctx, ws, delivery.DeliveryID, store.TriggerDeliveryResultUpdate{
			Status:     domain.TriggerDeliverySuperseded,
			Attempt:    delivery.Attempt,
			ErrorClass: "superseded",
		}); err != nil {
			slog.Warn("supersede: delivery transition failed", "err", err, "delivery", delivery.DeliveryID, "run", lost.RunID)
		}
	}
}

func triggerEventBefore(a, b *domain.TriggerEvent) bool {
	if a == nil || b == nil {
		return false
	}
	if a.ReceivedAt.Before(b.ReceivedAt) {
		return true
	}
	if b.ReceivedAt.Before(a.ReceivedAt) {
		return false
	}
	aSeq, aOK := triggerEventSequence(a.EventID)
	bSeq, bOK := triggerEventSequence(b.EventID)
	if aOK && bOK && aSeq != bSeq {
		return aSeq < bSeq
	}
	return a.EventID < b.EventID
}

func triggerEventSequence(id string) (int64, bool) {
	seq, err := strconv.ParseInt(strings.TrimPrefix(id, "event-"), 10, 64)
	return seq, err == nil
}

// triggerBindingRetryBackoff resolves the binding's retry backoff used for
// held (queue-policy) deliveries, defaulting defensively for records written
// before the retry fields existed (mirrors fleet-db).
func triggerBindingRetryBackoff(binding *domain.TriggerBinding) time.Duration {
	seconds := binding.RetryBackoffSeconds
	if seconds <= 0 {
		seconds = domain.DefaultTriggerRetryBackoffSeconds
	}
	return time.Duration(seconds) * time.Second
}

// renderTriggerSubjectKey renders the binding's concurrency subject key for a
// dispatched event (fleet-db's renderTriggerSubjectKey): the
// subject_key_template output, or the default "<binding_id>|<subject_ref>"
// key (empty when the event has no subject_ref — such deliveries carry no
// concurrency subject). attrs is the adapter-enriched subject attribute map
// from the dispatch input ({{attrs.X}} tokens); templates never read the raw
// payload. A render failure (e.g. a template referencing a missing attr)
// falls back to the default key with a warning instead of failing ingest: the
// default groups the delivery under the implicit per-binding subject scope,
// the conservative concurrency grouping.
func renderTriggerSubjectKey(binding *domain.TriggerBinding, event *domain.TriggerEvent, attrs map[string]string) string {
	in := trigger.SubjectInputs{
		WorkspaceKey: event.WorkspaceKey,
		BindingID:    binding.BindingID,
		EventType:    event.EventType,
		SubjectRef:   event.SubjectRef,
		ActorRef:     event.ActorRef,
		Attrs:        attrs,
	}
	subjectKey, err := trigger.RenderSubjectKey(binding.SubjectKeyTemplate, in)
	if err != nil {
		slog.Warn("subject key template render failed; using default subject key",
			"err", err, "binding", binding.BindingID, "event", event.EventID)
		subjectKey, _ = trigger.RenderSubjectKey("", in)
	}
	return subjectKey
}

// hasIdempotencyKey reports whether any run in the workspace already carries
// the idempotency key — the gate-bypass signal for redelivery healing.
func (s *driverRunStore) hasIdempotencyKey(ws, idempotencyKey string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, run := range s.items[ws] {
		if run.IdempotencyKey == idempotencyKey {
			return true
		}
	}
	return false
}

// shortTriggerHash derives the deterministic short id suffix for fan-out legs,
// mirroring fleet-db: stable across redeliveries because the event id itself
// dedups on the ingress idempotency key.
func shortTriggerHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return hex.EncodeToString(sum[:])[:12]
}

// compositeTriggerIdempotencyKey scopes the ingress idempotency key to one
// fan-out leg ({idempotencyKey}#{bindingID}) so each matched binding's run
// dedups independently. An absent ingress key stays absent — there is nothing
// to dedup on.
func compositeTriggerIdempotencyKey(idempotencyKey, bindingID string) string {
	if idempotencyKey == "" {
		return ""
	}
	return idempotencyKey + "#" + bindingID
}

// runID returns the caller-supplied id, or a generated monotonic one. fleet-db
// generates run ids server-side; the in-memory store does it here.
func (s *triggerRouteStore) runID(supplied string) string {
	if supplied != "" {
		return supplied
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	return fmt.Sprintf("run-%d", s.seq)
}

// dispatchTriggerEvent builds the TriggerEvent to persist, applying the same
// field defaults as fleet-db's dispatch path.
func dispatchTriggerEvent(ws string, binding *domain.TriggerBinding, in store.TriggerRouteDispatch, now time.Time) *domain.TriggerEvent {
	eventType := in.EventType
	if eventType == "" {
		eventType = "trigger_route_requested"
	}
	sourceEventID := in.SourceEventID
	if sourceEventID == "" {
		sourceEventID = in.IdempotencyKey
	}
	signatureStatus := in.SignatureStatus
	if signatureStatus == "" {
		signatureStatus = "not_applicable"
	}
	return &domain.TriggerEvent{
		WorkspaceKey:     ws,
		TriggerBindingID: binding.BindingID,
		SourceKind:       binding.SourceKind,
		SourceEventID:    sourceEventID,
		EventType:        eventType,
		SubjectRef:       in.SubjectRef,
		ActorRef:         in.ActorRef,
		// Structural provenance: route dispatch is the webhook ingest lane,
		// so the origin is always stamped external at hop depth 0 here —
		// never copied from caller input (mirrors fleet-db's stamping).
		Origin:           domain.TriggerEventOriginExternal,
		HopDepth:         0,
		OccurredAt:       now,
		ReceivedAt:       now,
		IdempotencyKey:   in.IdempotencyKey,
		RawPayloadRef:    in.RawPayloadRef,
		RawPayloadDigest: in.RawPayloadDigest,
		SignatureStatus:  signatureStatus,
		ReplayOfEventID:  in.ReplayOfEventID,
	}
}
