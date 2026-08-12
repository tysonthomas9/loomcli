// Package fleetdb adapts the shared low-level FleetDB transport to
// Automation-owned persistence ports. It contains no admission policy and
// never constructs a FleetDB client.
package fleetdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"
)

var (
	ErrTransportNotFound                   = errors.New("automation fleetdb transport: not found")
	ErrTransportInvalid                    = errors.New("automation fleetdb transport: invalid request")
	ErrTransportConflict                   = errors.New("automation fleetdb transport: conflict")
	ErrTransportRouteNotFound              = errors.New("automation fleetdb transport: route not found")
	ErrTransportParentRunNotFound          = errors.New("automation fleetdb transport: parent run not found")
	ErrTransportExecutionOwnerConflict     = errors.New("automation fleetdb transport: execution owner conflict")
	ErrTransportIdempotencyConflict        = errors.New("automation fleetdb transport: idempotency conflict")
	ErrTransportBindingSnapshotConflict    = errors.New("automation fleetdb transport: binding snapshot conflict")
	ErrTransportCatalogSnapshotConflict    = errors.New("automation fleetdb transport: catalog snapshot conflict")
	ErrTransportHopDepthExceeded           = errors.New("automation fleetdb transport: hop depth exceeded")
	ErrTransportCatalogUnavailable         = errors.New("automation fleetdb transport: catalog unavailable")
	ErrTransportFanoutLimitExceeded        = errors.New("automation fleetdb transport: fanout limit exceeded")
	ErrTransportAdmissionUnavailable       = errors.New("automation fleetdb transport: admission unavailable")
	ErrTransportAdmissionReplayNotFound    = errors.New("automation fleetdb transport: admission replay not found")
	ErrTransportDeliveryNotFound           = errors.New("automation fleetdb transport: delivery not found")
	ErrTransportDeliveryTransitionConflict = errors.New("automation fleetdb transport: delivery transition conflict")
	ErrTransportPayloadDigestMismatch      = errors.New("automation fleetdb transport: payload digest mismatch")
	ErrTransportCronOccurrenceNotFound     = errors.New("automation fleetdb transport: cron occurrence not found")
	ErrTransportCronCompletionConflict     = errors.New("automation fleetdb transport: cron completion conflict")
	ErrTransportManagedBindingConflict     = errors.New("automation fleetdb transport: managed binding conflict")
)

type TransportManagedBindingSnapshot struct {
	WorkspaceKey                 string
	BindingID                    string
	ExpectedTargetAgentServiceID string
	ExpectedRouteKey             string
	ExpectedCreatedAt            time.Time
	ExpectedUpdatedAt            time.Time
}

type TransportManagedBindingReplacement struct {
	Expected TransportManagedBindingSnapshot
	Binding  *automation.Binding
}

type TransportUnmanagedBindingSnapshot struct {
	WorkspaceKey      string
	BindingID         string
	ExpectedRouteKey  string
	ExpectedCreatedAt time.Time
	ExpectedUpdatedAt time.Time
}

type TransportUnmanagedBindingReplacement struct {
	Expected TransportUnmanagedBindingSnapshot
	Binding  *automation.Binding
}

// TransportBindingMatchSnapshot is FleetDB's optimistic binding read.
type TransportBindingMatchSnapshot struct {
	WorkspaceKey       string
	RouteKey           string
	BindingSetRevision uint64
	Bindings           []*automation.Binding
}

// TransportCatalogGuard carries the immutable Workflow Catalog facts FleetDB
// revalidates in the same transaction as event reservation.
type TransportCatalogGuard struct {
	BindingID      string
	DriverID       string
	VersionID      string
	DriverRevision uint64
	SourceDigest   string
	BundleDigest   string
}

