package serve

import (
	"context"
	"errors"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	infrafleetdb "github.com/tysonthomas9/loomcli/internal/infra/fleetdb"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
	automationfleetdb "github.com/tysonthomas9/loomcli/internal/modules/automation/fleetdb"
)

// automationFleetDBTransport is the composition-owned bridge between the one
// process-wide FleetDB client and Automation's consumer-owned transport. It
// translates DTOs and stable error vocabulary only; matching, admission,
// retry, and execution policy remain in the Automation core.
type automationFleetDBTransport struct {
	transport infrafleetdb.AutomationTransport
}

var _ automationfleetdb.Transport = (*automationFleetDBTransport)(nil)

func newAutomationFleetDBTransport(client *infrafleetdb.Client) automationfleetdb.Transport {
	if client == nil {
		return nil
	}
	return &automationFleetDBTransport{transport: client.Automation()}
}

func (transport *automationFleetDBTransport) CreateBinding(ctx context.Context, binding *automation.Binding) (*automation.Binding, error) {
	value, err := transport.transport.CreateBinding(ctx, binding)
	return value, translateAutomationFleetDBError(err)
}

func (transport *automationFleetDBTransport) GetBinding(ctx context.Context, workspace, bindingID string) (*automation.Binding, error) {
	value, err := transport.transport.GetBinding(ctx, workspace, bindingID)
	return value, translateAutomationFleetDBError(err)
}

func (transport *automationFleetDBTransport) ListBindings(ctx context.Context, workspace string, filter automation.BindingFilter) ([]*automation.Binding, error) {
	values, err := transport.transport.ListBindings(ctx, workspace, infrafleetdb.AutomationBindingFilter{
		SourceKind: filter.SourceKind, RouteKey: filter.RouteKey, DriverID: filter.DriverID,
		TargetAgentServiceID: filter.TargetAgentServiceID, Enabled: filter.Enabled, Limit: filter.Limit,
	})
	return values, translateAutomationFleetDBError(err)
}

func (transport *automationFleetDBTransport) UpdateBinding(ctx context.Context, binding *automation.Binding) (*automation.Binding, error) {
	value, err := transport.transport.UpdateBinding(ctx, binding)
	return value, translateAutomationFleetDBError(err)
}

func (transport *automationFleetDBTransport) DeleteBinding(ctx context.Context, workspace, bindingID string) error {
	return translateAutomationFleetDBError(transport.transport.DeleteBinding(ctx, workspace, bindingID))
}

func (transport *automationFleetDBTransport) ReplaceUnmanagedBinding(
	ctx context.Context,
	replacement automationfleetdb.TransportUnmanagedBindingReplacement,
) (*automation.Binding, error) {
	value, err := transport.transport.ReplaceUnmanagedBinding(ctx, infrafleetdb.AutomationUnmanagedBindingReplacement{
		Expected: infrafleetdb.AutomationUnmanagedBindingSnapshot{
			WorkspaceKey: replacement.Expected.WorkspaceKey, BindingID: replacement.Expected.BindingID,
			ExpectedRouteKey:  replacement.Expected.ExpectedRouteKey,
			ExpectedCreatedAt: replacement.Expected.ExpectedCreatedAt,
			ExpectedUpdatedAt: replacement.Expected.ExpectedUpdatedAt,
		},
		Binding: replacement.Binding,
	})
	return value, translateAutomationFleetDBError(err)
}

func (transport *automationFleetDBTransport) DeleteUnmanagedBindingIfUnchanged(
	ctx context.Context,
	expected automationfleetdb.TransportUnmanagedBindingSnapshot,
) error {
	return translateAutomationFleetDBError(transport.transport.DeleteUnmanagedBindingIfUnchanged(ctx, infrafleetdb.AutomationUnmanagedBindingSnapshot{
		WorkspaceKey: expected.WorkspaceKey, BindingID: expected.BindingID,
		ExpectedRouteKey: expected.ExpectedRouteKey, ExpectedCreatedAt: expected.ExpectedCreatedAt,
		ExpectedUpdatedAt: expected.ExpectedUpdatedAt,
	}))
}

func (transport *automationFleetDBTransport) CreateManagedBinding(ctx context.Context, binding *automation.Binding) (*automation.Binding, error) {
	value, err := transport.transport.CreateManagedBinding(ctx, binding)
	return value, translateAutomationFleetDBError(err)
}

