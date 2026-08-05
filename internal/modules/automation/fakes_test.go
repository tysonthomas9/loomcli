package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/workflowcatalog"
	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

type fakeReservationRecord struct {
	fingerprint string
	result      *ReservationResult
}

const (
	testRunFinishedEventType           = "run.finished"
	testRunFinishedActorRef            = "system"
	testRunFinishedSourceEventIDPrefix = "run-finished:"
	testSourceKindExecution            = "execution"
)

// fakeEventTrustPolicy is deliberately local to Automation tests: the core's
// tests exercise its consumer-owned port without importing the legacy
// Execution implementation that production composition supplies.
type fakeEventTrustPolicy struct{}

func (fakeEventTrustPolicy) EligibleForAdmission(eventType, origin, sourceKind, actorRef, sourceEventID string) bool {
	if origin != string(EventOriginSystem) &&
		(actorRef == testRunFinishedActorRef || strings.HasPrefix(actorRef, testRunFinishedActorRef+":")) {
		return false
	}
	return eventType != testRunFinishedEventType ||
		(origin == string(EventOriginSystem) &&
			(sourceKind == testSourceKindExecution || sourceKind == SourceKindInternal) &&
			actorRef == testRunFinishedActorRef &&
			strings.HasPrefix(sourceEventID, testRunFinishedSourceEventIDPrefix))
}

type fakePersistence struct {
	mu sync.Mutex

	bindings               map[string]*Binding
	bindingOrder           []string
	bindingSetRevision     uint64
	bumpRevisionAfterMatch bool
	matchCalls             int
	matchErr               error
	events                 map[string]*Event
	deliveries             map[string]*Delivery
	reservations           map[string]*fakeReservationRecord

	reserveCalls              int
	lastReservation           EventReservation
	transitionCalls           []DeliveryTransition
	deleteCalls               int
	unmanagedReplaceCalls     int
	unmanagedDeleteCalls      int
	managedReplaceCalls       int
	managedDeleteCalls        int
	managedMutationHook       func(*fakePersistence)
	managedDeleteErr          error
	managedDeleteCommitOnErr  bool
	managedDeleteRecreate     *Binding
	commitThenError           bool
	replayMisses              int
	mutateReserveResult       func(*ReservationResult)
	transitionCommitThenError bool
	forceStaleTransition      bool
	nextEvent                 int
	nextDelivery              int
}

func newFakePersistence() *fakePersistence {
	return &fakePersistence{
		bindings: make(map[string]*Binding), events: make(map[string]*Event),
		deliveries: make(map[string]*Delivery), reservations: make(map[string]*fakeReservationRecord),
	}
}

func bindingMapKey(workspace, bindingID string) string   { return workspace + "\x00" + bindingID }
func eventMapKey(workspace, eventID string) string       { return workspace + "\x00" + eventID }
func deliveryMapKey(workspace, deliveryID string) string { return workspace + "\x00" + deliveryID }
func reservationMapKey(workspace, idempotencyKey string) string {
	return workspace + "\x00" + idempotencyKey
}

func (p *fakePersistence) seedBinding(binding *Binding) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := bindingMapKey(binding.WorkspaceKey, binding.BindingID)
	if _, exists := p.bindings[key]; !exists {
		p.bindingOrder = append(p.bindingOrder, key)
	}
	p.bindings[key] = cloneBinding(binding)
	p.bindingSetRevision++
}

func (p *fakePersistence) MatchBindings(_ context.Context, workspace, routeKey string) (*BindingMatchSnapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.matchCalls++
	if p.matchErr != nil {
		return nil, p.matchErr
	}
	bindings := make([]*Binding, 0, len(p.bindingOrder))
	for _, key := range p.bindingOrder {
		binding := p.bindings[key]
		if binding != nil && binding.WorkspaceKey == workspace && binding.Enabled {
			bindings = append(bindings, cloneBinding(binding))
		}
	}
	revision := p.bindingSetRevision
	if revision == 0 {
		revision = 1
	}
	snapshot := &BindingMatchSnapshot{
		WorkspaceKey: workspace, RouteKey: routeKey, BindingSetRevision: revision, Bindings: bindings,
	}
	if p.bumpRevisionAfterMatch {
		p.bumpRevisionAfterMatch = false
		p.bindingSetRevision++
	}
	return snapshot, nil
}

func (p *fakePersistence) seedEvent(event *Event) {
	p.mu.Lock()
	defer p.mu.Unlock()
	event = cloneEvent(event)
	stampEventTimes(event)
	p.events[eventMapKey(event.WorkspaceKey, event.EventID)] = event
}

