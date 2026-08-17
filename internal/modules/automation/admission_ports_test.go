package automation

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/platform/authority"
)

func TestOriginSpecificAdmissionPortsShareCanonicalContentValidation(t *testing.T) {
	tests := []struct {
		name   string
		invoke func(*testHarness, json.RawMessage) error
	}{
		{
			name: "webhook",
			invoke: func(h *testHarness, payload json.RawMessage) error {
				_, err := h.service.AdmitWebhookEvent(t.Context(), h.issueWebhook(ActionAdmitEvent), WebhookEvent{
					WorkspaceKey: "ws", SourceKind: "github", RouteKey: "github.issue.opened",
					SourceEventID: "webhook-1", EventType: "issue.opened", Payload: payload,
				})
				return err
			},
		},
		{
			name: "workflow",
			invoke: func(h *testHarness, payload json.RawMessage) error {
				h.execution.emission = &ExecutionEmissionContext{
					WorkspaceKey: "ws", RunID: "run-1", NodeID: "node-1", LeaseID: "lease-1", FencingToken: 1,
				}
				_, err := h.service.AdmitWorkflowEvent(t.Context(), h.issueExecution(ActionAdmitEvent), WorkflowEvent{
					WorkspaceKey: "ws", SourceEventID: "workflow-1", EventType: "issue.created",
					ExecutionNodeID: "node-1", ExecutionLeaseID: "lease-1", ExecutionFencingToken: 1,
					Payload: payload,
				})
				return err
			},
		},
		{
			name: "system",
			invoke: func(h *testHarness, payload json.RawMessage) error {
				_, err := h.service.AdmitSystemEvent(t.Context(), h.issueSystem(ActionAdmitEvent), SystemEvent{
					WorkspaceKey: "ws", SourceEventID: "system-1", EventType: "issue.created", Payload: payload,
				})
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name+" rejects malformed JSON", func(t *testing.T) {
			h := newTestHarness(t)
			err := test.invoke(h, json.RawMessage(`{"broken"`))
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want %v", err, ErrInvalid)
			}
			if h.persistence.matchCalls != 0 || h.persistence.reserveCalls != 0 {
				t.Fatalf("invalid content reached matching/reservation: %d/%d", h.persistence.matchCalls, h.persistence.reserveCalls)
			}
		})
		t.Run(test.name+" rejects oversized payload", func(t *testing.T) {
			h := newTestHarness(t)
			err := test.invoke(h, json.RawMessage(bytes.Repeat([]byte{'x'}, MaxEventPayloadBytes+1)))
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v, want %v", err, ErrInvalid)
			}
			if h.persistence.matchCalls != 0 || h.persistence.reserveCalls != 0 {
				t.Fatalf("oversized content reached matching/reservation: %d/%d", h.persistence.matchCalls, h.persistence.reserveCalls)
			}
		})
	}
}

func TestWebhookAdmissionNormalizesOnlyAfterVerificationHandoffAndDefensivelyCopies(t *testing.T) {
	h := newTestHarness(t)
	h.persistence.seedBinding(seedBinding("github", "github.issue.opened"))

	empty, err := h.service.AdmitWebhookEvent(t.Context(), h.issueWebhook(ActionAdmitEvent), WebhookEvent{
		WorkspaceKey: "ws", SourceKind: "github", RouteKey: "github.issue.opened",
		SourceEventID: "empty", EventType: "issue.opened", Payload: json.RawMessage(" \n\t"),
	})
	if err != nil {
		t.Fatalf("empty admission: %v", err)
	}
	if empty == nil || empty.Event == nil {
		t.Fatalf("empty admission result = %#v", empty)
	}
	if string(empty.Event.Payload) != `{}` {
		t.Fatalf("normalized payload = %q, want {}", empty.Event.Payload)
	}

	payload := json.RawMessage(`{"issue":42}`)
	attrs := map[string]string{"repo": "loom"}
	result, err := h.service.AdmitWebhookEvent(t.Context(), h.issueWebhook(ActionAdmitEvent), WebhookEvent{
		WorkspaceKey: "ws", SourceKind: "github", RouteKey: "github.issue.opened",
		SourceEventID: "copied", EventType: "issue.opened", Payload: payload, SubjectAttrs: attrs,
	})
	if err != nil {
		t.Fatalf("copy admission: %v", err)
	}
	payload[2] = 'X'
	attrs["repo"] = "mutated"
	if string(result.Event.Payload) != `{"issue":42}` || result.Event.SubjectAttrs["repo"] != "loom" {
		t.Fatalf("caller mutation escaped defensive copy: payload=%s attrs=%v", result.Event.Payload, result.Event.SubjectAttrs)
	}
}

func TestAdmissionChecksTypedAuthorityBeforeCanonicalContent(t *testing.T) {
	h := newTestHarness(t)
	_, err := h.service.AdmitWebhookEvent(t.Context(), h.issueWebhook(ActionCreateBinding), WebhookEvent{
		WorkspaceKey: "ws", SourceKind: "github", RouteKey: "github.issue.opened",
		SourceEventID: "event-1", EventType: "issue.opened", Payload: json.RawMessage(`{"broken"`),
	})
	if !errors.Is(err, authority.ErrAdmissionDenied) {
		t.Fatalf("error = %v, want authority denial before content validation", err)
	}
	if h.persistence.matchCalls != 0 || h.persistence.reserveCalls != 0 {
		t.Fatalf("denied authority reached matching/reservation: %d/%d", h.persistence.matchCalls, h.persistence.reserveCalls)
	}
}