func (transport *automationFleetDBTransport) ReplaceManagedBinding(
	ctx context.Context,
	replacement automationfleetdb.TransportManagedBindingReplacement,
) (*automation.Binding, error) {
	value, err := transport.transport.ReplaceManagedBinding(ctx, infrafleetdb.AutomationManagedBindingReplacement{
		Expected: infrafleetdb.AutomationManagedBindingSnapshot{
			WorkspaceKey: replacement.Expected.WorkspaceKey, BindingID: replacement.Expected.BindingID,
			ExpectedTargetAgentServiceID: replacement.Expected.ExpectedTargetAgentServiceID,
			ExpectedRouteKey:             replacement.Expected.ExpectedRouteKey,
			ExpectedCreatedAt:            replacement.Expected.ExpectedCreatedAt,
			ExpectedUpdatedAt:            replacement.Expected.ExpectedUpdatedAt,
		},
		Binding: replacement.Binding,
	})
	return value, translateAutomationFleetDBError(err)
}

func (transport *automationFleetDBTransport) DeleteManagedBindingIfUnchanged(
	ctx context.Context,
	expected automationfleetdb.TransportManagedBindingSnapshot,
) error {
	return translateAutomationFleetDBError(transport.transport.DeleteManagedBindingIfUnchanged(ctx, infrafleetdb.AutomationManagedBindingSnapshot{
		WorkspaceKey: expected.WorkspaceKey, BindingID: expected.BindingID,
		ExpectedTargetAgentServiceID: expected.ExpectedTargetAgentServiceID,
		ExpectedRouteKey:             expected.ExpectedRouteKey,
		ExpectedCreatedAt:            expected.ExpectedCreatedAt, ExpectedUpdatedAt: expected.ExpectedUpdatedAt,
	}))
}

func (transport *automationFleetDBTransport) MatchBindings(ctx context.Context, workspace, routeKey string) (*automationfleetdb.TransportBindingMatchSnapshot, error) {
	result, err := transport.transport.MatchBindings(ctx, workspace, routeKey)
	if err != nil {
		return nil, translateAutomationFleetDBError(err)
	}
	if result == nil {
		return nil, nil
	}
	return &automationfleetdb.TransportBindingMatchSnapshot{
		WorkspaceKey: result.WorkspaceKey, RouteKey: result.RouteKey,
		BindingSetRevision: result.BindingSetRevision, Bindings: result.Bindings,
	}, nil
}

func (transport *automationFleetDBTransport) GetEvent(ctx context.Context, workspace, eventID string) (*automation.Event, error) {
	value, err := transport.transport.GetEvent(ctx, workspace, eventID)
	return value, translateAutomationFleetDBError(err)
}

func (transport *automationFleetDBTransport) ListEvents(ctx context.Context, workspace string, filter automation.EventFilter) ([]*automation.Event, error) {
	values, err := transport.transport.ListEvents(ctx, workspace, infrafleetdb.AutomationEventFilter{
		BindingID: filter.BindingID, SourceKind: filter.SourceKind, Origin: filter.Origin, Limit: filter.Limit,
	})
	return values, translateAutomationFleetDBError(err)
}

func (transport *automationFleetDBTransport) GetDelivery(ctx context.Context, workspace, deliveryID string) (*automation.Delivery, error) {
	value, err := transport.transport.GetDelivery(ctx, workspace, deliveryID)
	return value, translateAutomationFleetDBError(err)
}

func (transport *automationFleetDBTransport) ListDeliveries(ctx context.Context, workspace string, filter automation.DeliveryFilter) ([]*automation.Delivery, error) {
	values, err := transport.transport.ListDeliveries(ctx, workspace, infrafleetdb.AutomationDeliveryFilter{
		EventID: filter.EventID, BindingID: filter.BindingID, Status: filter.Status, Limit: filter.Limit,
	})
	return values, translateAutomationFleetDBError(err)
}