// TransportEventReservation is the narrow wire intent accepted by FleetDB.
// Origin and emitting run select trusted server routes; neither is serialized
// as caller-controlled event content by the low-level transport.
type TransportEventReservation struct {
	WorkspaceKey       string
	RouteKey           string
	IdempotencyKey     string
	ReplayOnly         bool
	Origin             automation.EventOrigin
	EmittingRunID      string
	NodeID             string
	LeaseID            string
	FencingToken       int64
	BindingSetRevision uint64
	MatchedBindingIDs  []string
	CatalogGuards      []TransportCatalogGuard
	SourceEventID      string
	EventType          string
	SubjectRef         string
	ActorRef           string
	OccurredAt         time.Time
	RawPayloadRef      string
	RawPayloadDigest   string
	Payload            json.RawMessage
	SubjectAttrs       map[string]string
}

type TransportReservationResult struct {
	Event             *automation.Event
	Deliveries        []*automation.Delivery
	EffectiveVersions []TransportCatalogGuard
	Replayed          bool
}

type TransportClaimedDelivery struct {
	Event    *automation.Event
	Delivery *automation.Delivery
}

type TransportCronClaim struct {
	WorkspaceKey   string
	IdempotencyKey string
	Before         time.Time
	ClaimUntil     time.Time
	Limit          int
}

type TransportCronOccurrence struct {
	WorkspaceKey string
	BindingID    string
	RouteKey     string
	OccurrenceID string
	OccurredAt   time.Time
}

type TransportCronCompletion struct {
	WorkspaceKey string
	BindingID    string
	OccurrenceID string
	Status       automation.CronCompletionStatus
	ErrorClass   string
}

type TransportDeliveryTransition struct {
	WorkspaceKey    string
	DeliveryID      string
	IdempotencyKey  string
	ExpectedStatus  automation.DeliveryStatus
	ExpectedAttempt int
	Status          automation.DeliveryStatus
	DriverRunID     string
	RejectionReason string
	NextRetryAt     *time.Time
	ErrorClass      string
}

// Transport is implemented by composition over the process-wide FleetDB
// client. Adapter-owned DTOs and sentinels keep infrastructure details from
// crossing into the Automation core.
type Transport interface {
	CreateBinding(context.Context, *automation.Binding) (*automation.Binding, error)
	GetBinding(context.Context, string, string) (*automation.Binding, error)
	ListBindings(context.Context, string, automation.BindingFilter) ([]*automation.Binding, error)
	UpdateBinding(context.Context, *automation.Binding) (*automation.Binding, error)
	DeleteBinding(context.Context, string, string) error
	ReplaceUnmanagedBinding(context.Context, TransportUnmanagedBindingReplacement) (*automation.Binding, error)
	DeleteUnmanagedBindingIfUnchanged(context.Context, TransportUnmanagedBindingSnapshot) error
	CreateManagedBinding(context.Context, *automation.Binding) (*automation.Binding, error)
	ReplaceManagedBinding(context.Context, TransportManagedBindingReplacement) (*automation.Binding, error)
	DeleteManagedBindingIfUnchanged(context.Context, TransportManagedBindingSnapshot) error
	MatchBindings(context.Context, string, string) (*TransportBindingMatchSnapshot, error)
	GetEvent(context.Context, string, string) (*automation.Event, error)
	ListEvents(context.Context, string, automation.EventFilter) ([]*automation.Event, error)
	GetDelivery(context.Context, string, string) (*automation.Delivery, error)
	ListDeliveries(context.Context, string, automation.DeliveryFilter) ([]*automation.Delivery, error)
	ReserveEvent(context.Context, TransportEventReservation) (*TransportReservationResult, error)
	ClaimDueCron(context.Context, TransportCronClaim) ([]TransportCronOccurrence, error)
	CompleteCron(context.Context, TransportCronCompletion) error
	ClaimDueDeliveries(context.Context, string, string, time.Time, time.Time, int) ([]TransportClaimedDelivery, error)
	TransitionDelivery(context.Context, TransportDeliveryTransition) (*automation.Delivery, error)
}

type Adapter struct{ transport Transport }

