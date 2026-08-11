package webhooks

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/app/webhookingestion"
	trigger "github.com/tysonthomas9/loomcli/internal/infra/automationruntime"
	"github.com/tysonthomas9/loomcli/internal/modules/automation"
)

// maxWebhookPayloadBytes caps the inbound webhook body. GitHub deliveries are
// well under this; the limit guards against abusive payloads.
const maxWebhookPayloadBytes = 8 << 20

// Module serves the inbound webhook ingestion route plus read-only listing of
// the persisted TriggerEvent / TriggerDelivery audit trail.
type Module struct {
	workflow   *webhookingestion.Workflow
	automation AutomationQueries
	adapters   registry
	// awaits is the dispatch-time await matcher (AW7): admitted events are
	// matched against pending await instances right after durable dispatch.
	awaits AwaitDispatcher
}

// AutomationQueries is the read-only Automation surface exposed by the
// trigger-event and trigger-delivery HTTP routes.
type AutomationQueries interface {
	automation.EventQueries
	automation.DeliveryQueries
}

// AwaitDispatcher is the narrow compatibility seam for AW7. Await ownership
// moves to Execution in a later phase; webhook transport does not need the
// composite Store to notify it.
type AwaitDispatcher interface {
	Dispatch(context.Context, string, trigger.AwaitDispatchEvent) (*trigger.AwaitDispatchResult, error)
}

// Config composes webhook transport over the named application workflow and
// Automation's public query APIs.
type Config struct {
	Workflow   *webhookingestion.Workflow
	Automation AutomationQueries
	Awaits     AwaitDispatcher
}

// New constructs the composition-friendly webhook module. Missing ports fail
// closed at request time while all established routes remain registered.
func New(config Config) *Module {
	return &Module{
		workflow: config.Workflow, automation: config.Automation,
		adapters: defaultRegistry(), awaits: config.Awaits,
	}
}

func (m *Module) Register(mux *http.ServeMux) {
	if m == nil || mux == nil {
		return
	}
	mux.HandleFunc("POST /api/workspaces/{ws}/webhooks/{name}", m.receiveWebhook)
	mux.HandleFunc("GET /api/workspaces/{ws}/trigger-events", m.listTriggerEvents)
	mux.HandleFunc("GET /api/workspaces/{ws}/trigger-events/{eventId}", m.getTriggerEvent)
	mux.HandleFunc("GET /api/workspaces/{ws}/trigger-deliveries", m.listTriggerDeliveries)
	mux.HandleFunc("GET /api/workspaces/{ws}/trigger-deliveries/{deliveryId}", m.getTriggerDelivery)
}

func (m *Module) receiveWebhook(w http.ResponseWriter, r *http.Request) {
	ws := r.PathValue("ws")
	name := strings.TrimSpace(r.PathValue("name"))
	adapter, ok := m.adapters[name]
	if !ok {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no webhook adapter registered for %q", name))
		return
	}
	if m.workflow == nil {
		writeError(w, http.StatusServiceUnavailable, "webhook ingestion is unavailable")
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookPayloadBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read webhook payload: "+err.Error())
		return
	}

	// 1. Normalize headers + body into routing metadata (does not trust payload).
	event, err := adapter.Normalize(r, body)
	if err != nil {
		writeAdapterError(w, err)
		return
	}
	// A missing delivery id would collapse every delivery to the same
	// idempotency key, silently dropping all but the first. Require it.
	if strings.TrimSpace(event.DeliveryID) == "" {
		writeError(w, http.StatusBadRequest, "adapter returned an empty delivery id")
		return
	}

	idempotencyKey := name + ":" + event.DeliveryID
	result, err := m.workflow.Ingest(r.Context(), webhookingestion.IngestRequest{
		WorkspaceKey: ws, SourceKind: name, SourceRef: event.RouteKey, RouteKey: event.RouteKey,
		SourceEventID: event.DeliveryID, EventType: event.EventType,
		SubjectRef: event.SubjectRef, ActorRef: event.ActorRef,
		RawPayloadDigest: payloadDigest(normalizedPayload(body)),
		Payload:          json.RawMessage(body), SubjectAttrs: event.SubjectAttrs,
		PresentedSignature: adapter.PresentedSignature(r),
	})
	if err != nil {
		writeIngestionError(w, err)
		return
	}
	// Dispatch-time await matching (AW7) runs after the durable fan-out so a
	// matcher failure can never lose an admitted delivery.
	m.notifyAwaits(r.Context(), ws, name, event, normalizedPayload(body))
	// BREAKING router-v2 wire (locked decision): the 202 body carries
	// deliveries[] only — no top-level driver_run_id / driver_run. loom-dev
	// consumers update at redeploy; callers that need the run body fetch it
	// by deliveries[i].driver_run_id.
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":          "accepted",
		"route_key":       event.RouteKey,
		"idempotency_key": idempotencyKey,
		"deliveries":      deliveryResponses(result.Deliveries),
	})
}

// notifyAwaits hands the admitted event to the dispatch-time await matcher
// (AW7): exact rendered-subject-key lookup against pending awaits, RULE 4
// actor enforcement, atomic resolve + resume of suspended runs. Best-effort
// after durable dispatch — a matcher error must not turn an accepted
// delivery into a webhook failure (redelivery and the deadline machinery
// converge); it is logged with the event identity instead.
func (m *Module) notifyAwaits(ctx context.Context, ws, sourceKind string, event NormalizedEvent, body []byte) {
	if m.awaits == nil {
		return
	}
	if _, err := m.awaits.Dispatch(ctx, ws, trigger.AwaitDispatchEvent{
		EventID:    event.DeliveryID,
		EventType:  event.EventType,
		SourceKind: sourceKind,
		Origin:     automation.EventOriginExternal,
		SubjectRef: event.SubjectRef,
		ActorRef:   event.ActorRef,
		Payload:    json.RawMessage(body),
	}); err != nil {
		slog.WarnContext(ctx, "webhook await dispatch failed",
			"workspace", ws, "source_event_id", event.DeliveryID,
			"event_type", event.EventType, "error", err)
	}
}

