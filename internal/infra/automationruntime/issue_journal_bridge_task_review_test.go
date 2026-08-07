package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

type taskReviewCaptureEmitter struct {
	events []InternalEvent
	err    error
}

func (e *taskReviewCaptureEmitter) Emit(_ context.Context, _ string, event InternalEvent) (*InternalEmitResult, error) {
	e.events = append(e.events, event)
	return &InternalEmitResult{}, e.err
}

func taskReviewJournalEvent(
	action, actor, before, after string,
	metadata map[string]string,
) store.JournalEvent {
	return store.JournalEvent{
		ID: "review-1", Action: action, Actor: actor, EntityID: "TASK-1",
		Before: json.RawMessage(before), After: json.RawMessage(after), Metadata: metadata,
	}
}

func TestIsTaskReviewEntrySuppressesOnlyAuthorizedReviewSuccessor(t *testing.T) {
	atomic := map[string]string{reviewProjectionMetadataKey: reviewProjectionMetadataValue}
	tests := []struct {
		name     string
		action   string
		actor    string
		before   string
		after    string
		metadata map[string]string
		want     bool
	}{
		{
			name: "human open to review", action: "issue.update", actor: "user:alice",
			before: `{"status":"open"}`, after: `{"status":"review"}`, want: true,
		},
		{
			name: "human in progress to review with marker still emits", action: "issue.update", actor: "user:alice",
			before: `{"status":"in_progress"}`, after: `{"status":"review"}`, metadata: atomic, want: true,
		},
		{
			name: "generic workflow update without marker emits", action: "issue.update", actor: "driver-run:coder",
			before: `{"status":"in_progress"}`, after: `{"status":"review"}`, want: true,
		},
		{
			name: "authorized driver review-role restoration enters admission lane", action: "issue.update", actor: " driver-run:docs ",
			before: `{"status":"in_progress"}`, after: `{"status":"review"}`,
			metadata: map[string]string{
				reviewProjectionMetadataKey:     reviewProjectionMetadataValue,
				reviewTriggerPolicyMetadataKey:  reviewTriggerPolicySuppressSuccessor,
				reviewTriggerSuppressBindingKey: "binding-review-origin",
			}, want: true,
		},
		{
			name: "bug triage atomic handoff without discriminator still emits", action: "issue.update", actor: "driver-run:bug-triage",
			before: `{"status":"in_progress"}`, after: `{"status":"review"}`,
			metadata: atomic, want: true,
		},
		{
			name: "task run atomic completion emits", action: "issue.update", actor: "task-run:docs",
			before: `{"status":"IN_PROGRESS"}`, after: `{"status":"Review"}`, metadata: atomic, want: true,
		},
		{
			name: "non-exact marker does not suppress", action: "issue.update", actor: "driver-run:docs",
			before: `{"status":"in_progress"}`, after: `{"status":"review"}`,
			metadata: map[string]string{
				reviewProjectionMetadataKey:    "ATOMIC",
				reviewTriggerPolicyMetadataKey: reviewTriggerPolicySuppressSuccessor,
			}, want: true,
		},
		{
			name: "non-exact review trigger policy does not suppress", action: "issue.update", actor: "driver-run:docs",
			before: `{"status":"in_progress"}`, after: `{"status":"review"}`,
			metadata: map[string]string{
				reviewProjectionMetadataKey:    reviewProjectionMetadataValue,
				reviewTriggerPolicyMetadataKey: "SUPPRESS_SELF",
			}, want: true,
		},
		{
			name: "workflow atomic open to review is not restoration", action: "issue.update", actor: "driver-run:docs",
			before: `{"status":"open"}`, after: `{"status":"review"}`, metadata: atomic, want: true,
		},
		{
			name: "missing before fails closed", action: "issue.update", actor: "user:alice",
			before: ``, after: `{"status":"review"}`, want: false,
		},
		{
			name: "already review is not transition", action: "issue.update", actor: "user:alice",
			before: `{"status":"review"}`, after: `{"status":"review"}`, want: false,
		},
		{
			name: "wrong action", action: "issue.create", actor: "user:alice",
			before: `{"status":"open"}`, after: `{"status":"review"}`, want: false,
		},
		{
			name: "after is not review", action: "issue.update", actor: "user:alice",
			before: `{"status":"open"}`, after: `{"status":"blocked"}`, want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := taskReviewJournalEvent(tt.action, tt.actor, tt.before, tt.after, tt.metadata)
			if got := isTaskReviewEntry(ev); got != tt.want {
				t.Fatalf("isTaskReviewEntry() = %v, want %v for %+v", got, tt.want, ev)
			}
		})
	}
}

