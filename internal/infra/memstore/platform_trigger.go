package memstore

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
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

// triggerDeliveryStore is the in-memory TriggerDelivery repository.
type triggerDeliveryStore struct {
	mu    sync.RWMutex
	items map[string]map[string]*domain.TriggerDelivery // ws -> deliveryID -> delivery
}

func newTriggerDeliveryStore() *triggerDeliveryStore {
	return &triggerDeliveryStore{items: make(map[string]map[string]*domain.TriggerDelivery)}
}

var _ store.TriggerDeliveryStore = (*triggerDeliveryStore)(nil)

func (s *triggerDeliveryStore) Get(_ context.Context, ws, deliveryID string) (*domain.TriggerDelivery, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	delivery, ok := s.items[ws][deliveryID]
	if !ok {
		return nil, fmt.Errorf("trigger delivery %q in workspace %q: %w", deliveryID, ws, domain.ErrNotFound)
	}
	out := *delivery
	return &out, nil
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
		clone := *delivery
		out = append(out, &clone)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
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
	stored := *delivery
	s.items[delivery.WorkspaceKey][delivery.DeliveryID] = &stored
	return nil
}

// triggerRouteStore implements the dispatch path against the in-memory stores,
// mirroring fleet-db's dispatchTriggerRouteRun: resolve enabled binding by route
// key, persist a TriggerEvent, enqueue a queued DriverRun, then record a
// TriggerDelivery linking the three. The writes are individually idempotent but
// not a single transaction (see store.TriggerRouteDispatcher); a failure after
// run creation returns an error and is healed on redelivery.
type triggerRouteStore struct {
	bindings   *triggerBindingStore
	events     *triggerEventStore
	deliveries *triggerDeliveryStore
	runs       *driverRunStore
	seq        int64
	mu         sync.Mutex
}

var _ store.TriggerRouteDispatcher = (*triggerRouteStore)(nil)

func (s *triggerRouteStore) DispatchTriggerRoute(ctx context.Context, ws, routeKey string, in store.TriggerRouteDispatch) (*domain.DriverRun, error) {
	binding, err := s.bindings.GetByRouteKey(ctx, ws, routeKey)
	if err != nil {
		return nil, err
	}
	if !binding.Enabled {
		return nil, fmt.Errorf("trigger binding route %q in workspace %q: %w", routeKey, ws, domain.ErrNotFound)
	}
	now := time.Now().UTC()
	event, _ := s.events.create(dispatchTriggerEvent(ws, binding, in, now))

	// runID() is monotonic when in.RunID is empty, so resolve it exactly once.
	requestedRunID := s.runID(in.RunID)
	run, err := s.runs.Create(ctx, store.DriverRunCreate{
		WorkspaceKey:    ws,
		RunID:           requestedRunID,
		DriverID:        binding.DriverID,
		DriverVersionID: binding.DriverVersionID,
		Entrypoint:      binding.TargetEntrypoint,
		SourceKind:      binding.SourceKind,
		SourceRef:       event.EventID,
		EpicID:          in.EpicID,
		IdempotencyKey:  in.IdempotencyKey,
		Payload:         in.Payload,
	})
	if err != nil {
		return nil, err
	}

	deliveryErr := s.deliveries.create(&domain.TriggerDelivery{
		WorkspaceKey:     ws,
		DeliveryID:       "delivery-" + event.EventID,
		TriggerEventID:   event.EventID,
		TriggerBindingID: binding.BindingID,
		Status:           domain.TriggerDeliveryDispatched,
		DriverRunID:      run.RunID,
		Attempt:          1,
		CreatedAt:        now,
		UpdatedAt:        now,
	})
	if deliveryErr != nil && !errors.Is(deliveryErr, domain.ErrAlreadyExists) {
		return nil, deliveryErr
	}
	// Subject-level supersede (mirrors fleet-db's dispatch path): for `replace`
	// bindings, keep the newest queued run for the subject and collapse only
	// older queued siblings. This remains safe for redelivery and concurrent
	// dispatches because the winner is derived from persisted TriggerEvent order,
	// not from the caller currently executing this loop.
	if binding.ConcurrencyPolicy == domain.TriggerBindingConcurrencyReplace {
		if subject := strings.TrimSpace(in.SubjectRef); subject != "" {
			s.supersedeQueuedRunsForSubject(ctx, ws, binding, subject)
		}
	}
	return run, nil
}

// supersedeQueuedRunsForSubject cancels still-queued runs older than the newest
// queued run for the same (binding, subject), matching each candidate by its
// source TriggerEvent. Best-effort: a run already claimed is left running.
func (s *triggerRouteStore) supersedeQueuedRunsForSubject(ctx context.Context, ws string, binding *domain.TriggerBinding, subject string) {
	queued, err := s.runs.List(ctx, ws, store.DriverRunFilter{DriverID: binding.DriverID, Status: domain.DriverRunQueued})
	if err != nil {
		return
	}
	type queuedSubjectRun struct {
		run   *domain.DriverRun
		event *domain.TriggerEvent
	}
	matches := make([]queuedSubjectRun, 0, len(queued))
	for _, run := range queued {
		if run == nil {
			continue
		}
		event, err := s.events.Get(ctx, ws, run.SourceRef)
		if err != nil || event.TriggerBindingID != binding.BindingID || strings.TrimSpace(event.SubjectRef) != subject {
			continue
		}
		matches = append(matches, queuedSubjectRun{run: run, event: event})
	}
	if len(matches) < 2 {
		return
	}
	newest := matches[0]
	for _, candidate := range matches[1:] {
		if triggerEventBefore(newest.event, candidate.event) {
			newest = candidate
		}
	}
	for _, candidate := range matches {
		if candidate.run.RunID == newest.run.RunID || !triggerEventBefore(candidate.event, newest.event) {
			continue
		}
		s.runs.cancelQueuedForSupersede(ws, candidate.run.RunID, "superseded by "+newest.run.RunID+" for "+subject)
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
		OccurredAt:       now,
		ReceivedAt:       now,
		IdempotencyKey:   in.IdempotencyKey,
		RawPayloadRef:    in.RawPayloadRef,
		RawPayloadDigest: in.RawPayloadDigest,
		SignatureStatus:  signatureStatus,
		ReplayOfEventID:  in.ReplayOfEventID,
	}
}
