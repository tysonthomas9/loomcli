package fleetdb

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// TestTriggerRouteFanOutWire pins the BREAKING router-v2 trigger-route wire:
// the POST body keeps its snake_case dispatch fields (and never carries
// subject_attrs until fleet-db's strict decoder accepts it), the
// Idempotency-Key header is forwarded, and the response decodes the
// deliveries[]-only fan-out shape (no top-level driver_run_id). The legacy
// DispatchTriggerRoute wrapper fetches the primary leg's run by id.
func TestTriggerRouteFanOutWire(t *testing.T) {
	canned := []map[string]any{
		{"delivery_id": "delivery-event-1-binding-exact", "trigger_binding_id": "binding-exact",
			"driver_run_id": "run-exact", "status": "dispatched"},
		{"delivery_id": "delivery-event-1-binding-pattern-a", "trigger_binding_id": "binding-pattern-a",
			"driver_run_id": "run-aaa", "status": "dispatched"},
		{"delivery_id": "delivery-event-1-binding-pattern-b", "trigger_binding_id": "binding-pattern-b",
			"driver_run_id": "run-bbb", "status": "held", "rejection_reason": "queue policy hold"},
	}
	dispatches := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/trigger-routes/github.pull_request.opened":
			dispatches++
			if got := r.Header.Get("Idempotency-Key"); got != "fan-1" {
				t.Fatalf("Idempotency-Key = %q, want fan-1", got)
			}
			var body map[string]any
			decodeJSONBody(t, r, &body)
			if body["event_type"] != "pull_request" || body["subject_ref"] != "acme/widgets#7" {
				t.Fatalf("dispatch body = %v, want event_type/subject_ref", body)
			}
			if _, ok := body["subject_attrs"]; ok {
				t.Fatalf("dispatch body sent subject_attrs = %v; fleet-db's strict decoder rejects it", body["subject_attrs"])
			}
			w.WriteHeader(http.StatusCreated)
			writeJSON(t, w, map[string]any{"deliveries": canned})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/driver-runs/run-exact":
			writeJSON(t, w, domain.DriverRun{
				WorkspaceKey: "WS", RunID: "run-exact", DriverID: "pr-review",
				DriverVersionID: "v1", Status: domain.DriverRunQueued, SourceRef: "event-1",
			})
		default:
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}

	in := store.TriggerRouteDispatch{
		IdempotencyKey: "fan-1",
		EventType:      "pull_request",
		SubjectRef:     "acme/widgets#7",
		SubjectAttrs:   map[string]string{"repo": "acme/widgets"},
	}
	result, err := client.TriggerRoutes().DispatchTriggerRouteV2(t.Context(), "WS", "github.pull_request.opened", in)
	if err != nil {
		t.Fatalf("DispatchTriggerRouteV2: %v", err)
	}
	if result.PrimaryRun != nil {
		t.Fatalf("PrimaryRun = %#v, want nil (the router-v2 wire returns no run bodies)", result.PrimaryRun)
	}
	want := []store.TriggerRouteDelivery{
		{DeliveryID: "delivery-event-1-binding-exact", BindingID: "binding-exact", RunID: "run-exact", Status: domain.TriggerDeliveryDispatched},
		{DeliveryID: "delivery-event-1-binding-pattern-a", BindingID: "binding-pattern-a", RunID: "run-aaa", Status: domain.TriggerDeliveryDispatched},
		{DeliveryID: "delivery-event-1-binding-pattern-b", BindingID: "binding-pattern-b", RunID: "run-bbb", Status: domain.TriggerDeliveryHeld, RejectionReason: "queue policy hold"},
	}
	if len(result.Deliveries) != len(want) {
		t.Fatalf("deliveries = %#v, want %d legs", result.Deliveries, len(want))
	}
	for i := range want {
		if result.Deliveries[i] != want[i] {
			t.Fatalf("delivery[%d] = %#v, want %#v", i, result.Deliveries[i], want[i])
		}
	}

	// Legacy wrapper: same dispatch, then a GET for the primary leg's run.
	run, err := client.TriggerRoutes().DispatchTriggerRoute(t.Context(), "WS", "github.pull_request.opened", in)
	if err != nil {
		t.Fatalf("DispatchTriggerRoute: %v", err)
	}
	if run.RunID != "run-exact" || run.Status != domain.DriverRunQueued {
		t.Fatalf("legacy run = %+v, want fetched run-exact (queued)", run)
	}
	if dispatches != 2 {
		t.Fatalf("dispatch POSTs = %d, want 2 (one per call)", dispatches)
	}
}