var (
	_ automation.BindingStore          = (*Adapter)(nil)
	_ automation.UnmanagedBindingStore = (*Adapter)(nil)
	_ automation.ManagedBindingStore   = (*Adapter)(nil)
	_ automation.BindingMatcher        = (*Adapter)(nil)
	_ automation.EventReader           = (*Adapter)(nil)
	_ automation.DeliveryReader        = (*Adapter)(nil)
	_ automation.AdmissionStore        = (*Adapter)(nil)
	_ automation.CronSweepPort         = (*Adapter)(nil)
	_ automation.DeliveryRetryPort     = (*Adapter)(nil)
)

func New(transport Transport) (*Adapter, error) {
	if transport == nil {
		return nil, fmt.Errorf("automation fleetdb adapter: nil transport: %w", automation.ErrUnavailable)
	}
	return &Adapter{transport: transport}, nil
}

func (a *Adapter) CreateBinding(ctx context.Context, binding *automation.Binding) (*automation.Binding, error) {
	if err := requireBinding(binding); err != nil {
		return nil, err
	}
	result, err := a.transport.CreateBinding(ctx, cloneBinding(binding))
	return bindingResult("create binding", result, err)
}

func (a *Adapter) GetBinding(ctx context.Context, workspace, bindingID string) (*automation.Binding, error) {
	result, err := a.transport.GetBinding(ctx, workspace, bindingID)
	return bindingResult("get binding", result, err)
}

func (a *Adapter) ListBindings(ctx context.Context, workspace string, filter automation.BindingFilter) ([]*automation.Binding, error) {
	results, err := a.transport.ListBindings(ctx, workspace, filter)
	if err != nil {
		return nil, mapError("list bindings", err)
	}
	return cloneBindings(results), nil
}

func (a *Adapter) UpdateBinding(ctx context.Context, binding *automation.Binding) (*automation.Binding, error) {
	if err := requireBinding(binding); err != nil {
		return nil, err
	}
	result, err := a.transport.UpdateBinding(ctx, cloneBinding(binding))
	return bindingResult("update binding", result, err)
}

func (a *Adapter) DeleteBinding(ctx context.Context, workspace, bindingID string) error {
	return mapError("delete binding", a.transport.DeleteBinding(ctx, workspace, bindingID))
}

func (a *Adapter) ReplaceUnmanagedBinding(ctx context.Context, replacement automation.UnmanagedBindingReplacement) (*automation.Binding, error) {
	if err := requireBinding(replacement.Binding); err != nil {
		return nil, err
	}
	result, err := a.transport.ReplaceUnmanagedBinding(ctx, TransportUnmanagedBindingReplacement{
		Expected: transportUnmanagedBindingSnapshot(replacement.Expected),
		Binding:  cloneBinding(replacement.Binding),
	})
	return bindingResult("replace unmanaged binding", result, err)
}

func (a *Adapter) DeleteUnmanagedBindingIfUnchanged(ctx context.Context, expected automation.UnmanagedBindingSnapshot) error {
	return mapError("delete unmanaged binding", a.transport.DeleteUnmanagedBindingIfUnchanged(ctx, transportUnmanagedBindingSnapshot(expected)))
}

func transportUnmanagedBindingSnapshot(expected automation.UnmanagedBindingSnapshot) TransportUnmanagedBindingSnapshot {
	return TransportUnmanagedBindingSnapshot{
		WorkspaceKey: expected.WorkspaceKey, BindingID: expected.BindingID,
		ExpectedRouteKey: expected.ExpectedRouteKey, ExpectedCreatedAt: expected.ExpectedCreatedAt,
		ExpectedUpdatedAt: expected.ExpectedUpdatedAt,
	}
}

func (a *Adapter) CreateManagedBinding(ctx context.Context, binding *automation.Binding) (*automation.Binding, error) {
	if err := requireBinding(binding); err != nil {
		return nil, err
	}
	result, err := a.transport.CreateManagedBinding(ctx, cloneBinding(binding))
	return bindingResult("create managed binding", result, err)
}