func (p *fakePersistence) CreateBinding(_ context.Context, binding *Binding) (*Binding, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := bindingMapKey(binding.WorkspaceKey, binding.BindingID)
	if _, exists := p.bindings[key]; exists {
		return nil, ErrConflict
	}
	for _, existing := range p.bindings {
		if existing.WorkspaceKey == binding.WorkspaceKey && existing.RouteKey != "" && existing.RouteKey == binding.RouteKey {
			return nil, ErrConflict
		}
	}
	p.bindings[key] = cloneBinding(binding)
	p.bindingOrder = append(p.bindingOrder, key)
	p.bindingSetRevision++
	return cloneBinding(binding), nil
}

func (p *fakePersistence) GetBinding(_ context.Context, workspace, bindingID string) (*Binding, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	binding := p.bindings[bindingMapKey(workspace, bindingID)]
	if binding == nil {
		return nil, ErrNotFound
	}
	return cloneBinding(binding), nil
}

func (p *fakePersistence) ListBindings(_ context.Context, workspace string, filter BindingFilter) ([]*Binding, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*Binding, 0)
	for _, key := range p.bindingOrder {
		binding := p.bindings[key]
		if binding == nil || binding.WorkspaceKey != workspace {
			continue
		}
		if filter.SourceKind != "" && binding.SourceKind != filter.SourceKind ||
			filter.RouteKey != "" && binding.RouteKey != filter.RouteKey ||
			filter.DriverID != "" && binding.DriverID != filter.DriverID ||
			filter.TargetAgentServiceID != "" && binding.TargetAgentServiceID != filter.TargetAgentServiceID ||
			filter.Enabled != nil && binding.Enabled != *filter.Enabled {
			continue
		}
		out = append(out, cloneBinding(binding))
		if filter.Limit > 0 && len(out) == filter.Limit {
			break
		}
	}
	return out, nil
}

func (p *fakePersistence) UpdateBinding(_ context.Context, binding *Binding) (*Binding, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := bindingMapKey(binding.WorkspaceKey, binding.BindingID)
	if p.bindings[key] == nil {
		return nil, ErrNotFound
	}
	p.bindings[key] = cloneBinding(binding)
	p.bindingSetRevision++
	return cloneBinding(binding), nil
}

func (p *fakePersistence) DeleteBinding(_ context.Context, workspace, bindingID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := bindingMapKey(workspace, bindingID)
	if p.bindings[key] == nil {
		return ErrNotFound
	}
	p.deleteCalls++
	delete(p.bindings, key)
	p.bindingSetRevision++
	return nil
}

func (p *fakePersistence) ReplaceUnmanagedBinding(_ context.Context, replacement UnmanagedBindingReplacement) (*Binding, error) {
	p.runManagedMutationHook()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.unmanagedReplaceCalls++
	expected := replacement.Expected
	current := p.bindings[bindingMapKey(expected.WorkspaceKey, expected.BindingID)]
	if !unmanagedBindingMatchesSnapshot(current, expected) || replacement.Binding == nil ||
		replacement.Binding.WorkspaceKey != expected.WorkspaceKey || replacement.Binding.BindingID != expected.BindingID ||
		strings.TrimSpace(replacement.Binding.TargetAgentServiceID) != "" ||
		!replacement.Binding.CreatedAt.Equal(expected.ExpectedCreatedAt) ||
		!replacement.Binding.UpdatedAt.After(expected.ExpectedUpdatedAt) {
		return nil, ErrManagedBinding
	}
	for key, binding := range p.bindings {
		if key != bindingMapKey(expected.WorkspaceKey, expected.BindingID) && binding.WorkspaceKey == expected.WorkspaceKey &&
			binding.RouteKey != "" && binding.RouteKey == replacement.Binding.RouteKey {
			return nil, ErrConflict
		}
	}
	p.bindings[bindingMapKey(expected.WorkspaceKey, expected.BindingID)] = cloneBinding(replacement.Binding)
	p.bindingSetRevision++
	return cloneBinding(replacement.Binding), nil
}

func (p *fakePersistence) DeleteUnmanagedBindingIfUnchanged(_ context.Context, expected UnmanagedBindingSnapshot) error {
	p.runManagedMutationHook()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.unmanagedDeleteCalls++
	key := bindingMapKey(expected.WorkspaceKey, expected.BindingID)
	current := p.bindings[key]
	if !unmanagedBindingMatchesSnapshot(current, expected) || current.Enabled {
		return ErrManagedBinding
	}
	delete(p.bindings, key)
	p.deleteCalls++
	p.bindingSetRevision++
	return nil
}

