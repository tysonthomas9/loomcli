package memstore

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// triggerEventStore is the in-memory TriggerEvent repository. It dedups on
// idempotency key the same way fleet-db's storage layer does: a create with a
// previously-seen key returns the existing event instead of inserting again.
type triggerEventStore struct {
	mu            sync.RWMutex
	items         map[string]map[string]*automation.Event // ws -> eventID -> event
	idempo        map[string]map[string]string            // ws -> idempotencyKey -> eventID
	notifications map[string]map[string]*awaitEventNotificationRow
	seq           int64
}

func newTriggerEventStore() *triggerEventStore {
	return &triggerEventStore{
		items:         make(map[string]map[string]*automation.Event),
		idempo:        make(map[string]map[string]string),
		notifications: make(map[string]map[string]*awaitEventNotificationRow),
	}
}

var (
	_ store.TriggerEventStore    = (*triggerEventStore)(nil)
	_ store.TriggerEventAppender = (*triggerEventStore)(nil)
)

func (s *triggerEventStore) Get(_ context.Context, ws, eventID string) (*automation.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	event, ok := s.items[ws][eventID]
	if !ok {
		return nil, fmt.Errorf("trigger event %q in workspace %q: %w", eventID, ws, domain.ErrNotFound)
	}
	out := *event
	return &out, nil
}

func (s *triggerEventStore) List(_ context.Context, ws string, filter store.TriggerEventFilter) ([]*automation.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*automation.Event, 0, len(s.items[ws]))
	for _, event := range s.items[ws] {
		if filter.SourceKind != "" && event.SourceKind != filter.SourceKind {
			continue
		}
		if filter.TriggerBindingID != "" && event.TriggerBindingID != filter.TriggerBindingID {
			continue
		}
		if filter.SubjectRef != "" && event.SubjectRef != filter.SubjectRef {
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
func (s *triggerEventStore) AppendTriggerEvent(_ context.Context, event *automation.Event) (*automation.Event, error) {
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
		s.items[event.WorkspaceKey] = make(map[string]*automation.Event)
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
func (s *triggerEventStore) create(event *automation.Event) (*automation.Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[event.WorkspaceKey] == nil {
		s.items[event.WorkspaceKey] = make(map[string]*automation.Event)
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
	items    map[string]map[string]*automation.Delivery // ws -> deliveryID -> delivery
	bindings *triggerBindingStore
	// failCreate, when set, lets tests inject delivery write failures to
	// exercise per-leg redelivery healing (mirrors fleet-db's fake-store
	// hook). Always nil in production wiring.
	failCreate func(*automation.Delivery) error
}

func newTriggerDeliveryStore(bindings *triggerBindingStore) *triggerDeliveryStore {
	return &triggerDeliveryStore{
		items:    make(map[string]map[string]*automation.Delivery),
		bindings: bindings,
	}
}

var _ store.TriggerDeliveryStore = (*triggerDeliveryStore)(nil)

func (s *triggerDeliveryStore) Get(_ context.Context, ws, deliveryID string) (*automation.Delivery, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	delivery, ok := s.items[ws][deliveryID]
	if !ok {
		return nil, fmt.Errorf("trigger delivery %q in workspace %q: %w", deliveryID, ws, domain.ErrNotFound)
	}
	return cloneTriggerDelivery(delivery), nil
}

func (s *triggerDeliveryStore) List(_ context.Context, ws string, filter store.TriggerDeliveryFilter) ([]*automation.Delivery, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*automation.Delivery, 0, len(s.items[ws]))
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
func (s *triggerDeliveryStore) ListDue(_ context.Context, ws string, filter store.TriggerDeliveryDueFilter) ([]*automation.Delivery, error) {
	now := filter.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*automation.Delivery, 0, len(s.items[ws]))
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
func (s *triggerDeliveryStore) UpdateResult(ctx context.Context, ws, deliveryID string, update store.TriggerDeliveryResultUpdate) (*automation.Delivery, error) {
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
		return automation.DefaultTriggerRetryMaxAttempts, nil
	}
	if err != nil {
		return 0, err
	}
	if binding.RetryMaxAttempts <= 0 {
		return automation.DefaultTriggerRetryMaxAttempts, nil
	}
	return binding.RetryMaxAttempts, nil
}

// applyTriggerDeliveryResult mutates the delivery with one attempt outcome
// (mirrors fleet-db's applyTriggerDeliveryResult). Final deliveries reject
// transitions to a different status; re-applying the same status stays
// idempotent.
func applyTriggerDeliveryResult(d *automation.Delivery, update store.TriggerDeliveryResultUpdate, maxAttempts int, now time.Time) error {
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
	if d.Status == automation.DeliveryFailed && d.Attempt >= maxAttempts {
		d.ErrorClass = automation.TriggerDeliveryErrorRetriesExhausted
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
func triggerDeliveryInDueIndex(d *automation.Delivery) bool {
	switch d.Status {
	case automation.DeliveryHeld:
		return true
	case automation.DeliveryFailed:
		return d.ErrorClass != automation.TriggerDeliveryErrorRetriesExhausted
	default:
		return false
	}
}

// triggerDeliverySupersedeTransition permits the one out-of-final transition
// the replace concurrency policy needs (mirrors fleet-db): a dispatched
// delivery whose queued run is later superseded by a newer event for the
// same subject.
func triggerDeliverySupersedeTransition(from, to automation.DeliveryStatus) bool {
	return from == automation.DeliveryDispatched && to == automation.DeliverySuperseded
}

// triggerDeliveryResultFinal reports whether the delivery reached a state
// the retry sweeper must not move it out of.
func triggerDeliveryResultFinal(d *automation.Delivery) bool {
	switch d.Status {
	case automation.DeliveryDispatched, automation.DeliveryRejected,
		automation.DeliveryDuplicate, automation.DeliverySuperseded,
		automation.DeliveryReplayed:
		return true
	case automation.DeliveryFailed:
		return d.ErrorClass == automation.TriggerDeliveryErrorRetriesExhausted
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
func cloneTriggerDelivery(d *automation.Delivery) *automation.Delivery {
	out := *d
	out.NextRetryAt = clonePtr(d.NextRetryAt)
	return &out
}

// create inserts a delivery, returning domain.ErrAlreadyExists when one with
// the same ID is already present (so replays are idempotent).
func (s *triggerDeliveryStore) create(delivery *automation.Delivery) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.items[delivery.WorkspaceKey] == nil {
		s.items[delivery.WorkspaceKey] = make(map[string]*automation.Delivery)
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