func (a *Adapter) ReplaceManagedBinding(ctx context.Context, replacement automation.ManagedBindingReplacement) (*automation.Binding, error) {
	if err := requireBinding(replacement.Binding); err != nil {
		return nil, err
	}
	result, err := a.transport.ReplaceManagedBinding(ctx, TransportManagedBindingReplacement{
		Expected: transportManagedBindingSnapshot(replacement.Expected),
		Binding:  cloneBinding(replacement.Binding),
	})
	return bindingResult("replace managed binding", result, err)
}

func (a *Adapter) DeleteManagedBindingIfUnchanged(ctx context.Context, expected automation.ManagedBindingSnapshot) error {
	return mapError("delete managed binding", a.transport.DeleteManagedBindingIfUnchanged(ctx, transportManagedBindingSnapshot(expected)))
}

func transportManagedBindingSnapshot(expected automation.ManagedBindingSnapshot) TransportManagedBindingSnapshot {
	return TransportManagedBindingSnapshot{
		WorkspaceKey: expected.WorkspaceKey, BindingID: expected.BindingID,
		ExpectedTargetAgentServiceID: expected.ExpectedTargetAgentServiceID,
		ExpectedRouteKey:             expected.ExpectedRouteKey,
		ExpectedCreatedAt:            expected.ExpectedCreatedAt, ExpectedUpdatedAt: expected.ExpectedUpdatedAt,
	}
}

func (a *Adapter) MatchBindings(ctx context.Context, workspace, routeKey string) (*automation.BindingMatchSnapshot, error) {
	result, err := a.transport.MatchBindings(ctx, workspace, routeKey)
	if err != nil {
		return nil, mapError("match bindings", err)
	}
	if result == nil {
		return nil, fmt.Errorf("match bindings: empty FleetDB response: %w", automation.ErrInvalidPersistedState)
	}
	return &automation.BindingMatchSnapshot{
		WorkspaceKey: result.WorkspaceKey, RouteKey: result.RouteKey,
		BindingSetRevision: result.BindingSetRevision, Bindings: cloneBindings(result.Bindings),
	}, nil
}

func (a *Adapter) GetEvent(ctx context.Context, workspace, eventID string) (*automation.Event, error) {
	result, err := a.transport.GetEvent(ctx, workspace, eventID)
	if err != nil {
		return nil, mapError("get event", err)
	}
	return cloneEvent(result), nil
}

func (a *Adapter) ListEvents(ctx context.Context, workspace string, filter automation.EventFilter) ([]*automation.Event, error) {
	results, err := a.transport.ListEvents(ctx, workspace, filter)
	if err != nil {
		return nil, mapError("list events", err)
	}
	out := make([]*automation.Event, 0, len(results))
	for _, result := range results {
		out = append(out, cloneEvent(result))
	}
	return out, nil
}

func (a *Adapter) GetDelivery(ctx context.Context, workspace, deliveryID string) (*automation.Delivery, error) {
	result, err := a.transport.GetDelivery(ctx, workspace, deliveryID)
	if err != nil {
		return nil, mapError("get delivery", err)
	}
	return cloneDelivery(result), nil
}

func (a *Adapter) ListDeliveries(ctx context.Context, workspace string, filter automation.DeliveryFilter) ([]*automation.Delivery, error) {
	results, err := a.transport.ListDeliveries(ctx, workspace, filter)
	if err != nil {
		return nil, mapError("list deliveries", err)
	}
	out := make([]*automation.Delivery, 0, len(results))
	for _, result := range results {
		out = append(out, cloneDelivery(result))
	}
	return out, nil
}