func (p *fakePersistence) runManagedMutationHook() {
	p.mu.Lock()
	hook := p.managedMutationHook
	p.managedMutationHook = nil
	p.mu.Unlock()
	if hook != nil {
		hook(p)
	}
}

func (p *fakePersistence) CreateManagedBinding(ctx context.Context, binding *Binding) (*Binding, error) {
	if binding == nil || strings.TrimSpace(binding.TargetAgentServiceID) == "" {
		return nil, ErrManagedBinding
	}
	return p.CreateBinding(ctx, binding)
}

func (p *fakePersistence) ReplaceManagedBinding(_ context.Context, replacement ManagedBindingReplacement) (*Binding, error) {
	p.runManagedMutationHook()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.managedReplaceCalls++
	expected := replacement.Expected
	current := p.bindings[bindingMapKey(expected.WorkspaceKey, expected.BindingID)]
	if !managedSnapshotMatches(current, expected) || replacement.Binding == nil ||
		replacement.Binding.WorkspaceKey != expected.WorkspaceKey || replacement.Binding.BindingID != expected.BindingID ||
		replacement.Binding.TargetAgentServiceID != expected.ExpectedTargetAgentServiceID ||
		!replacement.Binding.CreatedAt.Equal(expected.ExpectedCreatedAt) ||
		!replacement.Binding.UpdatedAt.After(expected.ExpectedUpdatedAt) {
		return nil, ErrManagedBinding
	}
	for key, binding := range p.bindings {
		if key != bindingMapKey(expected.WorkspaceKey, expected.BindingID) && binding.WorkspaceKey == expected.WorkspaceKey &&
			binding.RouteKey != "" && binding.RouteKey == replacement.Binding.RouteKey {
			return nil, ErrConflict
		}
	}
	p.bindings[bindingMapKey(expected.WorkspaceKey, expected.BindingID)] = cloneBinding(replacement.Binding)
	p.bindingSetRevision++
	return cloneBinding(replacement.Binding), nil
}

func (p *fakePersistence) DeleteManagedBindingIfUnchanged(_ context.Context, expected ManagedBindingSnapshot) error {
	p.runManagedMutationHook()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.managedDeleteCalls++
	key := bindingMapKey(expected.WorkspaceKey, expected.BindingID)
	current := p.bindings[key]
	if !managedSnapshotMatches(current, expected) || current.Enabled {
		return ErrManagedBinding
	}
	if p.managedDeleteErr != nil && !p.managedDeleteCommitOnErr {
		return p.managedDeleteErr
	}
	delete(p.bindings, key)
	p.bindingSetRevision++
	if p.managedDeleteRecreate != nil {
		p.bindings[key] = cloneBinding(p.managedDeleteRecreate)
		p.bindingSetRevision++
	}
	return p.managedDeleteErr
}

func managedSnapshotMatches(binding *Binding, expected ManagedBindingSnapshot) bool {
	return binding != nil && binding.WorkspaceKey == expected.WorkspaceKey && binding.BindingID == expected.BindingID &&
		binding.TargetAgentServiceID == expected.ExpectedTargetAgentServiceID && binding.RouteKey == expected.ExpectedRouteKey &&
		binding.CreatedAt.Equal(expected.ExpectedCreatedAt) && binding.UpdatedAt.Equal(expected.ExpectedUpdatedAt)
}

func (p *fakePersistence) GetEvent(_ context.Context, workspace, eventID string) (*Event, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	event := p.events[eventMapKey(workspace, eventID)]
	if event == nil {
		return nil, ErrNotFound
	}
	return cloneEvent(event), nil
}

func (p *fakePersistence) ListEvents(_ context.Context, workspace string, filter EventFilter) ([]*Event, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	keys := make([]string, 0, len(p.events))
	for key := range p.events {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]*Event, 0)
	for _, key := range keys {
		event := p.events[key]
		if event.WorkspaceKey != workspace || filter.BindingID != "" && event.TriggerBindingID != filter.BindingID ||
			filter.SourceKind != "" && event.SourceKind != filter.SourceKind || filter.Origin != "" && event.Origin != filter.Origin {
			continue
		}
		out = append(out, cloneEvent(event))
		if filter.Limit > 0 && len(out) == filter.Limit {
			break
		}
	}
	return out, nil
}

