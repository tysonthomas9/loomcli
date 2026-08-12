package trigger_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/infra/memstore"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
)

func setupTaskReviewBinding(t *testing.T, s *memstore.Store) {
	t.Helper()
	if _, err := s.Drivers().Create(t.Context(), store.DriverCreate{
		WorkspaceKey: "WS", DriverID: "prompt-agent", Name: "prompt-agent",
		OwnerType: domain.DriverOwnerSystem, Status: domain.DriverStatusActive,
	}); err != nil {
		t.Fatalf("Create driver: %v", err)
	}
	if _, err := s.DriverVersions().Create(t.Context(), store.DriverVersionCreate{
		WorkspaceKey: "WS", VersionID: "review-v1", DriverID: "prompt-agent", Version: 1,
		SourceDigest: "sha256:s", BundleDigest: "sha256:b",
		ValidationStatus: domain.DriverVersionValidationPassed,
	}); err != nil {
		t.Fatalf("Create driver version: %v", err)
	}
	if _, err := s.TriggerBindings().Create(t.Context(), store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "b-task-review", Name: "b-task-review",
		SourceKind: "internal", RouteKey: "internal." + trigger.TaskReviewEventType,
		SourceConfigRef: `{"roleName":"documentation-agent"}`,
		DriverID:        "prompt-agent", DriverVersionID: "review-v1", TargetEntrypoint: "run",
		ConcurrencyPolicy: domain.TriggerBindingConcurrencyAllow, Enabled: true,
	}); err != nil {
		t.Fatalf("Create trigger binding: %v", err)
	}
}

func setupSecondTaskReviewBinding(t *testing.T, s *memstore.Store) {
	t.Helper()
	if _, err := s.TriggerBindings().Create(t.Context(), store.TriggerBindingCreate{
		WorkspaceKey: "WS", BindingID: "b-task-review-second", Name: "b-task-review-second",
		SourceKind: "internal", RouteKey: "internal.task.review.secondary",
		EventTypePatterns: []string{"internal." + trigger.TaskReviewEventType},
		SourceConfigRef:   `{"roleName":"second-review-agent"}`,
		DriverID:          "prompt-agent", DriverVersionID: "review-v1", TargetEntrypoint: "run",
		ConcurrencyPolicy: domain.TriggerBindingConcurrencyAllow, Enabled: true,
	}); err != nil {
		t.Fatalf("Create second trigger binding: %v", err)
	}
}

func TestIssueJournalBridgeTaskReviewDispatchesInternalRoute(t *testing.T) {
	eventID := "review-route-1"
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {
			events: []store.JournalEvent{{
				ID: eventID, Action: "issue.update", Actor: "user:alice", EntityID: "TASK-REVIEW-1",
				Before: json.RawMessage(`{"status":"in_progress"}`),
				After:  json.RawMessage(`{"status":"review"}`),
			}},
			next: eventID,
		},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	s := memstore.New()
	setupTaskReviewBinding(t, s)
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: &trigger.InternalSource{Store: s}, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReview: true,
		IssueLookup: func(context.Context, string, string) (trigger.TaskReadySnapshot, error) {
			return trigger.TaskReadySnapshot{
				TaskID: "TASK-REVIEW-1", Status: "review", HasDesign: true,
				IssueType: "task", SourceRepo: "acme/widget", Labels: []string{"docs"},
			}, nil
		},
	}

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.TaskReviewEmitted != 1 {
		t.Fatalf("result = %+v, want one task.review", out)
	}

	events, err := s.TriggerEvents().List(t.Context(), "WS", store.TriggerEventFilter{})
	if err != nil {
		t.Fatalf("List trigger events: %v", err)
	}
	if len(events) != 1 || events[0].EventType != trigger.TaskReviewEventType {
		t.Fatalf("events = %+v, want one task.review routed by internal.task.review", events)
	}

	runs, err := s.DriverRuns().List(t.Context(), "WS", store.DriverRunFilter{})
	if err != nil || len(runs) != 1 {
		t.Fatalf("DriverRuns = %+v, %v; want one documentation-agent run", runs, err)
	}
	var envelope struct {
		Event json.RawMessage `json:"event"`
	}
	if err := json.Unmarshal(runs[0].Payload, &envelope); err != nil {
		t.Fatalf("decode run envelope: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(envelope.Event, &payload); err != nil {
		t.Fatalf("decode event payload: %v", err)
	}
	if payload["taskId"] != "TASK-REVIEW-1" ||
		payload["status"] != "review" ||
		payload["previousStatus"] != "in_progress" {
		t.Fatalf("run event payload = %#v", payload)
	}
}