func (a *Adapter) ReserveEvent(ctx context.Context, reservation automation.EventReservation) (*automation.ReservationResult, error) {
	request, err := transportReservation(reservation)
	if err != nil {
		return nil, err
	}
	result, err := a.transport.ReserveEvent(ctx, request)
	if err != nil {
		return nil, mapError("reserve event", err)
	}
	if result == nil || result.Event == nil {
		return nil, fmt.Errorf("reserve event: empty FleetDB response: %w", automation.ErrInvalidPersistedState)
	}
	event := cloneEvent(result.Event)
	deliveries := make([]automation.ReservedDelivery, 0, len(result.Deliveries))
	guards, err := guardsByBinding(result.EffectiveVersions)
	if err != nil {
		return nil, err
	}
	for _, resultDelivery := range result.Deliveries {
		delivery := cloneDelivery(resultDelivery)
		item := automation.ReservedDelivery{Delivery: delivery}
		if delivery != nil && delivery.Status == automation.DeliveryAccepted {
			guard, ok := guards[delivery.TriggerBindingID]
			if !ok {
				return nil, fmt.Errorf("reserve event: accepted delivery %q has no committed Catalog guard: %w",
					delivery.DeliveryID, automation.ErrInvalidPersistedState)
			}
			if guard.DriverID != delivery.DriverID || guard.VersionID != delivery.DriverVersionID {
				return nil, fmt.Errorf("reserve event: accepted delivery %q does not match committed Catalog guard: %w",
					delivery.DeliveryID, automation.ErrInvalidPersistedState)
			}
			item.Target = targetFromDelivery(delivery, guard)
			delete(guards, delivery.TriggerBindingID)
		} else if delivery != nil && delivery.Status == automation.DeliveryRejected {
			if _, ok := guards[delivery.TriggerBindingID]; ok {
				return nil, fmt.Errorf("reserve event: rejected delivery %q has a committed Catalog guard: %w",
					delivery.DeliveryID, automation.ErrInvalidPersistedState)
			}
		} else {
			return nil, fmt.Errorf("reserve event: delivery has invalid initial status: %w", automation.ErrInvalidPersistedState)
		}
		deliveries = append(deliveries, item)
	}
	if len(guards) != 0 {
		return nil, fmt.Errorf("reserve event: committed Catalog guards do not match deliveries: %w", automation.ErrInvalidPersistedState)
	}
	return &automation.ReservationResult{
		Event: event, Deliveries: deliveries, Payload: cloneRaw(event.Payload),
		SubjectAttrs: cloneStrings(event.SubjectAttrs), EpicID: event.EpicID, Replayed: result.Replayed,
	}, nil
}

func (a *Adapter) TransitionDelivery(ctx context.Context, transition automation.DeliveryTransition) (*automation.Delivery, error) {
	request := TransportDeliveryTransition{
		WorkspaceKey: transition.WorkspaceKey, DeliveryID: transition.DeliveryID,
		IdempotencyKey: transition.IdempotencyKey, ExpectedStatus: transition.ExpectedStatus,
		ExpectedAttempt: transition.ExpectedAttempt, Status: transition.Status,
		DriverRunID: transition.DriverRunID, RejectionReason: transition.RejectionReason,
		NextRetryAt: cloneTime(transition.NextRetryAt), ErrorClass: transition.ErrorClass,
	}
	result, err := a.transport.TransitionDelivery(ctx, request)
	if err != nil {
		return nil, mapError("transition delivery", err)
	}
	return cloneDelivery(result), nil
}

func (a *Adapter) ClaimDueDeliveries(ctx context.Context, workspace string, before, claimUntil time.Time, limit int) ([]automation.RetryCandidate, error) {
	key := claimIdempotencyKey(workspace, before, claimUntil, limit)
	results, err := a.transport.ClaimDueDeliveries(ctx, workspace, key, before, claimUntil, limit)
	if err != nil {
		return nil, mapError("claim due deliveries", err)
	}
	out := make([]automation.RetryCandidate, 0, len(results))
	for _, result := range results {
		event, delivery := cloneEvent(result.Event), cloneDelivery(result.Delivery)
		out = append(out, automation.RetryCandidate{
			Event: event, Delivery: delivery, Target: targetFromDelivery(delivery, automation.CatalogGuard{}),
			Payload: cloneRaw(eventPayload(event)), SubjectAttrs: cloneStrings(eventAttrs(event)), EpicID: eventEpic(event),
		})
	}
	return out, nil
}