func (p *fakePersistence) GetDelivery(_ context.Context, workspace, deliveryID string) (*Delivery, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delivery := p.deliveries[deliveryMapKey(workspace, deliveryID)]
	if delivery == nil {
		return nil, ErrNotFound
	}
	return cloneDelivery(delivery), nil
}

func (p *fakePersistence) ListDeliveries(_ context.Context, workspace string, filter DeliveryFilter) ([]*Delivery, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	keys := make([]string, 0, len(p.deliveries))
	for key := range p.deliveries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]*Delivery, 0)
	for _, key := range keys {
		delivery := p.deliveries[key]
		if delivery.WorkspaceKey != workspace || filter.EventID != "" && delivery.TriggerEventID != filter.EventID ||
			filter.BindingID != "" && delivery.TriggerBindingID != filter.BindingID || filter.Status != "" && delivery.Status != filter.Status {
			continue
		}
		out = append(out, cloneDelivery(delivery))
		if filter.Limit > 0 && len(out) == filter.Limit {
			break
		}
	}
	return out, nil
}

func (p *fakePersistence) ReserveEvent(_ context.Context, reservation EventReservation) (*ReservationResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reserveCalls++
	p.lastReservation = EventReservation{
		Event: cloneEvent(reservation.Event), ReplayOnly: reservation.ReplayOnly,
		Deliveries: cloneDeliveryReservations(reservation.Deliveries),
		Payload:    cloneRawMessage(reservation.Payload), SubjectAttrs: cloneStringMap(reservation.SubjectAttrs),
		EpicID: reservation.EpicID, MatchedBindingIDs: append([]string(nil), reservation.MatchedBindingIDs...),
		BindingSetRevision: reservation.BindingSetRevision,
		CatalogGuards:      append([]CatalogGuard(nil), reservation.CatalogGuards...), Fingerprint: reservation.Fingerprint,
	}
	key := reservationMapKey(reservation.Event.WorkspaceKey, reservation.Event.IdempotencyKey)
	if existing := p.reservations[key]; existing != nil {
		if reservation.ReplayOnly && p.replayMisses > 0 {
			p.replayMisses--
			return nil, ErrAdmissionReplayNotFound
		}
		if existing.fingerprint != reservation.Fingerprint {
			return nil, ErrConflict
		}
		out := cloneReservationResult(existing.result)
		out.Replayed = true
		if p.mutateReserveResult != nil {
			p.mutateReserveResult(out)
			p.mutateReserveResult = nil
		}
		return out, nil
	}
	if reservation.ReplayOnly {
		return nil, ErrAdmissionReplayNotFound
	}
	if reservation.BindingSetRevision == 0 || reservation.BindingSetRevision != p.bindingSetRevision {
		return nil, ErrConflict
	}
	p.nextEvent++
	event := cloneEvent(reservation.Event)
	event.EventID = fmt.Sprintf("evt-%d", p.nextEvent)
	stampEventTimes(event)
	p.events[eventMapKey(event.WorkspaceKey, event.EventID)] = event
	result := &ReservationResult{
		Event: cloneEvent(event), Payload: cloneRawMessage(reservation.Payload),
		SubjectAttrs: cloneStringMap(reservation.SubjectAttrs), EpicID: reservation.EpicID,
	}
	for _, requested := range reservation.Deliveries {
		p.nextDelivery++
		delivery := &Delivery{
			WorkspaceKey: event.WorkspaceKey, DeliveryID: fmt.Sprintf("del-%d", p.nextDelivery),
			TriggerEventID: event.EventID, TriggerBindingID: requested.BindingID,
			Status: requested.Status, SubjectKey: requested.SubjectKey, Attempt: 1,
			RejectionReason: requested.RejectionReason, CreatedAt: event.ReceivedAt, UpdatedAt: event.ReceivedAt,
		}
		if requested.Target != nil {
			delivery.DriverID = requested.Target.DriverID
			delivery.DriverVersionID = requested.Target.DriverVersionID
			delivery.TargetEntrypoint = requested.Target.Entrypoint
			delivery.TargetAgentServiceID = requested.Target.TargetAgentServiceID
			delivery.SourceKind = requested.Target.SourceKind
			delivery.ConcurrencyPolicy = requested.Target.ConcurrencyPolicy
			delivery.RetryMaxAttempts = requested.Target.RetryMaxAttempts
			delivery.RetryBackoffSeconds = int(requested.Target.RetryBackoff / time.Second)
		}
		p.deliveries[deliveryMapKey(delivery.WorkspaceKey, delivery.DeliveryID)] = delivery
		result.Deliveries = append(result.Deliveries, ReservedDelivery{Delivery: delivery, Target: cloneDispatchTarget(requested.Target)})
	}
	p.reservations[key] = &fakeReservationRecord{fingerprint: reservation.Fingerprint, result: result}
	if p.commitThenError {
		p.commitThenError = false
		return nil, errors.New("simulated lost reservation response")
	}
	out := cloneReservationResult(result)
	if p.mutateReserveResult != nil {
		p.mutateReserveResult(out)
		p.mutateReserveResult = nil
	}
	return out, nil
}