func TestIssueJournalBridgeTaskReviewFansOutInitialAndLegacyButSuppressesProvenSuccessor(t *testing.T) {
	reader := &fakeIssueJournalReader{pages: map[string]journalPage{
		"": {
			events: []store.JournalEvent{
				{
					ID: "review-fanout-initial", Action: "issue.update", Actor: "user:alice",
					EntityID: "TASK-REVIEW-FANOUT",
					Before:   json.RawMessage(`{"status":"open"}`),
					After:    json.RawMessage(`{"status":"review"}`),
				},
				{
					ID: "review-fanout-successor", Action: "issue.update", Actor: "driver-run:review-b",
					EntityID: "TASK-REVIEW-FANOUT",
					Before:   json.RawMessage(`{"status":"in_progress"}`),
					After:    json.RawMessage(`{"status":"review"}`),
					Metadata: map[string]string{
						"projection_applied":                 "atomic",
						"review_trigger_policy":              "suppress_successor",
						"review_trigger_suppress_binding_id": "b-task-review-second",
					},
				},
				{
					ID: "review-fanout-legacy", Action: "issue.update", Actor: "driver-run:legacy",
					EntityID: "TASK-REVIEW-FANOUT",
					Before:   json.RawMessage(`{"status":"in_progress"}`),
					After:    json.RawMessage(`{"status":"review"}`),
					Metadata: map[string]string{
						"projection_applied":    "atomic",
						"review_trigger_policy": "suppress_self",
					},
				},
			},
			next: "review-fanout-legacy",
		},
	}}
	cursors := newFixedCursorStore()
	seenStart(cursors, "WS")
	s := memstore.New()
	setupTaskReviewBinding(t, s)
	setupSecondTaskReviewBinding(t, s)
	bridge := &trigger.IssueJournalBridge{
		Store: s, Source: &trigger.InternalSource{Store: s}, Reader: reader,
		WorkspaceKey: "WS", Cursors: cursors, EmitTaskReview: true,
		IssueLookup: func(context.Context, string, string) (trigger.TaskReadySnapshot, error) {
			return trigger.TaskReadySnapshot{
				TaskID: "TASK-REVIEW-FANOUT", Status: "review", HasDesign: true,
				IssueType: "task", SourceRepo: "acme/widget",
			}, nil
		},
	}

	out, err := bridge.RunOnce(t.Context())
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.TaskReviewEmitted != 2 {
		t.Fatalf("result=%+v, want initial and legacy events only", out)
	}
	events, err := s.TriggerEvents().List(t.Context(), "WS", store.TriggerEventFilter{})
	if err != nil || len(events) != 2 {
		t.Fatalf("TriggerEvents=%+v err=%v, want two", events, err)
	}
	deliveries, err := s.TriggerDeliveries().List(t.Context(), "WS", store.TriggerDeliveryFilter{})
	if err != nil || len(deliveries) != 4 {
		t.Fatalf("TriggerDeliveries=%+v err=%v, want four", deliveries, err)
	}
	runs, err := s.DriverRuns().List(t.Context(), "WS", store.DriverRunFilter{})
	if err != nil || len(runs) != 4 {
		t.Fatalf("DriverRuns=%+v err=%v, want four", runs, err)
	}
}
