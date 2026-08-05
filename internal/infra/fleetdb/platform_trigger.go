package fleetdb

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type triggerEventStore struct{ client *Client }

var _ store.TriggerEventStore = (*triggerEventStore)(nil)
var _ store.TriggerEventAppender = (*triggerEventStore)(nil)

// AppendTriggerEvent uses FleetDB's service-auth producer lane. Loom has
// already derived any external session actor at its authenticated application
// boundary; Fleet attests the Loom service and stamps/validates provenance.
func (s *triggerEventStore) AppendTriggerEvent(
	ctx context.Context,
	event *domain.TriggerEvent,
) (*domain.TriggerEvent, error) {
	if event == nil || event.WorkspaceKey == "" || event.SourceKind == "" || event.EventType == "" {
		return nil, fmt.Errorf("append trigger event requires workspace, source kind, and event type: %w", domain.ErrInvalid)
	}
	canonicalID, canonical := event.CanonicalEventID()
	validProvenance := false
	switch event.Origin {
	case domain.TriggerEventOriginSystem:
		validProvenance = event.ParentEventID == ""
	case domain.TriggerEventOriginExternal:
		validProvenance = event.ParentEventID == "" && event.SignatureStatus == "session" && event.ActorRef != ""
	case "", domain.TriggerEventOriginWorkflow:
		validProvenance = true
	}
	if !canonical || domain.IsAwaitTimeoutEventID(canonicalID) || event.RouteKey != "" ||
		event.EmittingRunID != "" || event.HopDepth != 0 || len(event.SubjectAttrs) != 0 ||
		!validProvenance {
		return nil, fmt.Errorf("append trigger event envelope is invalid: %w", domain.ErrInvalid)
	}
	body := map[string]any{
		"event_id": event.EventID, "trigger_binding_id": event.TriggerBindingID,
		"source_kind": event.SourceKind, "source_event_id": event.SourceEventID,
		"event_type": event.EventType, "subject_ref": event.SubjectRef, "actor_ref": event.ActorRef,
		"origin": event.Origin, "parent_event_id": event.ParentEventID, "epic_id": event.EpicID,
		"occurred_at": event.OccurredAt, "received_at": event.ReceivedAt,
		"idempotency_key": event.IdempotencyKey,
		"raw_payload_ref": event.RawPayloadRef, "raw_payload_digest": event.RawPayloadDigest,
		"signature_status": event.SignatureStatus, "replay_of_event_id": event.ReplayOfEventID,
		"payload_base64": append([]byte(nil), event.Payload...),
	}
	var out automationEventWire
	path := "/api/v1/" + pathEscape(event.WorkspaceKey) + "/trigger-events"
	if err := s.client.do(ctx, "POST", path, body, &out); err != nil {
		return nil, err
	}
	return out.event(), nil
}

func (s *triggerEventStore) Get(ctx context.Context, ws, eventID string) (*domain.TriggerEvent, error) {
	var out automationEventWire
	path := "/api/v1/" + pathEscape(ws) + "/trigger-events/" + pathEscape(eventID)
	if err := s.client.do(ctx, "GET", path, nil, &out); err != nil {
		return nil, err
	}
	return out.event(), nil
}

func (s *triggerEventStore) List(ctx context.Context, ws string, filter store.TriggerEventFilter) ([]*domain.TriggerEvent, error) {
	q := url.Values{}
	if filter.SourceKind != "" {
		q.Set("source_kind", filter.SourceKind)
	}
	if filter.TriggerBindingID != "" {
		q.Set("trigger_binding_id", filter.TriggerBindingID)
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := "/api/v1/" + pathEscape(ws) + "/trigger-events"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var resp struct {
		TriggerEvents []automationEventWire `json:"trigger_events"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]*domain.TriggerEvent, 0, len(resp.TriggerEvents))
	for index := range resp.TriggerEvents {
		out = append(out, resp.TriggerEvents[index].event())
	}
	return out, nil
}

type triggerDeliveryStore struct{ client *Client }

var _ store.TriggerDeliveryStore = (*triggerDeliveryStore)(nil)

func (s *triggerDeliveryStore) Get(ctx context.Context, ws, deliveryID string) (*domain.TriggerDelivery, error) {
	var out domain.TriggerDelivery
	path := "/api/v1/" + pathEscape(ws) + "/trigger-deliveries/" + pathEscape(deliveryID)
	if err := s.client.do(ctx, "GET", path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *triggerDeliveryStore) List(ctx context.Context, ws string, filter store.TriggerDeliveryFilter) ([]*domain.TriggerDelivery, error) {
	q := url.Values{}
	if filter.TriggerEventID != "" {
		q.Set("trigger_event_id", filter.TriggerEventID)
	}
	if filter.TriggerBindingID != "" {
		q.Set("trigger_binding_id", filter.TriggerBindingID)
	}
	if filter.Status != "" {
		q.Set("status", string(filter.Status))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := "/api/v1/" + pathEscape(ws) + "/trigger-deliveries"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}
	var resp struct {
		TriggerDeliveries []*domain.TriggerDelivery `json:"trigger_deliveries"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.TriggerDeliveries == nil {
		resp.TriggerDeliveries = []*domain.TriggerDelivery{}
	}
	return resp.TriggerDeliveries, nil
}