func TestIssueJournalBridgeTaskReviewAuthorizedRestorationAdmitsRepositoryBeforeSuppressing(t *testing.T) {
	event := taskReviewJournalEvent(
		"issue.update", "driver-run:docs", `{"status":"in_progress"}`, `{"status":"review"}`,
		map[string]string{
			reviewProjectionMetadataKey:     reviewProjectionMetadataValue,
			reviewTriggerPolicyMetadataKey:  reviewTriggerPolicySuppressSuccessor,
			reviewTriggerSuppressBindingKey: "binding-review-origin",
		},
	)
	emitter := &taskReviewCaptureEmitter{}
	admissions := 0
	bridge := &IssueJournalBridge{
		Source: emitter, EmitTaskReview: true,
		IssueLookup: func(context.Context, string, string) (TaskReadySnapshot, error) {
			return TaskReadySnapshot{
				TaskID: "TASK-1", Status: "review", IssueType: "task",
				SourceRepo: "", RepositoryRequired: true,
			}, nil
		},
		RepositoryRequiredBlocker: func(context.Context, string, string) (TaskReadyRepositoryRequiredResult, error) {
			admissions++
			return TaskReadyRepositoryRequiredResult{Blocked: true}, nil
		},
	}

	result := &IssueJournalSweepResult{}
	advanced, err := bridge.emitBatch(t.Context(), "WS", []store.JournalEvent{event}, result)
	if err != nil {
		t.Fatalf("emitBatch: %v", err)
	}
	if advanced != event.ID || admissions != 1 || result.TaskReviewEmitted != 0 || len(emitter.events) != 0 {
		t.Fatalf("advanced=%q admissions=%d result=%+v events=%d", advanced, admissions, result, len(emitter.events))
	}
}

func TestTaskReviewSuppressedBindingIDRequiresExactServerAuthoredShape(t *testing.T) {
	event := taskReviewJournalEvent(
		"issue.update", "driver-run:reviewer", `{"status":"in_progress"}`, `{"status":"review"}`,
		map[string]string{
			reviewProjectionMetadataKey:     reviewProjectionMetadataValue,
			reviewTriggerPolicyMetadataKey:  reviewTriggerPolicySuppressSuccessor,
			reviewTriggerSuppressBindingKey: " binding-review-origin ",
		},
	)
	if got := taskReviewSuppressedBindingID(event); got != "binding-review-origin" {
		t.Fatalf("suppressed binding = %q, want binding-review-origin", got)
	}
	for name, mutate := range map[string]func(*store.JournalEvent){
		"missing server binding": func(ev *store.JournalEvent) { delete(ev.Metadata, reviewTriggerSuppressBindingKey) },
		"wrong prior status":     func(ev *store.JournalEvent) { ev.Before = json.RawMessage(`{"status":"open"}`) },
		"wrong actor":            func(ev *store.JournalEvent) { ev.Actor = "user:alice" },
		"wrong policy": func(ev *store.JournalEvent) {
			ev.Metadata[reviewTriggerPolicyMetadataKey] = "all"
		},
		"legacy caller policy": func(ev *store.JournalEvent) {
			ev.Metadata[reviewTriggerPolicyMetadataKey] = "suppress_self"
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate := event
			candidate.Metadata = map[string]string{}
			for key, value := range event.Metadata {
				candidate.Metadata[key] = value
			}
			mutate(&candidate)
			if got := taskReviewSuppressedBindingID(candidate); got != "" {
				t.Fatalf("suppressed binding = %q, want empty", got)
			}
		})
	}
}

