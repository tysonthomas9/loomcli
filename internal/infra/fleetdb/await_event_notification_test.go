package fleetdb

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestAwaitEventNotificationClaimWire(t *testing.T) {
	now := time.Now().UTC()
	client := awaitTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/await-event-notifications/claim" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			ClaimID    string    `json:"claim_id"`
			Before     time.Time `json:"before"`
			ClaimUntil time.Time `json:"claim_until"`
			Limit      int       `json:"limit"`
		}
		decodeJSONBody(t, r, &body)
		if body.ClaimID != "claim-1" || !body.Before.Equal(now) ||
			!body.ClaimUntil.Equal(now.Add(time.Minute)) || body.Limit != 17 {
			t.Fatalf("claim body = %+v", body)
		}
		writeJSON(t, w, map[string]any{
			"notifications": []any{map[string]any{
				"event": map[string]any{
					"workspace_key": "WS", "event_id": "stored-1", "source_event_id": "source-1",
					"source_kind": "webhook", "event_type": "approval.granted", "subject_ref": "deploy-1",
					"actor_ref": "alice", "occurred_at": now, "received_at": now,
					"payload_base64": []byte(`{"approved":true}`),
				},
				"attempt": 2, "durable_event_id": "stored-1", "canonical_event_id": "source-1",
				"payload_oversized": true, "payload_size": 8388609,
			}},
			"count": 1,
		})
	})
	outbox := client.TriggerEvents().(store.AwaitEventNotificationStore)
	values, err := outbox.ClaimAwaitEventNotifications(t.Context(), store.AwaitEventNotificationClaim{
		WorkspaceKey: "WS", ClaimID: "claim-1", Before: now,
		ClaimUntil: now.Add(time.Minute), Limit: 17,
	})
	if err != nil || len(values) != 1 {
		t.Fatalf("claim = %+v, %v", values, err)
	}
	got := values[0]
	if got.Attempt != 2 || got.Event.EventID != "stored-1" || got.Event.SourceEventID != "source-1" ||
		string(got.Event.Payload) != `{"approved":true}` || got.DurableEventID != "stored-1" ||
		got.CanonicalEventID != "source-1" || !got.PayloadOversized || got.PayloadSize != 8388609 {
		t.Fatalf("notification = %+v", got)
	}
}

func TestAwaitEventNotificationCompleteAndRetryWire(t *testing.T) {
	now := time.Now().UTC()
	calls := 0
	client := awaitTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body map[string]json.RawMessage
		decodeJSONBody(t, r, &body)
		if string(body["event_id"]) != `"stored-1"` || string(body["claim_id"]) != `"claim-1"` {
			t.Fatalf("body = %s", mustJSON(body))
		}
		switch calls {
		case 1:
			if r.URL.Path != "/api/v1/WS/await-event-notifications/complete" || body["completed_at"] == nil {
				t.Fatalf("complete request = %s %s", r.Method, r.URL.Path)
			}
		case 2:
			if r.URL.Path != "/api/v1/WS/await-event-notifications/retry" || body["available_at"] == nil ||
				string(body["error"]) != `"temporary"` {
				t.Fatalf("retry request = %s %s body %s", r.Method, r.URL.Path, mustJSON(body))
			}
		default:
			t.Fatalf("unexpected request %d", calls)
		}
		writeJSON(t, w, map[string]bool{"ok": true})
	})
	outbox := client.TriggerEvents().(store.AwaitEventNotificationStore)
	if err := outbox.CompleteAwaitEventNotification(t.Context(), store.AwaitEventNotificationCompletion{
		WorkspaceKey: "WS", EventID: "stored-1", ClaimID: "claim-1", CompletedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := outbox.RetryAwaitEventNotification(t.Context(), store.AwaitEventNotificationRetry{
		WorkspaceKey: "WS", EventID: "stored-1", ClaimID: "claim-1",
		AvailableAt: now.Add(time.Second), Error: "temporary",
	}); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