func (transport *automationFleetDBTransport) ReserveEvent(ctx context.Context, reservation automationfleetdb.TransportEventReservation) (*automationfleetdb.TransportReservationResult, error) {
	guards := make([]infrafleetdb.AutomationCatalogGuard, 0, len(reservation.CatalogGuards))
	for _, guard := range reservation.CatalogGuards {
		guards = append(guards, infrafleetdb.AutomationCatalogGuard{
			BindingID: guard.BindingID, DriverID: guard.DriverID, VersionID: guard.VersionID,
			DriverRevision: guard.DriverRevision, SourceDigest: guard.SourceDigest, BundleDigest: guard.BundleDigest,
		})
	}
	result, err := transport.transport.ReserveEvent(ctx, infrafleetdb.AutomationEventReservation{
		WorkspaceKey: reservation.WorkspaceKey, RouteKey: reservation.RouteKey,
		IdempotencyKey: reservation.IdempotencyKey, ReplayOnly: reservation.ReplayOnly, Origin: reservation.Origin,
		EmittingRunID: reservation.EmittingRunID, BindingSetRevision: reservation.BindingSetRevision,
		NodeID: reservation.NodeID, LeaseID: reservation.LeaseID, FencingToken: reservation.FencingToken,
		MatchedBindingIDs: append([]string(nil), reservation.MatchedBindingIDs...), CatalogGuards: guards,
		SourceEventID: reservation.SourceEventID, EventType: reservation.EventType,
		SubjectRef: reservation.SubjectRef, ActorRef: reservation.ActorRef, OccurredAt: reservation.OccurredAt,
		RawPayloadRef: reservation.RawPayloadRef, RawPayloadDigest: reservation.RawPayloadDigest,
		Payload: append([]byte(nil), reservation.Payload...), SubjectAttrs: cloneAutomationStringMap(reservation.SubjectAttrs),
	})
	if err != nil {
		return nil, translateAutomationFleetDBError(err)
	}
	if result == nil {
		return nil, nil
	}
	committedGuards := make([]automationfleetdb.TransportCatalogGuard, 0, len(result.EffectiveVersions))
	for _, guard := range result.EffectiveVersions {
		committedGuards = append(committedGuards, automationfleetdb.TransportCatalogGuard{
			BindingID: guard.BindingID, DriverID: guard.DriverID, VersionID: guard.VersionID,
			DriverRevision: guard.DriverRevision, SourceDigest: guard.SourceDigest, BundleDigest: guard.BundleDigest,
		})
	}
	return &automationfleetdb.TransportReservationResult{
		Event: result.Event, Deliveries: result.Deliveries,
		EffectiveVersions: committedGuards, Replayed: result.Replayed,
	}, nil
}

func (transport *automationFleetDBTransport) ClaimDueDeliveries(
	ctx context.Context,
	workspace, idempotencyKey string,
	before, claimUntil time.Time,
	limit int,
) ([]automationfleetdb.TransportClaimedDelivery, error) {
	items, err := transport.transport.ClaimDueDeliveries(ctx, workspace, idempotencyKey, before, claimUntil, limit)
	if err != nil {
		return nil, translateAutomationFleetDBError(err)
	}
	result := make([]automationfleetdb.TransportClaimedDelivery, 0, len(items))
	for _, item := range items {
		result = append(result, automationfleetdb.TransportClaimedDelivery{Event: item.Event, Delivery: item.Delivery})
	}
	return result, nil
}

func (transport *automationFleetDBTransport) ClaimDueCron(ctx context.Context, claim automationfleetdb.TransportCronClaim) ([]automationfleetdb.TransportCronOccurrence, error) {
	items, err := transport.transport.ClaimDueCron(ctx, infrafleetdb.AutomationCronClaim{
		WorkspaceKey: claim.WorkspaceKey, IdempotencyKey: claim.IdempotencyKey,
		Before: claim.Before, ClaimUntil: claim.ClaimUntil, Limit: claim.Limit,
	})
	if err != nil {
		return nil, translateAutomationFleetDBError(err)
	}
	result := make([]automationfleetdb.TransportCronOccurrence, 0, len(items))
	for _, item := range items {
		result = append(result, automationfleetdb.TransportCronOccurrence{
			WorkspaceKey: item.WorkspaceKey, BindingID: item.BindingID, RouteKey: item.RouteKey,
			OccurrenceID: item.OccurrenceID, OccurredAt: item.OccurredAt,
		})
	}
	return result, nil
}

func (transport *automationFleetDBTransport) CompleteCron(ctx context.Context, completion automationfleetdb.TransportCronCompletion) error {
	return translateAutomationFleetDBError(transport.transport.CompleteCron(ctx, infrafleetdb.AutomationCronCompletion{
		WorkspaceKey: completion.WorkspaceKey, BindingID: completion.BindingID,
		OccurrenceID: completion.OccurrenceID,
		Status:       infrafleetdb.AutomationCronCompletionStatus(completion.Status),
		ErrorClass:   completion.ErrorClass,
	}))
}