func (a *Adapter) ClaimDueCron(ctx context.Context, claim automation.CronClaim) ([]automation.CronOccurrence, error) {
	results, err := a.transport.ClaimDueCron(ctx, TransportCronClaim{
		WorkspaceKey: claim.WorkspaceKey, IdempotencyKey: claim.IdempotencyKey,
		Before: claim.Before, ClaimUntil: claim.ClaimUntil, Limit: claim.Limit,
	})
	if err != nil {
		return nil, mapError("claim due cron", err)
	}
	out := make([]automation.CronOccurrence, 0, len(results))
	seen := make(map[string]struct{}, len(results))
	for _, result := range results {
		if err := validateTransportCronOccurrence(result, claim.WorkspaceKey); err != nil {
			return nil, err
		}
		if _, duplicate := seen[result.OccurrenceID]; duplicate {
			return nil, fmt.Errorf("claim due cron: duplicate occurrence %q: %w", result.OccurrenceID, automation.ErrInvalidPersistedState)
		}
		seen[result.OccurrenceID] = struct{}{}
		out = append(out, automation.CronOccurrence{
			WorkspaceKey: result.WorkspaceKey, BindingID: result.BindingID, RouteKey: result.RouteKey,
			OccurrenceID: result.OccurrenceID, OccurredAt: result.OccurredAt.UTC(),
		})
	}
	return out, nil
}

func (a *Adapter) CompleteCron(ctx context.Context, completion automation.CronCompletion) error {
	return mapError("complete cron", a.transport.CompleteCron(ctx, TransportCronCompletion{
		WorkspaceKey: completion.WorkspaceKey, BindingID: completion.BindingID,
		OccurrenceID: completion.OccurrenceID, Status: completion.Status, ErrorClass: completion.ErrorClass,
	}))
}

func validateTransportCronOccurrence(occurrence TransportCronOccurrence, workspace string) error {
	canonical := func(value string) bool { return value != "" && value == strings.TrimSpace(value) }
	if occurrence.WorkspaceKey != workspace {
		return fmt.Errorf("claim due cron: occurrence workspace %q does not match %q: %w",
			occurrence.WorkspaceKey, workspace, errors.Join(automation.ErrWrongWorkspace, automation.ErrInvalidPersistedState))
	}
	if !canonical(occurrence.BindingID) || !canonical(occurrence.OccurrenceID) ||
		!strings.HasPrefix(occurrence.OccurrenceID, "cron:") || occurrence.OccurredAt.IsZero() ||
		(occurrence.RouteKey != "" && !canonical(occurrence.RouteKey)) {
		return fmt.Errorf("claim due cron: malformed occurrence: %w", automation.ErrInvalidPersistedState)
	}
	return nil
}

func transportReservation(reservation automation.EventReservation) (TransportEventReservation, error) {
	if reservation.Event == nil {
		return TransportEventReservation{}, fmt.Errorf("reserve event: missing event: %w", automation.ErrInvalid)
	}
	guards := make([]TransportCatalogGuard, 0, len(reservation.CatalogGuards))
	for _, guard := range reservation.CatalogGuards {
		guards = append(guards, TransportCatalogGuard{
			BindingID: guard.BindingID, DriverID: guard.DriverID, VersionID: guard.VersionID,
			DriverRevision: guard.DriverRevision, SourceDigest: guard.SourceDigest, BundleDigest: guard.BundleDigest,
		})
	}
	event := reservation.Event
	return TransportEventReservation{
		WorkspaceKey: event.WorkspaceKey, RouteKey: event.RouteKey, IdempotencyKey: event.IdempotencyKey,
		ReplayOnly: reservation.ReplayOnly, Origin: event.Origin, EmittingRunID: event.EmittingRunID,
		NodeID: reservation.ExecutionNodeID, LeaseID: reservation.ExecutionLeaseID, FencingToken: reservation.ExecutionFence,
		BindingSetRevision: reservation.BindingSetRevision,
		MatchedBindingIDs:  append([]string(nil), reservation.MatchedBindingIDs...), CatalogGuards: guards,
		SourceEventID: event.SourceEventID, EventType: event.EventType, SubjectRef: event.SubjectRef,
		ActorRef: event.ActorRef, OccurredAt: event.OccurredAt, RawPayloadRef: event.RawPayloadRef,
		RawPayloadDigest: event.RawPayloadDigest, Payload: cloneRaw(reservation.Payload),
		SubjectAttrs: cloneStrings(reservation.SubjectAttrs),
	}, nil
}

