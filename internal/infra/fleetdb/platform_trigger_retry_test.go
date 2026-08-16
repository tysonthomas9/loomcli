package fleetdb

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

// triggerRetryTestServer is the fake fleet-db for the trigger-delivery retry
// routes landed on feat/trigger-supersede. It asserts snake_case request
// shapes — including which keys are ABSENT — and responds with snake_case
// bodies the way fleet-db's WriteJSON(models.TriggerDelivery) does.
func triggerRetryTestServer(t *testing.T, now time.Time) *httptest.Server {
	t.Helper()
	retryAt := now.Add(30 * time.Second)
	dueDelivery := map[string]any{
		"workspace_key":      "WS",
		"delivery_id":        "d-1",
		"trigger_event_id":   "event-retry",
		"trigger_binding_id": "binding-retry",
		"subject_key":        "binding-retry|WS-1",
		"status":             "held",
		"attempt":            1,
		"next_retry_at":      retryAt,
		"created_at":         now,
		"updated_at":         now,
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/WS/trigger-deliveries/due":
			q := r.URL.Query()
			if q.Get("limit") != "5" {
				t.Errorf("due query limit = %q, want 5", q.Get("limit"))
			}
			// fleet-db parses now with time.RFC3339 (fractional seconds
			// allowed), exactly like the outbox due route.
			parsed, err := time.Parse(time.RFC3339, q.Get("now"))
			if err != nil || !parsed.Equal(now) {
				t.Errorf("due query now = %q err=%v, want RFC3339 %v", q.Get("now"), err, now)
			}
			writeJSON(t, w, map[string]any{"trigger_deliveries": []map[string]any{dueDelivery}, "count": 1})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/trigger-deliveries/d-1/result":
			raw := map[string]json.RawMessage{}
			decodeJSONBody(t, r, &raw)
			var req struct {
				Status      string     `json:"status"`
				Attempt     int        `json:"attempt"`
				NextRetryAt *time.Time `json:"next_retry_at"`
				ErrorClass  string     `json:"error_class"`
				DriverRunID string     `json:"driver_run_id"`
			}
			remarshal(t, raw, &req)
			updated := cloneJSONMap(dueDelivery)
			updated["status"] = req.Status
			updated["attempt"] = req.Attempt
			updated["error_class"] = req.ErrorClass
			switch req.Status {
			case "failed":
				if req.Attempt != 2 || req.NextRetryAt == nil || !req.NextRetryAt.Equal(retryAt) || req.ErrorClass != "admission_failed" {
					t.Errorf("failed result body = %+v", req)
				}
				updated["next_retry_at"] = *req.NextRetryAt
			case "dispatched":
				if _, ok := raw["next_retry_at"]; ok {
					t.Errorf("dispatched result body carries next_retry_at, want key absent")
				}
				if req.Attempt != 3 || req.DriverRunID != "run-retry-1" {
					t.Errorf("dispatched result body = %+v", req)
				}
				delete(updated, "next_retry_at")
				updated["driver_run_id"] = req.DriverRunID
			default:
				t.Errorf("unexpected result status %q", req.Status)
			}
			writeJSON(t, w, updated)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/trigger-deliveries/missing/result":
			w.WriteHeader(http.StatusNotFound)
			writeJSON(t, w, map[string]any{"error": map[string]string{"code": "not_found", "message": "trigger delivery not found"}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/WS/trigger-deliveries/d-final/result":
			w.WriteHeader(http.StatusConflict)
			writeJSON(t, w, map[string]any{"error": map[string]string{"code": "invalid_transition", "message": "delivery already dispatched"}})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func remarshal(t *testing.T, raw map[string]json.RawMessage, out any) {
	t.Helper()
	data, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("remarshal: %v", err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("remarshal decode: %v", err)
	}
}

func TestPlatformTriggerDeliveryRetryClientRoutes(t *testing.T) {
	now := time.Date(2026, 6, 12, 10, 0, 0, 0, time.UTC)
	ts := triggerRetryTestServer(t, now)
	defer ts.Close()
	client, err := New(Config{BaseURL: ts.URL, Actor: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	retryAt := now.Add(30 * time.Second)

	due, err := client.TriggerDeliveries().ListDue(t.Context(), "WS", automation.TriggerDeliveryDueFilter{Now: now, Limit: 5})
	if err != nil {
		t.Fatalf("ListDue: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("ListDue = %+v, want one delivery", due)
	}
	if d := due[0]; d.DeliveryID != "d-1" || d.Status != automation.DeliveryHeld ||
		d.SubjectKey != "binding-retry|WS-1" || d.TriggerBindingID != "binding-retry" ||
		d.NextRetryAt == nil || !d.NextRetryAt.Equal(retryAt) {
		t.Fatalf("ListDue delivery = %+v, want held d-1 with next_retry_at %v", d, retryAt)
	}

	// Failed reschedule: next_retry_at and error_class ride the wire.
	failed, err := client.TriggerDeliveries().UpdateResult(t.Context(), "WS", "d-1", automation.TriggerDeliveryResultUpdate{
		Status:      automation.DeliveryFailed,
		Attempt:     2,
		NextRetryAt: &retryAt,
		ErrorClass:  "admission_failed",
	})
	if err != nil {
		t.Fatalf("UpdateResult failed: %v", err)
	}
	if failed.Status != automation.DeliveryFailed || failed.Attempt != 2 ||
		failed.ErrorClass != "admission_failed" || failed.NextRetryAt == nil || !failed.NextRetryAt.Equal(retryAt) {
		t.Fatalf("UpdateResult failed = %+v", failed)
	}

	// Dispatch: no next_retry_at key on the wire, run id stamped.
	dispatched, err := client.TriggerDeliveries().UpdateResult(t.Context(), "WS", "d-1", automation.TriggerDeliveryResultUpdate{
		Status:      automation.DeliveryDispatched,
		Attempt:     3,
		DriverRunID: "run-retry-1",
	})
	if err != nil {
		t.Fatalf("UpdateResult dispatched: %v", err)
	}
	if dispatched.Status != automation.DeliveryDispatched || dispatched.DriverRunID != "run-retry-1" || dispatched.NextRetryAt != nil {
		t.Fatalf("UpdateResult dispatched = %+v", dispatched)
	}

	// Error mapping: 404 -> ErrNotFound, 409 invalid_transition ->
	// ErrInvalidTransition (fleet-db's writeStorageError shapes).
	if _, err := client.TriggerDeliveries().UpdateResult(t.Context(), "WS", "missing", automation.TriggerDeliveryResultUpdate{Status: automation.DeliveryFailed}); !errors.Is(err, persistence.ErrNotFound) {
		t.Fatalf("missing delivery err = %v, want ErrNotFound", err)
	}
	if _, err := client.TriggerDeliveries().UpdateResult(t.Context(), "WS", "d-final", automation.TriggerDeliveryResultUpdate{Status: automation.DeliveryFailed}); !errors.Is(err, persistence.ErrInvalidTransition) {
		t.Fatalf("final delivery err = %v, want ErrInvalidTransition", err)
	}
}
