package webhooks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// maxWebhookPayloadBytes caps the inbound webhook body. GitHub deliveries are
// well under this; the limit guards against abusive payloads.
const maxWebhookPayloadBytes = 8 << 20

// Module serves the inbound webhook ingestion route plus read-only listing of
// the persisted TriggerEvent / TriggerDelivery audit trail.
type Module struct {
	store    store.Store
	adapters registry
}

// NewModule constructs the webhooks module backed by the given store. Returns
// nil-safe behavior: with a nil store, Register registers nothing.
func NewModule(st store.Store) *Module {
	return &Module{store: st, adapters: defaultRegistry()}
}

func (m *Module) Register(mux *http.ServeMux) {
	if m.store == nil {
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
	if m.store.TriggerRoutes() == nil {
		writeError(w, http.StatusNotImplemented, "trigger dispatch is unavailable for this store")
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

	// 2-3. Resolve the enabled binding and verify the signature BEFORE dispatch.
	if !m.authorizeWebhook(w, r, adapter, ws, event, body) {
		return
	}
	// 4-5. Dispatch durably and respond.
	m.dispatchWebhook(w, r, ws, name, event, body)
}

// authorizeWebhook resolves the enabled binding for the event's route key,
// resolves the inbound signing secret(s), and verifies the request signature.
// It writes the appropriate error and returns false on any failure.
//
// Signature model (step-7 connectors): verification resolves the per-source
// connector's inbound secret — one rotation point per source — accepting the
// previous secret inside the dual-secret rotation window (stale matches emit
// an audit signal). Bindings whose source kind has no connector yet keep
// verifying against the exact-RouteKey binding's secret (back-compat, no flag
// day). Pattern-matched fan-out bindings need no secrets of their own —
// verification happens here at ingress, before dispatch.
func (m *Module) authorizeWebhook(w http.ResponseWriter, r *http.Request, adapter Adapter, ws string, event NormalizedEvent, body []byte) bool {
	binding, err := m.store.TriggerBindings().GetByRouteKey(r.Context(), ws, event.RouteKey)
	if err != nil {
		writeDomainError(w, err, fmt.Sprintf("no trigger binding for route %q", event.RouteKey))
		return false
	}
	if !binding.Enabled {
		writeError(w, http.StatusNotFound, fmt.Sprintf("trigger binding for route %q is disabled", event.RouteKey))
		return false
	}
	if err := m.verifyInboundSignature(r, adapter, ws, binding, body); err != nil {
		writeAdapterError(w, err)
		return false
	}
	return true
}

// dispatchWebhook normalizes the payload to valid JSON, hands off to the durable
// idempotent fan-out dispatch path (redelivery heals each leg independently),
// and writes the 202 response. The handler still only persists + enqueues —
// it never executes work inline.
func (m *Module) dispatchWebhook(w http.ResponseWriter, r *http.Request, ws, name string, event NormalizedEvent, body []byte) {
	if len(strings.TrimSpace(string(body))) == 0 {
		body = []byte("{}")
	} else if !json.Valid(body) {
		writeError(w, http.StatusBadRequest, "webhook payload must be valid JSON")
		return
	}
	idempotencyKey := name + ":" + event.DeliveryID
	result, err := m.store.TriggerRoutes().DispatchTriggerRouteV2(r.Context(), ws, event.RouteKey, store.TriggerRouteDispatch{
		IdempotencyKey:   idempotencyKey,
		SourceEventID:    event.DeliveryID,
		EventType:        event.EventType,
		SubjectRef:       event.SubjectRef,
		ActorRef:         event.ActorRef,
		SignatureStatus:  "verified",
		RawPayloadDigest: payloadDigest(body),
		Payload:          json.RawMessage(body),
		SubjectAttrs:     event.SubjectAttrs,
	})
	if err != nil {
		writeDomainError(w, err, "dispatch webhook failed")
		return
	}
	// BREAKING router-v2 wire (locked decision): the 202 body carries
	// deliveries[] only — no top-level driver_run_id / driver_run. loom-dev
	// consumers update at redeploy; callers that need the run body fetch it
	// by deliveries[i].driver_run_id.
	writeJSON(w, http.StatusAccepted, map[string]any{
		"status":          "accepted",
		"route_key":       event.RouteKey,
		"idempotency_key": idempotencyKey,
		"deliveries":      result.Deliveries,
	})
}

func (m *Module) listTriggerEvents(w http.ResponseWriter, r *http.Request) {
	limit, err := parseLimit(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	events, err := m.store.TriggerEvents().List(r.Context(), r.PathValue("ws"), store.TriggerEventFilter{
		SourceKind:       strings.TrimSpace(r.URL.Query().Get("source_kind")),
		TriggerBindingID: strings.TrimSpace(r.URL.Query().Get("trigger_binding_id")),
		Limit:            limit,
	})
	if err != nil {
		writeDomainError(w, err, "list trigger events failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"trigger_events": events, "count": len(events)})
}

func (m *Module) getTriggerEvent(w http.ResponseWriter, r *http.Request) {
	event, err := m.store.TriggerEvents().Get(r.Context(), r.PathValue("ws"), r.PathValue("eventId"))
	if err != nil {
		writeDomainError(w, err, "trigger event not found")
		return
	}
	writeJSON(w, http.StatusOK, event)
}

func (m *Module) listTriggerDeliveries(w http.ResponseWriter, r *http.Request) {
	limit, err := parseLimit(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	deliveries, err := m.store.TriggerDeliveries().List(r.Context(), r.PathValue("ws"), store.TriggerDeliveryFilter{
		TriggerEventID:   strings.TrimSpace(r.URL.Query().Get("trigger_event_id")),
		TriggerBindingID: strings.TrimSpace(r.URL.Query().Get("trigger_binding_id")),
		Status:           domain.TriggerDeliveryStatus(strings.TrimSpace(r.URL.Query().Get("status"))),
		Limit:            limit,
	})
	if err != nil {
		writeDomainError(w, err, "list trigger deliveries failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"trigger_deliveries": deliveries, "count": len(deliveries)})
}

func (m *Module) getTriggerDelivery(w http.ResponseWriter, r *http.Request) {
	delivery, err := m.store.TriggerDeliveries().Get(r.Context(), r.PathValue("ws"), r.PathValue("deliveryId"))
	if err != nil {
		writeDomainError(w, err, "trigger delivery not found")
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

func writeDomainError(w http.ResponseWriter, err error, fallback string) {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		writeError(w, http.StatusNotFound, fallback)
	case errors.Is(err, domain.ErrInvalid):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrAlreadyExists):
		writeError(w, http.StatusConflict, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, fallback)
	}
}