func stampEventTimes(event *Event) {
	committedAt := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	if event.ReceivedAt.IsZero() {
		event.ReceivedAt = committedAt
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = event.ReceivedAt
	}
}

func (p *fakePersistence) TransitionDelivery(_ context.Context, transition DeliveryTransition) (*Delivery, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.transitionCalls = append(p.transitionCalls, transition)
	delivery := p.deliveries[deliveryMapKey(transition.WorkspaceKey, transition.DeliveryID)]
	if delivery == nil {
		return nil, ErrNotFound
	}
	if p.forceStaleTransition {
		p.forceStaleTransition = false
		delivery.Attempt++
	}
	if delivery.Status != transition.ExpectedStatus || delivery.Attempt != transition.ExpectedAttempt {
		return nil, ErrConflict
	}
	delivery.Status = transition.Status
	delivery.RejectionReason = transition.RejectionReason
	delivery.DriverRunID = transition.DriverRunID
	delivery.Attempt = transition.Attempt
	delivery.ErrorClass = transition.ErrorClass
	if transition.NextRetryAt == nil {
		delivery.NextRetryAt = nil
	} else {
		next := *transition.NextRetryAt
		delivery.NextRetryAt = &next
	}
	delivery.UpdatedAt = delivery.UpdatedAt.Add(time.Second)
	if p.transitionCommitThenError {
		p.transitionCommitThenError = false
		return nil, errors.New("simulated lost transition response")
	}
	return cloneDelivery(delivery), nil
}

func cloneDispatchTarget(in *DispatchTarget) *DispatchTarget {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func cloneReservationResult(in *ReservationResult) *ReservationResult {
	if in == nil {
		return nil
	}
	out := &ReservationResult{
		Event: cloneEvent(in.Event), Payload: cloneRawMessage(in.Payload),
		SubjectAttrs: cloneStringMap(in.SubjectAttrs), EpicID: in.EpicID, Replayed: in.Replayed,
	}
	for _, item := range in.Deliveries {
		out.Deliveries = append(out.Deliveries, ReservedDelivery{Delivery: cloneDelivery(item.Delivery), Target: cloneDispatchTarget(item.Target)})
	}
	return out
}

type fakeCatalog struct {
	mu     sync.Mutex
	values map[string]*workflowcatalog.EffectiveVersion
	calls  []string
}

func (c *fakeCatalog) ResolveEffectiveVersion(_ context.Context, _ authority.SystemAuthority, workspace, driverRef string) (*workflowcatalog.EffectiveVersion, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, driverRef)
	value := c.values[driverRef]
	if value == nil {
		return nil, workflowcatalog.ErrNotFound
	}
	return cloneEffective(value), nil
}

func cloneEffective(in *workflowcatalog.EffectiveVersion) *workflowcatalog.EffectiveVersion {
	if in == nil {
		return nil
	}
	out := *in
	if in.Driver != nil {
		driver := *in.Driver
		driver.Metadata = cloneStringMap(in.Driver.Metadata)
		out.Driver = &driver
	}
	if in.Version != nil {
		version := *in.Version
		version.Manifest = cloneStringMap(in.Version.Manifest)
		out.Version = &version
	}
	return &out
}

type fakeCatalogAuthority struct {
	mu    sync.Mutex
	auth  authority.SystemAuthority
	err   error
	calls int
}

func (p *fakeCatalogAuthority) AuthorityForEffectiveVersion(_ context.Context, _, _ string) (authority.SystemAuthority, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	return p.auth, p.err
}

