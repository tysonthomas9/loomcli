package driverapi

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/tysonthomas9/loomcli/internal/app/workfloweventing"
	trigger "github.com/tysonthomas9/loomcli/internal/infra/automationruntime"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
)

// emit-event is the workflow lane of the internal-event loopback (chunk C14):
// a running workflow posts event content to the named workfloweventing
// application workflow. The lane is run-scoped: verifyParent proves the caller
// owns a RUNNING DriverRun, the authority provider derives ExecutionAuthority
// from that verified run, and Automation re-derives origin, parent, actor,
// epic, hop depth, source, route, signature status, and idempotency.

// emitEventParams is the camelCase driver-op request wire.
type emitEventParams struct {
	// EventID is the workflow's stable id for this occurrence (required):
	// the idempotency anchor, so SDK retries of the same emission dedup.
	EventID string `json:"eventId"`
	// EventType is the journal-style action or normalized event type
	// (issue.create, issue.created, task.blocked, ...). Required.
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
	if m.workflowEventing == nil {
		return nil, workfloweventing.ErrUnavailable
	}
	result, err := m.workflowEventing.Emit(ctx, workfloweventing.VerifiedRun{
		WorkspaceKey: parent.WorkspaceKey, RunID: parent.RunID, Status: string(parent.Status),
		NodeID: parent.Owner.NodeID, LeaseID: parent.Owner.LeaseID, FencingToken: parent.Owner.FencingToken,
	}, workfloweventing.EmitRequest{
		WorkspaceKey: ws, EventID: params.EventID, EventType: params.EventType,
		SubjectRef: params.SubjectRef, Payload: params.Payload, SubjectAttrs: params.SubjectAttrs,
	})
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("workflow event admission returned no result: %w", automation.ErrInvalidPersistedState)
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
	if !result.Dropped {
		for _, leg := range result.Deliveries {
			if leg != nil {
				resp.Deliveries = append(resp.Deliveries, emitEventDelivery{
					DeliveryID:      leg.DeliveryID,
					BindingID:       leg.TriggerBindingID,
					RunID:           leg.DriverRunID,
					Status:          string(leg.Status),
					RejectionReason: leg.RejectionReason,
				})
			}
		}
	}
	m.notifyWorkflowEventAwaits(ctx, ws, result, params.Payload)
	return resp, nil
}

// notifyWorkflowEventAwaits preserves the existing best-effort AW7 behavior
// after durable admission. Every identity used here is Automation-derived;
// caller-supplied actor/epic fields are intentionally ignored.
func (m *Module) notifyWorkflowEventAwaits(ctx context.Context, ws string, result *automation.AdmissionResult, payload json.RawMessage) {
	if m.eventAwaits == nil || result == nil || result.Dropped || result.Event == nil {
		return
	}
	event := result.Event
	if _, err := m.eventAwaits.Dispatch(ctx, ws, trigger.AwaitDispatchEvent{
		EventID: event.SourceEventID, EventType: event.EventType,
		SourceKind: event.SourceKind, Origin: event.Origin,
		SubjectRef: event.SubjectRef, ActorRef: event.ActorRef, Payload: payload,
	}); err != nil {
		slog.WarnContext(ctx, "workflow event await dispatch failed",
			"workspace", ws, "event_id", event.SourceEventID,
			"event_type", event.EventType, "error", err)
	}
}
