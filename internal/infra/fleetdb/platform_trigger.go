package fleetdb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

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
	event *automation.Event,
) (*automation.Event, error) {
	if event == nil || event.WorkspaceKey == "" || event.SourceKind == "" || event.EventType == "" {
		return nil, fmt.Errorf("append trigger event requires workspace, source kind, and event type: %w", domain.ErrInvalid)
	}
	canonicalID, canonical := event.CanonicalEventID()
	validProvenance := false
	switch event.Origin {
	case automation.EventOriginSystem:
		validProvenance = event.ParentEventID == ""
	case automation.EventOriginExternal:
		validProvenance = event.ParentEventID == "" && event.SignatureStatus == "session" && event.ActorRef != ""
	case "", automation.EventOriginWorkflow:
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

func (s *triggerEventStore) Get(ctx context.Context, ws, eventID string) (*automation.Event, error) {
	var out automationEventWire
	path := "/api/v1/" + pathEscape(ws) + "/trigger-events/" + pathEscape(eventID)
	if err := s.client.do(ctx, "GET", path, nil, &out); err != nil {
		return nil, err
	}
	return out.event(), nil
}

func (s *triggerEventStore) List(ctx context.Context, ws string, filter store.TriggerEventFilter) ([]*automation.Event, error) {
	q := url.Values{}
	if filter.SourceKind != "" {
		q.Set("source_kind", filter.SourceKind)
	}
	if filter.TriggerBindingID != "" {
		q.Set("trigger_binding_id", filter.TriggerBindingID)
	}
	if filter.SubjectRef != "" {
		q.Set("subject_ref", filter.SubjectRef)
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
	out := make([]*automation.Event, 0, len(resp.TriggerEvents))
	for index := range resp.TriggerEvents {
		out = append(out, resp.TriggerEvents[index].event())
	}
	return out, nil
}

type triggerDeliveryStore struct{ client *Client }

var _ store.TriggerDeliveryStore = (*triggerDeliveryStore)(nil)

func (s *triggerDeliveryStore) Get(ctx context.Context, ws, deliveryID string) (*automation.Delivery, error) {
	var out automation.Delivery
	path := "/api/v1/" + pathEscape(ws) + "/trigger-deliveries/" + pathEscape(deliveryID)
	if err := s.client.do(ctx, "GET", path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *triggerDeliveryStore) List(ctx context.Context, ws string, filter store.TriggerDeliveryFilter) ([]*automation.Delivery, error) {
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
		TriggerDeliveries []*automation.Delivery `json:"trigger_deliveries"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.TriggerDeliveries == nil {
		resp.TriggerDeliveries = []*automation.Delivery{}
	}
	return resp.TriggerDeliveries, nil
}

var _ store.AwaitEventNotificationStore = (*triggerEventStore)(nil)

func (s *triggerEventStore) ClaimAwaitEventNotifications(
	ctx context.Context,
	claim store.AwaitEventNotificationClaim,
) ([]store.AwaitEventNotification, error) {
	body := map[string]any{
		"claim_id": claim.ClaimID, "before": claim.Before,
		"claim_until": claim.ClaimUntil, "limit": claim.Limit,
	}
	var response struct {
		Notifications []struct {
			Event            automationEventWire `json:"event"`
			Attempt          int                 `json:"attempt"`
			DurableEventID   string              `json:"durable_event_id"`
			CanonicalEventID string              `json:"canonical_event_id"`
			PayloadOversized bool                `json:"payload_oversized"`
			PayloadSize      int                 `json:"payload_size"`
		} `json:"notifications"`
	}
	path := "/api/v1/" + pathEscape(claim.WorkspaceKey) + "/await-event-notifications/claim"
	if err := s.client.do(ctx, "POST", path, body, &response); err != nil {
		return nil, err
	}
	out := make([]store.AwaitEventNotification, 0, len(response.Notifications))
	for _, notification := range response.Notifications {
		event := notification.Event.event()
		if event == nil {
			continue
		}
		out = append(out, store.AwaitEventNotification{
			Event: *event, Attempt: notification.Attempt,
			DurableEventID: notification.DurableEventID, CanonicalEventID: notification.CanonicalEventID,
			PayloadOversized: notification.PayloadOversized, PayloadSize: notification.PayloadSize,
		})
	}
	return out, nil
}

func (s *triggerEventStore) CompleteAwaitEventNotification(
	ctx context.Context,
	completion store.AwaitEventNotificationCompletion,
) error {
	completedAt := completion.CompletedAt
	if completedAt.IsZero() {
		completedAt = time.Now().UTC()
	}
	body := map[string]any{
		"event_id": completion.EventID, "claim_id": completion.ClaimID, "completed_at": completedAt,
	}
	path := "/api/v1/" + pathEscape(completion.WorkspaceKey) + "/await-event-notifications/complete"
	return s.client.do(ctx, "POST", path, body, nil)
}

func (s *triggerEventStore) RetryAwaitEventNotification(
	ctx context.Context,
	retry store.AwaitEventNotificationRetry,
) error {
	body := map[string]any{
		"event_id": retry.EventID, "claim_id": retry.ClaimID,
		"available_at": retry.AvailableAt, "error": retry.Error,
	}
	path := "/api/v1/" + pathEscape(retry.WorkspaceKey) + "/await-event-notifications/retry"
	return s.client.do(ctx, "POST", path, body, nil)
}

// IssueJournalReader is the OPTIONAL store.IssueJournalReader capability,
// served by the fleet-db backend only (memstore deliberately omits it — the
// A4 bridge is capability-gated on its presence). It is implemented on the
// triggerEventStore so it rides the same TriggerEventStore type assertion as
// TriggerEventAppender; there is no fleet-db change, the issue journal is just
// the entity_type=issue slice of GET /events/mutations.
var _ store.IssueJournalReader = (*triggerEventStore)(nil)

// mutationsResponse mirrors fleet-db's MutationsResponse (api/mutations.go).
// Wire is snake_case v1; cursor is the opaque resume token echoed back, and
// has_more reports a full page.
type mutationsResponse struct {
	Events  []journalEventWire `json:"events"`
	Cursor  string             `json:"cursor"`
	HasMore bool               `json:"has_more"`
}

// journalEventWire mirrors fleet-db's EventResponse (api/event_types.go). The
// before/after fields arrive as JSON-encoded strings (the entity snapshot is
// itself serialized JSON, not an inline object); both are unwrapped into raw
// JSON for the projection.
type journalEventWire struct {
	ID         string            `json:"id"`
	Timestamp  time.Time         `json:"timestamp"`
	Actor      string            `json:"actor"`
	Action     string            `json:"action"`
	EntityType string            `json:"entity_type"`
	EntityID   string            `json:"entity_id"`
	Before     string            `json:"before"`
	After      string            `json:"after"`
	Metadata   map[string]string `json:"metadata"`
}

// ListIssueEvents fetches issue mutations strictly after afterCursor, oldest
// first, by polling GET /api/v1/{ws}/events/mutations?entity_type=issue with a
// since-cursor and limit. The cursor is opaque and passed through verbatim;
// "" maps to fleet-db's "0" beginning-of-stream sentinel. nextCursor is the
// response Cursor (the resume position), and hasMore is the response has_more.
//
// A malformed before/after state on any one event is skipped (that field is
// left nil) rather than failing the whole batch — a single poisoned snapshot
// must not stall the bridge's forward progress.
func (s *triggerEventStore) ListIssueEvents(ctx context.Context, ws, afterCursor string, limit int) ([]store.JournalEvent, string, bool, error) {
	q := url.Values{}
	q.Set("entity_type", "issue")
	since := afterCursor
	if since == "" {
		since = "0"
	}
	q.Set("since", since)
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := withQuery("/api/v1/"+pathEscape(ws)+"/events/mutations", q)

	var resp mutationsResponse
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, "", false, err
	}

	events := make([]store.JournalEvent, 0, len(resp.Events))
	for _, e := range resp.Events {
		events = append(events, store.JournalEvent{
			ID:        e.ID,
			Action:    e.Action,
			Actor:     e.Actor,
			EntityID:  e.EntityID,
			Timestamp: e.Timestamp,
			Before:    unwrapJournalSnapshot(e.Before),
			After:     unwrapJournalSnapshot(e.After),
			Metadata:  e.Metadata,
		})
	}

	// fleet-db echoes the client's cursor when the batch is empty, so the
	// caller's afterCursor is the natural resume point; prefer the server's
	// value when present.
	nextCursor := resp.Cursor
	if nextCursor == "" {
		nextCursor = afterCursor
	}
	return events, nextCursor, resp.HasMore, nil
}

// unwrapJournalSnapshot turns a fleet-db JSON-encoded entity-state string into
// raw JSON. Empty or malformed input yields nil — a missing/poisoned snapshot
// is skipped rather than propagated as a hard error (see ListIssueEvents).
func unwrapJournalSnapshot(snapshot string) json.RawMessage {
	if snapshot == "" {
		return nil
	}
	raw := json.RawMessage(snapshot)
	if !json.Valid(raw) {
		return nil
	}
	return bytes.TrimSpace(raw)
}

// ListDue returns deliveries awaiting the retry sweeper whose due score is
// <= filter.Now, in due order. A zero Now is omitted from the query; the
// server then cuts off at its own current time.
func (s *triggerDeliveryStore) ListDue(ctx context.Context, ws string, filter store.TriggerDeliveryDueFilter) ([]*automation.Delivery, error) {
	q := url.Values{}
	if !filter.Now.IsZero() {
		q.Set("now", filter.Now.UTC().Format(time.RFC3339Nano))
	}
	if filter.Limit > 0 {
		q.Set("limit", strconv.Itoa(filter.Limit))
	}
	path := withQuery("/api/v1/"+pathEscape(ws)+"/trigger-deliveries/due", q)
	var resp struct {
		TriggerDeliveries []*automation.Delivery `json:"trigger_deliveries"`
	}
	if err := s.client.do(ctx, "GET", path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.TriggerDeliveries == nil {
		resp.TriggerDeliveries = []*automation.Delivery{}
	}
	return resp.TriggerDeliveries, nil
}

// UpdateResult records one attempt outcome. next_retry_at is only sent when
// set (mirrors the outbox MarkResult wire); a final delivery transitioning
// to a different status surfaces as domain.ErrInvalidTransition via the 409
// invalid_transition mapping in classifyHTTPError.
func (s *triggerDeliveryStore) UpdateResult(ctx context.Context, ws, deliveryID string, update store.TriggerDeliveryResultUpdate) (*automation.Delivery, error) {
	body := map[string]any{
		"status":        update.Status,
		"attempt":       update.Attempt,
		"error_class":   update.ErrorClass,
		"driver_run_id": update.DriverRunID,
	}
	if update.NextRetryAt != nil {
		body["next_retry_at"] = update.NextRetryAt
	}
	path := "/api/v1/" + pathEscape(ws) + "/trigger-deliveries/" + pathEscape(deliveryID) + "/result"
	var out automation.Delivery
	if err := s.client.do(ctx, "POST", path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type triggerRouteStore struct{ client *Client }

var _ store.TriggerRouteDispatcher = (*triggerRouteStore)(nil)

// DispatchTriggerRoute is the legacy single-run lane over the router-v2 wire:
// it dispatches via DispatchTriggerRouteV2 and then fetches the primary leg's
// run, so existing webhook callers keep receiving the admitted run.
func (s *triggerRouteStore) DispatchTriggerRoute(ctx context.Context, ws, routeKey string, in store.TriggerRouteDispatch) (*domain.DriverRun, error) {
	result, err := s.DispatchTriggerRouteV2(ctx, ws, routeKey, in)
	if err != nil {
		return nil, err
	}
	if len(result.Deliveries) == 0 {
		return nil, fmt.Errorf("trigger route %q in workspace %q dispatched no deliveries: %w", routeKey, ws, domain.ErrNotFound)
	}
	var run domain.DriverRun
	runPath := "/api/v1/" + pathEscape(ws) + "/driver-runs/" + pathEscape(result.Deliveries[0].RunID)
	if err := s.client.do(ctx, "GET", runPath, nil, &run); err != nil {
		return nil, err
	}
	return &run, nil
}

// DispatchTriggerRouteV2 posts the trigger-routes endpoint and decodes the
// BREAKING router-v2 fan-out wire: 201 {"deliveries":[...]} with NO top-level
// driver_run_id. PrimaryRun stays nil — the wire no longer returns run bodies;
// callers needing the run fetch it by Deliveries[0].RunID. SubjectAttrs is
// deliberately not sent: fleet-db's strict decoder rejects unknown fields and
// the server-side subject-key templating lane lands with a later chunk.
func (s *triggerRouteStore) DispatchTriggerRouteV2(ctx context.Context, ws, routeKey string, in store.TriggerRouteDispatch) (*store.TriggerRouteDispatchResult, error) {
	body := map[string]any{
		"run_id":             in.RunID,
		"idempotency_key":    in.IdempotencyKey,
		"source_event_id":    in.SourceEventID,
		"event_type":         in.EventType,
		"subject_ref":        in.SubjectRef,
		"actor_ref":          in.ActorRef,
		"epic_id":            in.EpicID,
		"raw_payload_ref":    in.RawPayloadRef,
		"raw_payload_digest": in.RawPayloadDigest,
		"signature_status":   in.SignatureStatus,
		"replay_of_event_id": in.ReplayOfEventID,
		"payload":            in.Payload,
	}
	headers := map[string]string{}
	if in.IdempotencyKey != "" {
		headers["Idempotency-Key"] = in.IdempotencyKey
	}
	var resp struct {
		Deliveries []store.TriggerRouteDelivery `json:"deliveries"`
	}
	path := "/api/v1/" + pathEscape(ws) + "/trigger-routes/" + pathEscape(routeKey)
	if err := s.client.doWithHeaders(ctx, "POST", path, body, &resp, headers); err != nil {
		return nil, err
	}
	return &store.TriggerRouteDispatchResult{Deliveries: resp.Deliveries}, nil
}