func TestIssueJournalBridgeEmitsEnrichedTaskReview(t *testing.T) {
	emitter := &taskReviewCaptureEmitter{}
	bridge := &IssueJournalBridge{
		Source:         emitter,
		EmitTaskReview: true,
		IssueLookup: func(_ context.Context, ws, taskID string) (TaskReadySnapshot, error) {
			if ws != "WS" || taskID != "TASK-1" {
				t.Fatalf("lookup = (%q, %q), want (WS, TASK-1)", ws, taskID)
			}
			return TaskReadySnapshot{
				TaskID: "TASK-1", Status: "review", HasDesign: true,
				Labels: []string{"docs", "priority"}, IssueType: "task",
				SourceRepo: "acme/widget",
			}, nil
		},
	}
	event := taskReviewJournalEvent(
		"issue.update", "user:alice", `{"status":"open"}`, `{"status":"review"}`, nil,
	)
	result := &IssueJournalSweepResult{}
	advanced, err := bridge.emitBatch(t.Context(), "WS", []store.JournalEvent{event}, result)
	if err != nil {
		t.Fatalf("emitBatch: %v", err)
	}
	if advanced != event.ID {
		t.Fatalf("advanced = %q, want %q", advanced, event.ID)
	}
	if result.TaskReviewEmitted != 1 || result.Skipped != 1 {
		t.Fatalf("result = %+v, want one task.review and one normal-lane skip", result)
	}
	if len(emitter.events) != 1 {
		t.Fatalf("emitted events = %d, want 1", len(emitter.events))
	}
	got := emitter.events[0]
	if got.EventID != IssueJournalEventIDPrefix+event.ID+taskReviewEventIDSuffix ||
		got.EventType != TaskReviewEventType ||
		got.Origin != automation.EventOriginSystem ||
		got.ActorRef != "user:alice" ||
		got.SubjectRef != IssueJournalSubjectRefPrefix+"TASK-1" {
		t.Fatalf("task.review event = %+v", got)
	}
	if got.SubjectAttrs["status"] != "review" || got.SubjectAttrs["repo"] != "acme/widget" {
		t.Fatalf("subject attrs = %v", got.SubjectAttrs)
	}
	var payload map[string]any
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["taskId"] != "TASK-1" || payload["status"] != "review" ||
		payload["previousStatus"] != "open" || payload["hasDesign"] != true ||
		payload["sourceRepo"] != "acme/widget" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestIssueJournalBridgeTaskReviewRepositoryAdmissionClosesBothRaces(t *testing.T) {
	event := taskReviewJournalEvent(
		"issue.update", "user:alice", `{"status":"open"}`, `{"status":"review"}`, nil,
	)
	t.Run("sole fallback removed before admission blocks and suppresses", func(t *testing.T) {
		emitter := &taskReviewCaptureEmitter{}
		admissions := 0
		bridge := &IssueJournalBridge{
			Source: emitter, EmitTaskReview: true,
			IssueLookup: func(context.Context, string, string) (TaskReadySnapshot, error) {
				return TaskReadySnapshot{
					TaskID: "TASK-1", Status: "review", IssueType: "task",
					SourceRepo: "", RepositoryRequired: false,
				}, nil
			},
			RepositoryRequiredBlocker: func(_ context.Context, ws, taskID string) (TaskReadyRepositoryRequiredResult, error) {
				admissions++
				if ws != "WS" || taskID != "TASK-1" {
					t.Fatalf("admission = (%q, %q)", ws, taskID)
				}
				return TaskReadyRepositoryRequiredResult{Blocked: true}, nil
			},
		}
		result := &IssueJournalSweepResult{}
		advanced, err := bridge.emitBatch(t.Context(), "WS", []store.JournalEvent{event}, result)
		if err != nil {
			t.Fatal(err)
		}
		if advanced != event.ID || admissions != 1 || result.TaskReviewEmitted != 0 || len(emitter.events) != 0 {
			t.Fatalf("advanced=%q admissions=%d result=%+v events=%d", advanced, admissions, result, len(emitter.events))
		}
	})

	t.Run("repository added before admission dispatches canonical review", func(t *testing.T) {
		emitter := &taskReviewCaptureEmitter{}
		bridge := &IssueJournalBridge{
			Source: emitter, EmitTaskReview: true,
			IssueLookup: func(context.Context, string, string) (TaskReadySnapshot, error) {
				return TaskReadySnapshot{
					TaskID: "TASK-1", Status: "review", IssueType: "task",
					SourceRepo: "", RepositoryRequired: true,
				}, nil
			},
			RepositoryRequiredBlocker: func(context.Context, string, string) (TaskReadyRepositoryRequiredResult, error) {
				return TaskReadyRepositoryRequiredResult{DispatchReady: &TaskReadySnapshot{
					TaskID: "TASK-1", Status: "review", IssueType: "task",
					SourceRepo: "acme/widget", HasDesign: true,
				}}, nil
			},
		}
		result := &IssueJournalSweepResult{}
		advanced, err := bridge.emitBatch(t.Context(), "WS", []store.JournalEvent{event}, result)
		if err != nil {
			t.Fatal(err)
		}
		if advanced != event.ID || result.TaskReviewEmitted != 1 || len(emitter.events) != 1 {
			t.Fatalf("advanced=%q result=%+v events=%d", advanced, result, len(emitter.events))
		}
		var payload map[string]any
		if err := json.Unmarshal(emitter.events[0].Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["status"] != "review" || payload["sourceRepo"] != "acme/widget" ||
			payload["repositoryRequired"] != false || emitter.events[0].SubjectAttrs["repo"] != "acme/widget" {
			t.Fatalf("canonical event=%+v payload=%#v", emitter.events[0], payload)
		}
	})
}

func TestIssueJournalBridgeTaskReviewRequiresLiveReviewAndLookup(t *testing.T) {
	event := taskReviewJournalEvent(
		"issue.update", "user:alice", `{"status":"open"}`, `{"status":"review"}`, nil,
	)
	tests := []struct {
		name   string
		lookup TaskReadyIssueLookup
	}{
		{name: "missing lookup"},
		{
			name: "live task moved on",
			lookup: func(context.Context, string, string) (TaskReadySnapshot, error) {
				return TaskReadySnapshot{TaskID: "TASK-1", Status: "closed", IssueType: "task"}, nil
			},
		},
		{
			name: "live epic",
			lookup: func(context.Context, string, string) (TaskReadySnapshot, error) {
				return TaskReadySnapshot{TaskID: "TASK-1", Status: "review", IssueType: "epic"}, nil
			},
		},
		{
			name: "deleted task",
			lookup: func(context.Context, string, string) (TaskReadySnapshot, error) {
				return TaskReadySnapshot{}, domain.ErrNotFound
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			emitter := &taskReviewCaptureEmitter{}
			bridge := &IssueJournalBridge{
				Source: emitter, EmitTaskReview: true, IssueLookup: tt.lookup,
			}
			result := &IssueJournalSweepResult{}
			advanced, err := bridge.emitBatch(t.Context(), "WS", []store.JournalEvent{event}, result)
			if err != nil {
				t.Fatalf("emitBatch: %v", err)
			}
			if advanced != event.ID || result.TaskReviewEmitted != 0 || len(emitter.events) != 0 {
				t.Fatalf("advanced=%q result=%+v events=%d", advanced, result, len(emitter.events))
			}
		})
	}
}

func TestIssueJournalBridgeTaskReviewLookupFailurePinsCursor(t *testing.T) {
	lookupErr := errors.New("lookup unavailable")
	emitter := &taskReviewCaptureEmitter{}
	bridge := &IssueJournalBridge{
		Source: emitter, EmitTaskReview: true,
		IssueLookup: func(context.Context, string, string) (TaskReadySnapshot, error) {
			return TaskReadySnapshot{}, lookupErr
		},
	}
	event := taskReviewJournalEvent(
		"issue.update", "user:alice", `{"status":"open"}`, `{"status":"review"}`, nil,
	)
	result := &IssueJournalSweepResult{}
	advanced, err := bridge.emitBatch(t.Context(), "WS", []store.JournalEvent{event}, result)
	if !errors.Is(err, lookupErr) {
		t.Fatalf("err = %v, want errors.Is lookupErr", err)
	}
	if advanced != "" || result.TaskReviewEmitted != 0 || len(emitter.events) != 0 {
		t.Fatalf("advanced=%q result=%+v events=%d", advanced, result, len(emitter.events))
	}
}

func TestIssueJournalBridgeTaskReviewNoListenerCountsAndAdvances(t *testing.T) {
	emitter := &taskReviewCaptureEmitter{err: domain.ErrNotFound}
	bridge := &IssueJournalBridge{
		Source: emitter, EmitTaskReview: true,
		IssueLookup: func(context.Context, string, string) (TaskReadySnapshot, error) {
			return TaskReadySnapshot{TaskID: "TASK-1", Status: "review", IssueType: "task"}, nil
		},
	}
	event := taskReviewJournalEvent(
		"issue.update", "user:alice", `{"status":"open"}`, `{"status":"review"}`, nil,
	)
	result := &IssueJournalSweepResult{}
	advanced, err := bridge.emitBatch(t.Context(), "WS", []store.JournalEvent{event}, result)
	if err != nil {
		t.Fatalf("emitBatch: %v", err)
	}
	if advanced != event.ID || result.TaskReviewEmitted != 1 || len(emitter.events) != 1 {
		t.Fatalf("advanced=%q result=%+v attempts=%d; want handled no-listener occurrence",
			advanced, result, len(emitter.events))
	}
}