type fakeDispatchOutcome struct {
	result          *ExecutionDispatchResult
	err             error
	committedStatus DeliveryStatus
	mutateCommitted func(*Delivery)
}
type fakeExecution struct {
	emission    *ExecutionEmissionContext
	emissionErr error
	calls       []ExecutionDispatchRequest
	outcomes    map[string][]fakeDispatchOutcome
	replays     map[string]fakeDispatchOutcome
	persistence *fakePersistence
}

// idempotentTestExecution serializes the fake ExecutionPort and replays a
// committed result by dispatch idempotency key. It models the production port's
// concurrency contract for tests where two admission replays race to dispatch
// the same newly reserved delivery.
type idempotentTestExecution struct {
	mu       sync.Mutex
	delegate *fakeExecution
	results  map[string]*ExecutionDispatchResult
}

func (e *idempotentTestExecution) EmissionContext(
	ctx context.Context,
	auth authority.ExecutionAuthority,
) (*ExecutionEmissionContext, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.delegate.EmissionContext(ctx, auth)
}

func (e *idempotentTestExecution) Dispatch(
	ctx context.Context,
	request ExecutionDispatchRequest,
) (*ExecutionDispatchResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.results == nil {
		e.results = make(map[string]*ExecutionDispatchResult)
	}
	if result := e.results[request.IdempotencyKey]; result != nil {
		return cloneExecutionDispatchResult(result), nil
	}
	result, err := e.delegate.Dispatch(ctx, request)
	if err == nil && result != nil {
		e.results[request.IdempotencyKey] = cloneExecutionDispatchResult(result)
	}
	return result, err
}

func (e *idempotentTestExecution) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.delegate.calls)
}

func cloneExecutionDispatchResult(in *ExecutionDispatchResult) *ExecutionDispatchResult {
	if in == nil {
		return nil
	}
	out := *in
	out.Delivery = cloneDelivery(in.Delivery)
	out.RunSnapshot = cloneRawMessage(in.RunSnapshot)
	return &out
}

func (e *fakeExecution) EmissionContext(_ context.Context, _ authority.ExecutionAuthority) (*ExecutionEmissionContext, error) {
	if e.emission == nil {
		return nil, e.emissionErr
	}
	out := *e.emission
	return &out, e.emissionErr
}

func (e *fakeExecution) Dispatch(_ context.Context, request ExecutionDispatchRequest) (*ExecutionDispatchResult, error) {
	request.Payload = cloneRawMessage(request.Payload)
	request.SubjectAttrs = cloneStringMap(request.SubjectAttrs)
	e.calls = append(e.calls, request)
	if request.ReplayOnly {
		outcome, ok := e.replays[request.TriggerBindingID]
		if !ok {
			return nil, ErrDispatchReplayNotFound
		}
		if outcome.result == nil {
			return nil, outcome.err
		}
		result := *outcome.result
		result.RunSnapshot = cloneRawMessage(outcome.result.RunSnapshot)
		return &result, outcome.err
	}
	queue := e.outcomes[request.TriggerBindingID]
	if len(queue) > 0 {
		outcome := queue[0]
		e.outcomes[request.TriggerBindingID] = queue[1:]
		if outcome.result == nil {
			return nil, outcome.err
		}
		result := *outcome.result
		result.RunSnapshot = cloneRawMessage(outcome.result.RunSnapshot)
		if request.DeliveryID == "" && len(result.RunSnapshot) == 0 && result.RunID != "" {
			result.RunSnapshot = json.RawMessage(fmt.Sprintf(`{"run_id":%q}`, result.RunID))
		}
		result.Delivery = cloneDelivery(outcome.result.Delivery)
		if request.DeliveryID != "" && result.Delivery == nil && outcome.committedStatus != "" {
			var err error
			result.Delivery, err = e.commitDispatch(request, &result, outcome.committedStatus, outcome.mutateCommitted)
			if err != nil {
				return nil, err
			}
		} else if result.Delivery != nil {
			e.commitProvidedDelivery(result.Delivery)
		}
		return &result, outcome.err
	}
	result := &ExecutionDispatchResult{RunID: "run-" + request.TriggerBindingID}
	if request.DeliveryID != "" {
		var err error
		result.Delivery, err = e.commitDispatch(request, result, DeliveryDispatched, nil)
		if err != nil {
			return nil, err
		}
	}
	if request.DeliveryID == "" {
		result.RunSnapshot = json.RawMessage(fmt.Sprintf(`{"run_id":%q}`, result.RunID))
	}
	return result, nil
}

