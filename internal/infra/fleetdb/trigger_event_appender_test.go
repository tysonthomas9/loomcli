package fleetdb

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

func TestTriggerEventAppenderPreservesTrustedEnvelopeAndPayload(t *testing.T) {
	now := time.Now().UTC()
	payload := json.RawMessage(`{"runId":"child","status":"completed"}`)
	client := awaitTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/WS/trigger-events" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			EventID         string                    `json:"event_id"`
			SourceKind      string                    `json:"source_kind"`
			SourceEventID   string                    `json:"source_event_id"`
			EventType       string                    `json:"event_type"`
			SubjectRef      string                    `json:"subject_ref"`
			ActorRef        string                    `json:"actor_ref"`
			Origin          domain.TriggerEventOrigin `json:"origin"`
			EpicID          string                    `json:"epic_id"`
			OccurredAt      time.Time                 `json:"occurred_at"`
			ReceivedAt      time.Time                 `json:"received_at"`
			IdempotencyKey  string                    `json:"idempotency_key"`
			SignatureStatus string                    `json:"signature_status"`
			PayloadBase64   []byte                    `json:"payload_base64"`
		}
		decodeJSONBody(t, r, &body)
		if body.EventID != "execution-await-event-1" || body.SourceEventID != "run-finished:child:completed" ||
			body.SourceKind != "execution" || body.EventType != "run.finished" || body.SubjectRef != "child" ||
			body.ActorRef != "system" || body.Origin != domain.TriggerEventOriginSystem || body.EpicID != "EPIC-1" ||
			body.IdempotencyKey != "execution-await:1" || body.SignatureStatus != "internal" ||
			!body.OccurredAt.Equal(now) || !body.ReceivedAt.Equal(now) || string(body.PayloadBase64) != string(payload) {
			t.Fatalf("body = %+v payload=%q", body, body.PayloadBase64)
		}
		writeJSON(t, w, map[string]any{
			"workspace_key": "WS", "event_id": body.EventID, "source_kind": body.SourceKind,
			"source_event_id": body.SourceEventID, "event_type": body.EventType,
			"subject_ref": body.SubjectRef, "actor_ref": body.ActorRef, "origin": body.Origin,
			"epic_id": body.EpicID, "occurred_at": body.OccurredAt, "received_at": body.ReceivedAt,
			"idempotency_key": body.IdempotencyKey, "signature_status": body.SignatureStatus,
			"payload_base64": body.PayloadBase64,
		})
	})
	appender := client.TriggerEvents().(store.TriggerEventAppender)
	got, err := appender.AppendTriggerEvent(t.Context(), &domain.TriggerEvent{
		WorkspaceKey: "WS", EventID: "execution-await-event-1", SourceKind: "execution",
		SourceEventID: "run-finished:child:completed", EventType: "run.finished",
		SubjectRef: "child", ActorRef: "system", Origin: domain.TriggerEventOriginSystem,
		EpicID: "EPIC-1", OccurredAt: now, ReceivedAt: now,
		IdempotencyKey: "execution-await:1", SignatureStatus: "internal", Payload: payload,
	})
	if err != nil || got == nil || got.EventID != "execution-await-event-1" ||
		got.SourceEventID != "run-finished:child:completed" || got.EpicID != "EPIC-1" ||
		string(got.Payload) != string(payload) {
		t.Fatalf("AppendTriggerEvent = %+v, %v", got, err)
	}
}

func TestTriggerEventAppenderPreservesSessionAttestation(t *testing.T) {
	client := awaitTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		decodeJSONBody(t, r, &body)
		if string(body["origin"]) != `"external"` || string(body["signature_status"]) != `"session"` ||
			string(body["actor_ref"]) != `"user:alice"` {
			t.Fatalf("session envelope = %s", mustJSON(body))
		}
		writeJSON(t, w, map[string]any{
			"workspace_key": "WS", "event_id": "approval-1", "source_kind": "approval",
			"source_event_id": "approval-1", "event_type": "approval.granted",
			"subject_ref": "deploy-1", "actor_ref": "user:alice", "origin": "external",
			"signature_status": "session", "occurred_at": time.Now().UTC(), "received_at": time.Now().UTC(),
		})
	})
	_, err := client.TriggerEvents().(store.TriggerEventAppender).AppendTriggerEvent(t.Context(), &domain.TriggerEvent{
		WorkspaceKey: "WS", EventID: "approval-1", SourceKind: "approval", SourceEventID: "approval-1",
		EventType: "approval.granted", SubjectRef: "deploy-1", ActorRef: "user:alice",
		Origin: domain.TriggerEventOriginExternal, SignatureStatus: "session",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTriggerEventAppenderRejectsNoncanonicalOrUnattestableEnvelope(t *testing.T) {
	client := awaitTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("invalid envelope reached FleetDB: %s %s", r.Method, r.URL.Path)
	})
	appender := client.TriggerEvents().(store.TriggerEventAppender)
	base := domain.TriggerEvent{
		WorkspaceKey: "WS", EventID: "event-1", SourceKind: "test", SourceEventID: "source-1",
		EventType: "test.event", Origin: domain.TriggerEventOriginSystem,
	}
	tests := []struct {
		name   string
		mutate func(*domain.TriggerEvent)
	}{
		{name: "padded source", mutate: func(event *domain.TriggerEvent) { event.SourceEventID = " source-1 " }},
		{name: "reserved source", mutate: func(event *domain.TriggerEvent) { event.SourceEventID = domain.AwaitTimeoutEventID("run#await-1") }},
		{name: "system parent", mutate: func(event *domain.TriggerEvent) { event.ParentEventID = "parent-1" }},
		{name: "caller hop", mutate: func(event *domain.TriggerEvent) { event.HopDepth = 1 }},
		{name: "unsupported attrs", mutate: func(event *domain.TriggerEvent) { event.SubjectAttrs = map[string]string{"x": "y"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := base
			test.mutate(&event)
			if _, err := appender.AppendTriggerEvent(t.Context(), &event); !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("AppendTriggerEvent error = %v, want ErrInvalid", err)
			}
		})
	}
}
