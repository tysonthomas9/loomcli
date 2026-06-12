package memstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
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
	mu     sync.RWMutex
	items  map[string]map[string]*domain.TriggerEvent // ws -> eventID -> event
	idempo map[string]map[string]string               // ws -> idempotencyKey -> eventID
	seq    int64
}

func newTriggerEventStore() *triggerEventStore {
	return &triggerEventStore{
		items:  make(map[string]map[string]*domain.TriggerEvent),
		idempo: make(map[string]map[string]string),
	}
}

var _ store.TriggerEventStore = (*triggerEventStore)(nil)

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
	if triggerDeliveryResultFinal(d) && update.Status != d.Status {
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
		run, err := s.dispatchTriggerRouteLeg(ctx, ws, binding, event, leg, in, now)
		if err != nil {
			return nil, err
		}
		if result.PrimaryRun == nil {
			result.PrimaryRun = run
		}
		result.Deliveries = append(result.Deliveries, store.TriggerRouteDelivery{
			DeliveryID: leg.DeliveryID,
			BindingID:  binding.BindingID,
			RunID:      run.RunID,
			Status:     domain.TriggerDeliveryDispatched,
		})
	}
	return result, nil
}

// triggerRouteLeg carries the per-binding identifiers for one fan-out leg.
type triggerRouteLeg struct {
	RunID          string
	DeliveryID     string
	IdempotencyKey string
}

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
// one matched binding. The run create dedups on the leg's idempotency key and
// the delivery create is first-writer-wins on the deterministic delivery id,
// so a retry after partial failure heals the leg. Supersede only fires when
// the delivery was freshly written — an idempotent redelivery must not
// re-collapse queued siblings.
func (s *triggerRouteStore) dispatchTriggerRouteLeg(ctx context.Context, ws string, binding *domain.TriggerBinding, event *domain.TriggerEvent, leg triggerRouteLeg, in store.TriggerRouteDispatch, now time.Time) (*domain.DriverRun, error) {
	run, err := s.runs.Create(ctx, store.DriverRunCreate{
		WorkspaceKey:    ws,
		RunID:           leg.RunID,
		DriverID:        binding.DriverID,
		DriverVersionID: binding.DriverVersionID,
		Entrypoint:      binding.TargetEntrypoint,
		SourceKind:      binding.SourceKind,
		SourceRef:       event.EventID,
		EpicID:          in.EpicID,
		IdempotencyKey:  leg.IdempotencyKey,
		Payload:         in.Payload,
	})
	if err != nil {
		return nil, err
	}
	deliveryErr := s.deliveries.create(&domain.TriggerDelivery{
		WorkspaceKey:     ws,
		DeliveryID:       leg.DeliveryID,
		TriggerEventID:   event.EventID,
		TriggerBindingID: binding.BindingID,
		Status:           domain.TriggerDeliveryDispatched,
		DriverRunID:      run.RunID,
		Attempt:          1,
		CreatedAt:        now,
		UpdatedAt:        now,
	})
	if deliveryErr != nil {
		if errors.Is(deliveryErr, domain.ErrAlreadyExists) {
			// Idempotent redelivery of an already-recorded leg.
			return run, nil
		}
		return nil, deliveryErr
	}
	// Subject-level supersede (mirrors fleet-db's dispatch path): when the
	// binding's concurrency policy is `replace` and this leg's delivery was
	// freshly recorded, collapse older still-queued runs for the same subject
	// so a push storm doesn't queue N stale reviews.
	if binding.ConcurrencyPolicy == domain.TriggerBindingConcurrencyReplace {
		if subject := strings.TrimSpace(in.SubjectRef); subject != "" {
			s.supersedeQueuedSiblingRuns(ctx, ws, binding, subject, run.RunID)
		}
	}
	return run, nil
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

// supersedeQueuedSiblingRuns cancels still-queued runs for the same
// (binding, subject) as keepRunID, matching each candidate by its source
// TriggerEvent. Best-effort: a run already claimed is left running.
func (s *triggerRouteStore) supersedeQueuedSiblingRuns(ctx context.Context, ws string, binding *domain.TriggerBinding, subject, keepRunID string) {
	queued, err := s.runs.List(ctx, ws, store.DriverRunFilter{DriverID: binding.DriverID, Status: domain.DriverRunQueued})
	if err != nil {
		return
	}
	for _, run := range queued {
		if run == nil || run.RunID == keepRunID {
			continue
		}
		event, err := s.events.Get(ctx, ws, run.SourceRef)
		if err != nil || event.TriggerBindingID != binding.BindingID || strings.TrimSpace(event.SubjectRef) != subject {
			continue
		}
		s.runs.cancelQueuedForSupersede(ws, run.RunID, "superseded by "+keepRunID+" for "+subject)
	}
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