func (e *fakeExecution) commitDispatch(
	request ExecutionDispatchRequest,
	result *ExecutionDispatchResult,
	status DeliveryStatus,
	mutate func(*Delivery),
) (*Delivery, error) {
	if e.persistence == nil {
		return nil, ErrUnavailable
	}
	e.persistence.mu.Lock()
	defer e.persistence.mu.Unlock()
	stored := e.persistence.deliveries[deliveryMapKey(request.WorkspaceKey, request.DeliveryID)]
	if stored == nil {
		return nil, ErrNotFound
	}
	if stored.Status != request.ExpectedDeliveryStatus || stored.Attempt != request.ExpectedDeliveryAttempt {
		return nil, ErrConflict
	}
	committed := cloneDelivery(stored)
	committed.Status = status
	committed.UpdatedAt = committed.UpdatedAt.Add(time.Second)
	switch status {
	case DeliveryDispatched, DeliverySuperseded:
		committed.DriverRunID = result.RunID
		committed.NextRetryAt = nil
	case DeliveryHeld:
		committed.DriverRunID = ""
		if committed.NextRetryAt == nil {
			next := committed.UpdatedAt.Add(time.Duration(committed.RetryBackoffSeconds) * time.Second)
			committed.NextRetryAt = &next
		}
	case DeliveryRejected:
		committed.DriverRunID = ""
		committed.RejectionReason = RejectionConcurrencyForbid
		committed.NextRetryAt = nil
	}
	if mutate != nil {
		mutate(committed)
	}
	*stored = *cloneDelivery(committed)
	return cloneDelivery(committed), nil
}

func (e *fakeExecution) commitProvidedDelivery(delivery *Delivery) {
	if e.persistence == nil || delivery == nil {
		return
	}
	e.persistence.mu.Lock()
	defer e.persistence.mu.Unlock()
	stored := e.persistence.deliveries[deliveryMapKey(delivery.WorkspaceKey, delivery.DeliveryID)]
	if stored != nil {
		*stored = *cloneDelivery(delivery)
	}
}

type testHarness struct {
	t                *testing.T
	now              time.Time
	issuer           *authority.Issuer
	persistence      *fakePersistence
	catalog          *fakeCatalog
	catalogAuthority *fakeCatalogAuthority
	execution        *fakeExecution
	awaits           *fakeAwaitEventNotifier
	eventTrustPolicy fakeEventTrustPolicy
	service          *Service
}

type fakeAwaitEventNotifier struct {
	calls     []AwaitEventNotification
	failCount int
	err       error
}

func (notifier *fakeAwaitEventNotifier) NotifyAwaitEvent(_ context.Context, event AwaitEventNotification) error {
	event.Payload = cloneRawMessage(event.Payload)
	notifier.calls = append(notifier.calls, event)
	if notifier.failCount > 0 {
		notifier.failCount--
		return notifier.err
	}
	return nil
}