func (transport *automationFleetDBTransport) TransitionDelivery(ctx context.Context, transition automationfleetdb.TransportDeliveryTransition) (*automation.Delivery, error) {
	value, err := transport.transport.TransitionDelivery(ctx, infrafleetdb.AutomationDeliveryTransition{
		WorkspaceKey: transition.WorkspaceKey, DeliveryID: transition.DeliveryID,
		IdempotencyKey: transition.IdempotencyKey, ExpectedStatus: transition.ExpectedStatus,
		ExpectedAttempt: transition.ExpectedAttempt, Status: transition.Status,
		DriverRunID: transition.DriverRunID, RejectionReason: transition.RejectionReason,
		NextRetryAt: transition.NextRetryAt, ErrorClass: transition.ErrorClass,
	})
	return value, translateAutomationFleetDBError(err)
}

//nolint:cyclop // Keep the exhaustive FleetDB-to-Automation sentinel mapping together for boundary auditing.
func translateAutomationFleetDBError(err error) error {
	if err == nil {
		return nil
	}
	var translated error
	switch {
	case errors.Is(err, infrafleetdb.ErrAutomationRouteNotFound):
		translated = automationfleetdb.ErrTransportRouteNotFound
	case errors.Is(err, infrafleetdb.ErrAutomationParentRunNotFound):
		translated = automationfleetdb.ErrTransportParentRunNotFound
	case errors.Is(err, infrafleetdb.ErrAutomationExecutionOwnerConflict):
		translated = automationfleetdb.ErrTransportExecutionOwnerConflict
	case errors.Is(err, infrafleetdb.ErrAutomationIdempotencyConflict):
		translated = automationfleetdb.ErrTransportIdempotencyConflict
	case errors.Is(err, infrafleetdb.ErrAutomationBindingSnapshotConflict):
		translated = automationfleetdb.ErrTransportBindingSnapshotConflict
	case errors.Is(err, infrafleetdb.ErrAutomationCatalogSnapshotConflict):
		translated = automationfleetdb.ErrTransportCatalogSnapshotConflict
	case errors.Is(err, infrafleetdb.ErrAutomationHopDepthExceeded):
		translated = automationfleetdb.ErrTransportHopDepthExceeded
	case errors.Is(err, infrafleetdb.ErrAutomationCatalogUnavailable):
		translated = automationfleetdb.ErrTransportCatalogUnavailable
	case errors.Is(err, infrafleetdb.ErrAutomationFanoutLimitExceeded):
		translated = automationfleetdb.ErrTransportFanoutLimitExceeded
	case errors.Is(err, infrafleetdb.ErrAutomationAdmissionUnavailable):
		translated = automationfleetdb.ErrTransportAdmissionUnavailable
	case errors.Is(err, infrafleetdb.ErrAutomationAdmissionReplayNotFound):
		translated = automationfleetdb.ErrTransportAdmissionReplayNotFound
	case errors.Is(err, infrafleetdb.ErrAutomationDeliveryNotFound):
		translated = automationfleetdb.ErrTransportDeliveryNotFound
	case errors.Is(err, infrafleetdb.ErrAutomationBindingNotFound), errors.Is(err, domain.ErrNotFound):
		// The legacy trigger-binding CRUD endpoint still reports the shared
		// domain not-found sentinel, while newer atomic Automation operations
		// use the capability-specific binding sentinel. Both represent the
		// same consumer-owned transport condition at this boundary.
		translated = automationfleetdb.ErrTransportNotFound
	case errors.Is(err, infrafleetdb.ErrAutomationDeliveryTransitionConflict):
		translated = automationfleetdb.ErrTransportDeliveryTransitionConflict
	case errors.Is(err, infrafleetdb.ErrAutomationPayloadDigestMismatch):
		translated = automationfleetdb.ErrTransportPayloadDigestMismatch
	case errors.Is(err, infrafleetdb.ErrAutomationCronOccurrenceNotFound):
		translated = automationfleetdb.ErrTransportCronOccurrenceNotFound
	case errors.Is(err, infrafleetdb.ErrAutomationCronCompletionConflict):
		translated = automationfleetdb.ErrTransportCronCompletionConflict
	case errors.Is(err, infrafleetdb.ErrAutomationManagedBindingConflict):
		translated = automationfleetdb.ErrTransportManagedBindingConflict
	case errors.Is(err, infrafleetdb.ErrAutomationInvalid):
		translated = automationfleetdb.ErrTransportInvalid
	default:
		return err
	}
	return errors.Join(translated, err)
}

func cloneAutomationStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	output := make(map[string]string, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}