func (m *Module) listTriggerEvents(w http.ResponseWriter, r *http.Request) {
	if m.automation == nil {
		writeError(w, http.StatusServiceUnavailable, "automation queries are unavailable")
		return
	}
	limit, err := parseLimit(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	events, err := m.automation.ListEvents(r.Context(), r.PathValue("ws"), automation.EventFilter{
		SourceKind: strings.TrimSpace(r.URL.Query().Get("source_kind")),
		BindingID:  strings.TrimSpace(r.URL.Query().Get("trigger_binding_id")), Limit: limit,
	})
	if err != nil {
		writeAutomationError(w, err, "list trigger events failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"trigger_events": events, "count": len(events)})
}

func (m *Module) getTriggerEvent(w http.ResponseWriter, r *http.Request) {
	if m.automation == nil {
		writeError(w, http.StatusServiceUnavailable, "automation queries are unavailable")
		return
	}
	event, err := m.automation.GetEvent(r.Context(), r.PathValue("ws"), r.PathValue("eventId"))
	if err != nil {
		writeAutomationError(w, err, "trigger event not found")
		return
	}
	writeJSON(w, http.StatusOK, event)
}

func (m *Module) listTriggerDeliveries(w http.ResponseWriter, r *http.Request) {
	if m.automation == nil {
		writeError(w, http.StatusServiceUnavailable, "automation queries are unavailable")
		return
	}
	limit, err := parseLimit(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	deliveries, err := m.automation.ListDeliveries(r.Context(), r.PathValue("ws"), automation.DeliveryFilter{
		EventID:   strings.TrimSpace(r.URL.Query().Get("trigger_event_id")),
		BindingID: strings.TrimSpace(r.URL.Query().Get("trigger_binding_id")),
		Status:    automation.DeliveryStatus(strings.TrimSpace(r.URL.Query().Get("status"))), Limit: limit,
	})
	if err != nil {
		writeAutomationError(w, err, "list trigger deliveries failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"trigger_deliveries": deliveries, "count": len(deliveries)})
}

func (m *Module) getTriggerDelivery(w http.ResponseWriter, r *http.Request) {
	if m.automation == nil {
		writeError(w, http.StatusServiceUnavailable, "automation queries are unavailable")
		return
	}
	delivery, err := m.automation.GetDelivery(r.Context(), r.PathValue("ws"), r.PathValue("deliveryId"))
	if err != nil {
		writeAutomationError(w, err, "trigger delivery not found")
		return
	}
	writeJSON(w, http.StatusOK, delivery)
}

func parseLimit(r *http.Request) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("limit"))
	if raw == "" {
		return 0, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 1 || limit > 1000 {
		return 0, fmt.Errorf("invalid limit: must be 1-1000")
	}
	return limit, nil
}

func payloadDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func normalizedPayload(body []byte) []byte {
	if strings.TrimSpace(string(body)) == "" {
		return []byte("{}")
	}
	return body
}

// deliveryResponse pins the existing webhook deliveries[] wire while the
// underlying model is now owned by Automation.
type deliveryResponse struct {
	DeliveryID      string                    `json:"delivery_id"`
	BindingID       string                    `json:"trigger_binding_id"`
	RunID           string                    `json:"driver_run_id"`
	Status          automation.DeliveryStatus `json:"status"`
	RejectionReason string                    `json:"rejection_reason,omitempty"`
}

func deliveryResponses(deliveries []*automation.Delivery) []deliveryResponse {
	out := make([]deliveryResponse, 0, len(deliveries))
	for _, delivery := range deliveries {
		if delivery == nil {
			continue
		}
		out = append(out, deliveryResponse{
			DeliveryID: delivery.DeliveryID, BindingID: delivery.TriggerBindingID,
			RunID: delivery.DriverRunID, Status: delivery.Status,
			RejectionReason: delivery.RejectionReason,
		})
	}
	return out
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func writeAdapterError(w http.ResponseWriter, err error) {
	var ae *adapterError
	if errors.As(err, &ae) {
		writeError(w, ae.status, ae.message)
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}

func writeIngestionError(w http.ResponseWriter, err error) {
	var ae *adapterError
	if errors.As(err, &ae) {
		writeError(w, ae.status, ae.message)
		return
	}
	if errors.Is(err, webhookingestion.ErrInvalidRequest) || errors.Is(err, automation.ErrInvalid) {
		message := err.Error()
		if strings.Contains(message, "webhook payload must be valid JSON") {
			message = "webhook payload must be valid JSON"
		}
		writeError(w, http.StatusBadRequest, message)
		return
	}
	writeAutomationError(w, err, "dispatch webhook failed")
}

func writeAutomationError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, automation.ErrNotFound), errors.Is(err, automation.ErrNoMatchingBinding):
		writeError(w, http.StatusNotFound, fallback)
	case errors.Is(err, automation.ErrInvalid), errors.Is(err, automation.ErrWrongWorkspace):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, automation.ErrConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, automation.ErrUnavailable):
		writeError(w, http.StatusServiceUnavailable, fallback)
	default:
		writeError(w, http.StatusInternalServerError, fallback)
	}
}