func newTestHarness(t *testing.T) *testHarness {
	t.Helper()
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	issuer, err := authority.NewIssuerWithClock(func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	admission, err := issuer.NewAdmission(
		authority.OperatorOnly(ActionCreateBinding), authority.OperatorOnly(ActionUpdateBinding),
		authority.OperatorOnly(ActionEnableBinding), authority.OperatorOnly(ActionDisableBinding),
		authority.OperatorOnly(ActionDeleteBinding), authority.OperatorOnly(ActionDispatchBinding),
		authority.OperatorOnly(ActionCreateManagedBinding), authority.OperatorOnly(ActionUpdateManagedBinding),
		authority.OperatorOnly(ActionEnableManagedBinding), authority.OperatorOnly(ActionDisableManagedBinding),
		authority.OperatorOnly(ActionDeleteManagedBinding),
		authority.Allow(ActionAdmitEvent, authority.ClassWebhook, authority.ClassExecution, authority.ClassSystem),
		authority.Allow(ActionSweepCron, authority.ClassSystem), authority.Allow(ActionRetryDeliveries, authority.ClassSystem),
	)
	if err != nil {
		t.Fatal(err)
	}
	persistence := newFakePersistence()
	catalog := &fakeCatalog{values: make(map[string]*workflowcatalog.EffectiveVersion)}
	execution := &fakeExecution{
		outcomes: make(map[string][]fakeDispatchOutcome), replays: make(map[string]fakeDispatchOutcome),
		persistence: persistence,
	}
	awaits := &fakeAwaitEventNotifier{}
	h := &testHarness{
		t: t, now: now, issuer: issuer, persistence: persistence, catalog: catalog,
		execution: execution, awaits: awaits, eventTrustPolicy: fakeEventTrustPolicy{},
	}
	h.catalog.values["driver-a"] = effectiveVersion("ws", "driver-a", "version-active")
	catalogAuth := h.issueSystem(workflowcatalog.ActionResolveEffectiveVersion)
	h.catalogAuthority = &fakeCatalogAuthority{auth: catalogAuth}
	h.service = New(
		persistence, persistence, persistence, persistence, persistence, persistence, persistence,
		execution, catalog, h.catalogAuthority, admission,
		WithClock(func() time.Time { return now }), WithAwaitEventNotifier(awaits), WithEventTrustPolicy(h.eventTrustPolicy),
	)
	return h
}

func (h *testHarness) restartService() {
	h.t.Helper()
	authorityAdmission := h.service.authority
	h.service = New(
		h.persistence, h.persistence, h.persistence, h.persistence, h.persistence, h.persistence, h.persistence,
		h.execution, h.catalog, h.catalogAuthority, authorityAdmission,
		WithClock(func() time.Time { return h.now }), WithAwaitEventNotifier(h.awaits), WithEventTrustPolicy(h.eventTrustPolicy),
	)
}

func effectiveVersion(workspace, driverID, versionID string) *workflowcatalog.EffectiveVersion {
	return &workflowcatalog.EffectiveVersion{
		Driver:  &workflowcatalog.Driver{WorkspaceKey: workspace, DriverID: driverID, ActiveVersionID: versionID, Revision: 7},
		Version: &workflowcatalog.DriverVersion{WorkspaceKey: workspace, DriverID: driverID, VersionID: versionID, SourceDigest: "source-" + versionID, BundleDigest: "bundle-" + versionID},
	}
}

func (h *testHarness) principal(class authority.Class, action authority.Action) authority.VerifiedPrincipal {
	h.t.Helper()
	principal, err := h.issuer.DeriveVerifiedPrincipal(authority.PrincipalClaims{
		Subject: "subject-" + string(class), Class: class, Workspace: "ws", Actions: []authority.Action{action}, ExpiresAt: h.now.Add(time.Hour),
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return principal
}

func (h *testHarness) issueOperator(action authority.Action) authority.OperatorAuthority {
	h.t.Helper()
	value, err := h.issuer.IssueOperator(h.principal(authority.ClassOperator, action), "ws", action)
	if err != nil {
		h.t.Fatal(err)
	}
	return value
}
func (h *testHarness) issueWebhook(action authority.Action) authority.WebhookAuthority {
	h.t.Helper()
	value, err := h.issuer.IssueWebhook(h.principal(authority.ClassWebhook, action), "ws", action)
	if err != nil {
		h.t.Fatal(err)
	}
	return value
}
func (h *testHarness) issueExecution(action authority.Action) authority.ExecutionAuthority {
	h.t.Helper()
	value, err := h.issuer.IssueExecution(h.principal(authority.ClassExecution, action), "ws", action)
	if err != nil {
		h.t.Fatal(err)
	}
	return value
}
func (h *testHarness) issueSystem(action authority.Action) authority.SystemAuthority {
	h.t.Helper()
	value, err := h.issuer.IssueSystem(h.principal(authority.ClassSystem, action), "ws", action, "test")
	if err != nil {
		h.t.Fatal(err)
	}
	return value
}

func seedBinding(id, route string, patterns ...string) *Binding {
	return &Binding{
		WorkspaceKey: "ws", BindingID: id, Name: id, SourceKind: "github", RouteKey: route,
		EventTypePatterns: patterns, DriverID: "driver-a", DriverVersionID: "version-inactive",
		ConcurrencyPolicy: ConcurrencyOneActivePerEpic, RetryMaxAttempts: 5, RetryBackoffSeconds: 30, Enabled: true,
	}
}

func findDelivery(t *testing.T, result *AdmissionResult, bindingID string) *Delivery {
	t.Helper()
	for _, delivery := range result.Deliveries {
		if delivery.TriggerBindingID == bindingID {
			return delivery
		}
	}
	t.Fatalf("delivery for %q not found in %+v", bindingID, result.Deliveries)
	return nil
}

func callBindingIDs(calls []ExecutionDispatchRequest) []string {
	out := make([]string, len(calls))
	for index, call := range calls {
		out[index] = call.TriggerBindingID
	}
	return out
}

func assertErrorIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("error = %v, want errors.Is(..., %v)", err, target)
	}
}

var _ = strings.Builder{}