func targetFromDelivery(delivery *automation.Delivery, guard automation.CatalogGuard) *automation.DispatchTarget {
	if delivery == nil {
		return nil
	}
	target := &automation.DispatchTarget{
		DriverID: delivery.DriverID, DriverVersionID: delivery.DriverVersionID,
		Entrypoint: delivery.TargetEntrypoint, TargetAgentServiceID: delivery.TargetAgentServiceID,
		SourceKind: delivery.SourceKind,
		BindingID:  delivery.TriggerBindingID, ConcurrencyPolicy: delivery.ConcurrencyPolicy,
		RetryMaxAttempts: delivery.RetryMaxAttempts,
		RetryBackoff:     time.Duration(delivery.RetryBackoffSeconds) * time.Second,
	}
	if guard.BindingID == delivery.TriggerBindingID && guard.DriverID == delivery.DriverID && guard.VersionID == delivery.DriverVersionID {
		target.DriverRevision = guard.DriverRevision
		target.SourceDigest = guard.SourceDigest
		target.BundleDigest = guard.BundleDigest
	}
	return target
}

func guardsByBinding(guards []TransportCatalogGuard) (map[string]automation.CatalogGuard, error) {
	out := make(map[string]automation.CatalogGuard, len(guards))
	for _, guard := range guards {
		if guard.BindingID == "" || guard.DriverID == "" || guard.VersionID == "" || guard.DriverRevision == 0 ||
			guard.SourceDigest == "" || guard.BundleDigest == "" {
			return nil, fmt.Errorf("reserve event: incomplete committed Catalog guard: %w", automation.ErrInvalidPersistedState)
		}
		if _, duplicate := out[guard.BindingID]; duplicate {
			return nil, fmt.Errorf("reserve event: duplicate committed Catalog guard %q: %w", guard.BindingID, automation.ErrInvalidPersistedState)
		}
		out[guard.BindingID] = automation.CatalogGuard{
			BindingID: guard.BindingID, DriverID: guard.DriverID, VersionID: guard.VersionID,
			DriverRevision: guard.DriverRevision, SourceDigest: guard.SourceDigest, BundleDigest: guard.BundleDigest,
		}
	}
	return out, nil
}

func claimIdempotencyKey(workspace string, before, claimUntil time.Time, limit int) string {
	stable := fmt.Sprintf("%s\x00%s\x00%s\x00%d", workspace, before.UTC().Format(time.RFC3339Nano), claimUntil.UTC().Format(time.RFC3339Nano), limit)
	sum := sha256.Sum256([]byte(stable))
	return "automation-claim:" + hex.EncodeToString(sum[:])
}

func requireBinding(binding *automation.Binding) error {
	if binding == nil {
		return fmt.Errorf("binding is required: %w", automation.ErrInvalid)
	}
	return nil
}

func bindingResult(operation string, binding *automation.Binding, err error) (*automation.Binding, error) {
	if err != nil {
		return nil, mapError(operation, err)
	}
	if binding == nil {
		return nil, fmt.Errorf("%s: empty FleetDB response: %w", operation, automation.ErrInvalidPersistedState)
	}
	return cloneBinding(binding), nil
}

