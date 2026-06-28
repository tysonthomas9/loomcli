package driverapi

import (
	"context"
	"encoding/json"

	"github.com/tysonthomas9/loomcli/internal/domain"
	driverpkg "github.com/tysonthomas9/loomcli/internal/driver"
	"github.com/tysonthomas9/loomcli/internal/trigger"
)

// emit-event is the workflow lane of the internal-event loopback (chunk C14):
// a running workflow posts an event it produced and the loopback re-enters it
// into the trigger router with structural provenance. The lane is run-scoped:
// verifyParent proves the caller owns a RUNNING DriverRun, the origin is
// forced to workflow (never client-chosen) and the parent trigger event is
// derived from the verified run's SourceRef — the event id the dispatch path
// stamped when it admitted the run — so hop depth accumulates structurally
// and a workflow cannot forge a shallow chain. See
// internal/trigger/internal_source.go for the guard and transport decision.

// emitEventParams is the camelCase driver-op request wire.
type emitEventParams struct {
	// EventID is the workflow's stable id for this occurrence (required):
	// the idempotency anchor, so SDK retries of the same emission dedup.
	EventID string `json:"eventId"`
	// EventType is the journal-style action or normalized event type
	// (issue.create, issue.created, task.parked, ...). Required.
	EventType    string            `json:"eventType"`
	SubjectRef   string            `json:"subjectRef,omitempty"`
	ActorRef     string            `json:"actorRef,omitempty"`
	EpicID       string            `json:"epicId,omitempty"`
	Payload      json.RawMessage   `json:"payload,omitempty"`
	SubjectAttrs map[string]string `json:"subjectAttrs,omitempty"`
}

// emitEventDelivery is one fan-out leg on the camelCase driver wire.
type emitEventDelivery struct {
	DeliveryID      string `json:"deliveryId"`
	BindingID       string `json:"triggerBindingId"`
	RunID           string `json:"driverRunId,omitempty"`
	Status          string `json:"status"`
	RejectionReason string `json:"rejectionReason,omitempty"`
}

// emitEventResponse reports the emission: either dropped by the structural
// self-trigger guard (dropped + dropReason, no deliveries) or dispatched with
// its fan-out legs.
type emitEventResponse struct {
	Dropped    bool                `json:"dropped"`
	DropReason string              `json:"dropReason,omitempty"`
	EventType  string              `json:"eventType"`
	RouteKey   string              `json:"routeKey"`
	Origin     string              `json:"origin"`
	HopDepth   int                 `json:"hopDepth"`
	Deliveries []emitEventDelivery `json:"deliveries"`
}

func (m *Module) emitEvent(ctx context.Context, ws string, id driverIdentity, body []byte) (any, error) {
	params, err := decodeParams[emitEventParams](body)
	if err != nil {
		return nil, err
	}
	parent, err := m.verifyParent(ctx, ws, id)
	if err != nil {
		return nil, err
	}
	result, err := m.internalEvents.Emit(ctx, ws, trigger.InternalEvent{
		EventID:   params.EventID,
		EventType: params.EventType,
		// Structural provenance: the run-scoped lane is always the workflow
		// origin, and the parent event is the verified run's admitting
		// trigger event (SourceRef), never a client-supplied id.
		Origin:         domain.TriggerEventOriginWorkflow,
		ParentEventID:  parent.SourceRef,
		EmittedByRunID: parent.RunID,
		SubjectRef:     params.SubjectRef,
		ActorRef:       firstNonEmpty(params.ActorRef, driverpkg.DriverRunActor(parent.RunID)),
		EpicID:         firstNonEmpty(params.EpicID, parent.EpicID, driverpkg.DriverRunPayloadEpicID(parent.Payload)),
		Payload:        params.Payload,
		SubjectAttrs:   params.SubjectAttrs,
	})
	if err != nil {
		return nil, err
	}
	resp := emitEventResponse{
		Dropped:    result.Dropped,
		DropReason: result.DropReason,
		EventType:  result.EventType,
		RouteKey:   result.RouteKey,
		Origin:     string(result.Origin),
		HopDepth:   result.HopDepth,
		Deliveries: []emitEventDelivery{},
	}
	if result.Dispatch != nil {
		for _, leg := range result.Dispatch.Deliveries {
			resp.Deliveries = append(resp.Deliveries, emitEventDelivery{
				DeliveryID:      leg.DeliveryID,
				BindingID:       leg.BindingID,
				RunID:           leg.RunID,
				Status:          string(leg.Status),
				RejectionReason: leg.RejectionReason,
			})
		}
	}
	return resp, nil
}
