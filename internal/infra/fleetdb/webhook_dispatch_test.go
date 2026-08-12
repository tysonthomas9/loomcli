package fleetdb

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// TestTriggerRouteDispatchAndAudit exercises the trigger-routes dispatch client
// plus the read-only trigger event/delivery clients against a mock fleet-db.
func TestTriggerRouteDispatchAndAudit(t *testing.T) {
	const route = "github.pull_request.opened"
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/trigger-routes/"+route:
			var req struct {
				IdempotencyKey   string          `json:"idempotency_key"`
				SourceEventID    string          `json:"source_event_id"`
				EventType        string          `json:"event_type"`
				SubjectRef       string          `json:"subject_ref"`
				SignatureStatus  string          `json:"signature_status"`
				RawPayloadDigest string          `json:"raw_payload_digest"`
				Payload          json.RawMessage `json:"payload"`
			}
			decodeJSONBody(t, r, &req)
			if req.IdempotencyKey != "github:d-1" || req.SignatureStatus != "verified" || req.SourceEventID != "d-1" {
				t.Fatalf("dispatch body = %+v", req)
			}
			if req.EventType != "pull_request" || !strings.HasPrefix(req.RawPayloadDigest, "sha256:") {
				t.Fatalf("dispatch metadata = %+v", req)
			}
			if r.Header.Get("Idempotency-Key") != "github:d-1" {
				t.Fatalf("missing Idempotency-Key header: %q", r.Header.Get("Idempotency-Key"))
			}
			// BREAKING router-v2 wire: deliveries[] only, no top-level run.
			writeJSON(t, w, map[string]any{"deliveries": []map[string]any{{
				"delivery_id": "delivery-event-1", "trigger_binding_id": "binding-1",
				"driver_run_id": "run-1", "status": "dispatched",
			}}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/driver-runs/run-1":
			// The legacy DispatchTriggerRoute wrapper fetches the primary run.
			writeJSON(t, w, domain.DriverRun{WorkspaceKey: "WS", RunID: "run-1", DriverID: "d", DriverVersionID: "v1", Status: domain.DriverRunQueued})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/trigger-events":
			if got := r.URL.Query().Get("source_kind"); got != "github" {
				t.Fatalf("source_kind filter = %q", got)
			}
			if got := r.URL.Query().Get("subject_ref"); got != "issue:TASK-1" {
				t.Fatalf("subject_ref filter = %q", got)
			}
			writeJSON(t, w, map[string]any{"trigger_events": []domain.TriggerEvent{{WorkspaceKey: "WS", EventID: "event-1", SourceKind: "github", EventType: "pull_request", SignatureStatus: "verified"}}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/trigger-events/event-1":
			writeJSON(t, w, domain.TriggerEvent{WorkspaceKey: "WS", EventID: "event-1", SourceKind: "github"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/trigger-deliveries":
			writeJSON(t, w, map[string]any{"trigger_deliveries": []domain.TriggerDelivery{{WorkspaceKey: "WS", DeliveryID: "delivery-event-1", TriggerEventID: "event-1", DriverRunID: "run-1", Status: domain.TriggerDeliveryDispatched}}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/trigger-deliveries/delivery-event-1":
			writeJSON(t, w, domain.TriggerDelivery{WorkspaceKey: "WS", DeliveryID: "delivery-event-1", DriverRunID: "run-1"})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}

	run, err := client.TriggerRoutes().DispatchTriggerRoute(t.Context(), "WS", route, store.TriggerRouteDispatch{
		IdempotencyKey:   "github:d-1",
		SourceEventID:    "d-1",
		EventType:        "pull_request",
		SignatureStatus:  "verified",
		RawPayloadDigest: "sha256:abc",
		Payload:          json.RawMessage(`{"action":"opened"}`),
	})
	if err != nil || run.RunID != "run-1" {
		t.Fatalf("DispatchTriggerRoute = %+v err=%v", run, err)
	}

	events, err := client.TriggerEvents().List(t.Context(), "WS", store.TriggerEventFilter{
		SourceKind: "github", SubjectRef: "issue:TASK-1",
	})
	if err != nil || len(events) != 1 || events[0].EventID != "event-1" {
		t.Fatalf("TriggerEvents.List = %+v err=%v", events, err)
	}
	if ev, err := client.TriggerEvents().Get(t.Context(), "WS", "event-1"); err != nil || ev.SourceKind != "github" {
		t.Fatalf("TriggerEvents.Get = %+v err=%v", ev, err)
	}
	deliveries, err := client.TriggerDeliveries().List(t.Context(), "WS", store.TriggerDeliveryFilter{})
	if err != nil || len(deliveries) != 1 || deliveries[0].DriverRunID != "run-1" {
		t.Fatalf("TriggerDeliveries.List = %+v err=%v", deliveries, err)
	}
	if d, err := client.TriggerDeliveries().Get(t.Context(), "WS", "delivery-event-1"); err != nil || d.DriverRunID != "run-1" {
		t.Fatalf("TriggerDeliveries.Get = %+v err=%v", d, err)
	}
}

// TestTriggerBindingWebhookSecret verifies the secret is sent on create but the
// binding read surface is redacted, and that ResolveWebhookSecret fetches it
// from the dedicated privileged endpoint.
func TestTriggerBindingWebhookSecret(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/trigger-bindings":
			var req map[string]any
			decodeJSONBody(t, r, &req)
			if req["webhook_secret"] != "topsecret" {
				t.Fatalf("webhook_secret not sent on create: %v", req["webhook_secret"])
			}
			// Server redacts the secret on the create response.
			writeJSON(t, w, domain.TriggerBinding{WorkspaceKey: "WS", BindingID: "b1", RouteKey: "github.push", Enabled: true})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/trigger-bindings/b1/webhook-secret":
			writeJSON(t, w, map[string]any{"binding_id": "b1", "webhook_secret": "topsecret"})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := client.TriggerBindings().Create(t.Context(), store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "b1", Name: "push", SourceKind: "github",
		RouteKey: "github.push", DriverID: "d", DriverVersionID: "v1",
		WebhookSecret: "topsecret", Enabled: true,
	})
	if err != nil || binding.WebhookSecret != "" {
		t.Fatalf("Create binding should be redacted, got = %+v err=%v", binding, err)
	}
	secret, err := client.TriggerBindings().ResolveWebhookSecret(t.Context(), "WS", "b1")
	if err != nil || secret != "topsecret" {
		t.Fatalf("ResolveWebhookSecret = %q err=%v, want topsecret", secret, err)
	}
}