func cloneBindings(bindings []*automation.Binding) []*automation.Binding {
	out := make([]*automation.Binding, 0, len(bindings))
	for _, binding := range bindings {
		out = append(out, cloneBinding(binding))
	}
	return out
}

func cloneBinding(in *automation.Binding) *automation.Binding {
	if in == nil {
		return nil
	}
	out := *in
	out.EventTypePatterns = append([]string(nil), in.EventTypePatterns...)
	out.Permissions = append([]string(nil), in.Permissions...)
	if in.ActorFilter != nil {
		out.ActorFilter = &automation.ActorFilter{
			ExcludeActorKinds: append([]string(nil), in.ActorFilter.ExcludeActorKinds...),
			AllowActors:       append([]string(nil), in.ActorFilter.AllowActors...),
		}
	}
	return &out
}

func cloneEvent(in *automation.Event) *automation.Event {
	if in == nil {
		return nil
	}
	out := *in
	out.Payload = cloneRaw(in.Payload)
	out.SubjectAttrs = cloneStrings(in.SubjectAttrs)
	out.NormalizeProvenance()
	return &out
}

func cloneDelivery(in *automation.Delivery) *automation.Delivery {
	if in == nil {
		return nil
	}
	out := *in
	out.NextRetryAt = cloneTime(in.NextRetryAt)
	return &out
}

func cloneRaw(in json.RawMessage) json.RawMessage { return append(json.RawMessage(nil), in...) }

func cloneStrings(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneTime(in *time.Time) *time.Time {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

func eventPayload(event *automation.Event) json.RawMessage {
	if event == nil {
		return nil
	}
	return event.Payload
}

func eventAttrs(event *automation.Event) map[string]string {
	if event == nil {
		return nil
	}
	return event.SubjectAttrs
}

func eventEpic(event *automation.Event) string {
	if event == nil {
		return ""
	}
	return event.EpicID
}

func mapError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var mapped error
	switch {
	case errors.Is(err, ErrTransportRouteNotFound):
		mapped = errors.Join(automation.ErrNoMatchingBinding, automation.ErrNotFound)
	case errors.Is(err, ErrTransportParentRunNotFound):
		mapped = errors.Join(automation.ErrParentEventNotFound, automation.ErrNotFound)
	case errors.Is(err, ErrTransportExecutionOwnerConflict):
		mapped = automation.ErrConflict
	case errors.Is(err, ErrTransportHopDepthExceeded):
		mapped = automation.ErrHopDepthExceeded
	case errors.Is(err, ErrTransportDeliveryNotFound), errors.Is(err, ErrTransportNotFound):
		mapped = automation.ErrNotFound
	case errors.Is(err, ErrTransportCronOccurrenceNotFound):
		mapped = automation.ErrNotFound
	case errors.Is(err, ErrTransportInvalid), errors.Is(err, ErrTransportPayloadDigestMismatch):
		mapped = automation.ErrInvalid
	case errors.Is(err, ErrTransportIdempotencyConflict), errors.Is(err, ErrTransportBindingSnapshotConflict),
		errors.Is(err, ErrTransportCatalogSnapshotConflict), errors.Is(err, ErrTransportDeliveryTransitionConflict),
		errors.Is(err, ErrTransportCronCompletionConflict), errors.Is(err, ErrTransportConflict),
		errors.Is(err, ErrTransportFanoutLimitExceeded):
		mapped = automation.ErrConflict
	case errors.Is(err, ErrTransportCatalogUnavailable), errors.Is(err, ErrTransportAdmissionUnavailable):
		mapped = automation.ErrUnavailable
	case errors.Is(err, ErrTransportAdmissionReplayNotFound):
		mapped = automation.ErrAdmissionReplayNotFound
	case errors.Is(err, ErrTransportManagedBindingConflict):
		mapped = automation.ErrManagedBinding
	default:
		mapped = automation.ErrUnavailable
	}
	return fmt.Errorf("%s: %w", operation, errors.Join(mapped, err))
}
