package fleetdb

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// TestTriggerRouteFanOutWire pins the BREAKING router-v2 trigger-route wire:
// the POST body keeps its snake_case dispatch fields, the Idempotency-Key
// header is forwarded, and the response decodes the deliveries[]-only fan-out
// shape (no top-level driver_run_id). The legacy DispatchTriggerRoute wrapper
// fetches the primary leg's run by id.
//
// This dispatch carries attrs, which now ride as subject_attrs (Gate 3) — the
// client used to drop them because fleet-db's decoder rejected the field.
// TestTriggerRouteOmitsEmptySubjectAttrs covers the empty direction, where the
// key must be omitted rather than sent as null.
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
			attrs, ok := body["subject_attrs"].(map[string]any)
			if !ok || attrs["repo"] != "acme/widgets" {
				t.Fatalf("subject_attrs = %#v, want the dispatch's attrs on the wire", body["subject_attrs"])
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

// TestTriggerRouteSendsSubjectAttrs is the loomcli half of Gate 3: a dispatch
// carrying adapter-enriched attrs puts them on the wire as subject_attrs, in
// the string-map shape fleet-db's decoder binds and RenderSubjectKey
// substitutes into {{attrs.<name>}}.
func TestTriggerRouteSendsSubjectAttrs(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/trigger-routes/internal.issue.labeled" {
			t.Fatalf("unexpected request %s %s", r.Method, r.URL.String())
		}
		var body map[string]any
		decodeJSONBody(t, r, &body)
		attrs, ok := body["subject_attrs"].(map[string]any)
		if !ok {
			t.Fatalf("subject_attrs = %#v, want a string map", body["subject_attrs"])
		}
		if attrs["label"] != "needs-review" || attrs["status"] != "open" {
			t.Fatalf("subject_attrs = %#v, want label/status carried verbatim", attrs)
		}
		w.WriteHeader(http.StatusCreated)
		writeJSON(t, w, map[string]any{"deliveries": []map[string]any{
			{"delivery_id": "delivery-1", "trigger_binding_id": "b1",
				"driver_run_id": "run-1", "status": "dispatched"},
		}})
	}))
	defer ts.Close()

	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.TriggerRoutes().DispatchTriggerRouteV2(t.Context(), "WS", "internal.issue.labeled", store.TriggerRouteDispatch{
		IdempotencyKey: "internal:WS:fleet-journal-40#label+needs-review",
		EventType:      "issue.labeled",
		SubjectRef:     "issue:DOGFOOD-42",
		SubjectAttrs:   map[string]string{"label": "needs-review", "status": "open"},
	})
	if err != nil {
		t.Fatalf("DispatchTriggerRouteV2: %v", err)
	}
	if len(result.Deliveries) != 1 {
		t.Fatalf("deliveries = %#v, want 1", result.Deliveries)
	}
}

// TestTriggerRouteOmitsEmptySubjectAttrs pins the compatibility half of the
// wire contract: a dispatch with no attrs must OMIT subject_attrs rather than
// send null or {}. That keeps the request byte-identical to what a pre-Gate-3
// client sends, so a loomcli running ahead of its fleet-db only trips the
// server's strict decoder when it actually has attrs to deliver.
func TestTriggerRouteOmitsEmptySubjectAttrs(t *testing.T) {
	for _, tc := range []struct {
		name  string
		attrs map[string]string
	}{
		{"nil attrs", nil},
		{"empty attrs", map[string]string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				decodeJSONBody(t, r, &body)
				if _, ok := body["subject_attrs"]; ok {
					t.Fatalf("subject_attrs present = %#v, want the key omitted entirely", body["subject_attrs"])
				}
				w.WriteHeader(http.StatusCreated)
				writeJSON(t, w, map[string]any{"deliveries": []map[string]any{}})
			}))
			defer ts.Close()

			client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.TriggerRoutes().DispatchTriggerRouteV2(t.Context(), "WS", "internal.issue.created", store.TriggerRouteDispatch{
				IdempotencyKey: "empty-1",
				EventType:      "issue.created",
				SubjectRef:     "issue:DOGFOOD-1",
				SubjectAttrs:   tc.attrs,
			}); err != nil {
				t.Fatalf("DispatchTriggerRouteV2: %v", err)
			}
		})
	}
}
