package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/modules/automation"

	"github.com/tysonthomas9/loomcli/internal/platform/persistence"
)

// TaskReviewEventType is the normalized internal event type emitted when a
// task enters Review. It routes on internal.task.review.
const TaskReviewEventType = "task.review"

// taskReviewEventIDSuffix keeps the review-transition occurrence independent
// from the same journal row's normal issue.update occurrence.
const taskReviewEventIDSuffix = "-review"

const (
	reviewIssueStatus                    = "review"
	reviewProjectionMetadataKey          = "projection_applied"
	reviewProjectionMetadataValue        = "atomic"
	reviewTriggerPolicyMetadataKey       = "review_trigger_policy"
	reviewTriggerPolicySuppressSuccessor = "suppress_successor"
	reviewTriggerSuppressBindingKey      = "review_trigger_suppress_binding_id"
)

// isTaskReviewEntry accepts only a proven issue.update transition into Review.
// Missing Before fails closed: an After-only snapshot cannot distinguish a
// transition from an unrelated update to a card already in Review.
//
// Authorized Review-origin handoffs are still accepted here. They must enter
// emitTaskReview so commit-time repository admission can repair a repo-less
// Review card before Automation emission is suppressed.
func isTaskReviewEntry(ev automation.JournalEvent) bool {
	if strings.ToLower(strings.TrimSpace(ev.Action)) != "issue.update" {
		return false
	}
	before := journalSnapshotStatus(ev.Before)
	if before == "" || before == reviewIssueStatus {
		return false
	}
	if journalSnapshotStatus(ev.After) != reviewIssueStatus {
		return false
	}
	return true
}

func taskReviewSuppressedBindingID(ev automation.JournalEvent) string {
	before := journalSnapshotStatus(ev.Before)
	if before != "in_progress" {
		return ""
	}
	actor := strings.ToLower(strings.TrimSpace(ev.Actor))
	if !strings.HasPrefix(actor, "driver-run:") {
		return ""
	}
	if ev.Metadata[reviewProjectionMetadataKey] != reviewProjectionMetadataValue ||
		ev.Metadata[reviewTriggerPolicyMetadataKey] != reviewTriggerPolicySuppressSuccessor {
		return ""
	}
	return strings.TrimSpace(ev.Metadata[reviewTriggerSuppressBindingKey])
}

// emitTaskReview emits one task.review occurrence. A missing listener is a
// durable no-op, matching the normal issue and task.ready lanes; other errors
// pin the cursor so this exact journal row is retried.
func (b *IssueJournalBridge) emitTaskReview(ctx context.Context, ws string, ev automation.JournalEvent) (bool, error) {
	event, dispatch, snapshot, err := b.toTaskReviewEvent(ctx, ws, ev)
	if err != nil {
		return false, err
	}
	if !dispatch {
		b.logger().Debug("issue journal bridge: suppressing task.review event for ineligible or missing task",
			"workspace", ws, "event_id", IssueJournalEventIDPrefix+ev.ID+taskReviewEventIDSuffix, "task_id", ev.EntityID)
		return false, nil
	}
	// Review uses the same Work Items-owned commit-time repository admission as
	// Ready. A repo-less Review snapshot can race either creation of a valid
	// fallback repository or deletion of its sole fallback after lookup. The
	// atomic command returns the canonical Review projection or blocks/suppresses
	// this generation; a stale pre-read never becomes dispatch authority.
	if snapshot != nil && taskReadyNeedsRepositoryAdmission(*snapshot) {
		if b.RepositoryRequiredBlocker == nil {
			return false, fmt.Errorf("task.review requires repository admission: %w", persistence.ErrInvalid)
		}
		admission, blockErr := b.RepositoryRequiredBlocker(ctx, ws, snapshot.TaskID)
		if blockErr != nil {
			return false, fmt.Errorf("admit repository for review task %q in workspace %q: %w", snapshot.TaskID, ws, blockErr)
		}
		if admission.DispatchReady == nil {
			return false, nil
		}
		canonical := normalizeTaskReadySnapshot(*admission.DispatchReady)
		canonical.RepositoryRequired = false
		if canonical.Status != reviewIssueStatus || strings.EqualFold(canonical.IssueType, "epic") {
			return false, nil
		}
		event, err = rebuildTaskReviewEvent(event, canonical, journalSnapshotStatus(ev.Before))
		if err != nil {
			return false, err
		}
	}
	// A Review-origin handoff is a successor of the original task.review
	// occurrence, whose reservation already fanned out every matching Review
	// binding. Re-emitting that restoration would create fresh system-root
	// events and let multiple Review bindings ping-pong forever. Fleet stamps
	// the exact server-derived originating binding as unmintable proof. Suppress
	// only after repository admission has either blocked the card or returned a
	// canonical dispatch-ready Review projection.
	if taskReviewSuppressedBindingID(ev) != "" {
		return false, nil
	}

	_, err = b.Source.Emit(ctx, ws, event)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, persistence.ErrNotFound):
		b.logger().Debug("issue journal bridge: no binding for task.review event, advancing past it",
			"workspace", ws, "event_id", event.EventID, "task_id", ev.EntityID)
		return true, nil
	default:
		return false, fmt.Errorf("emit task.review event %q in workspace %q: %w", event.EventID, ws, err)
	}
}

// toTaskReviewEvent enriches a proven journal transition with the same live
// issue projection used by task.ready. Both the journal After and the live
// issue must say review at dispatch time. The live lookup also excludes epics
// and supplies the role-gating/repository fields needed by prompt-agent.
func (b *IssueJournalBridge) toTaskReviewEvent(
	ctx context.Context,
	ws string,
	ev automation.JournalEvent,
) (InternalEvent, bool, *TaskReadySnapshot, error) {
	if b.IssueLookup == nil {
		return InternalEvent{}, false, nil, fmt.Errorf("task.review requires current issue lookup: %w", persistence.ErrInvalid)
	}
	snapshot, err := b.IssueLookup(ctx, ws, ev.EntityID)
	if err != nil {
		if errors.Is(err, persistence.ErrNotFound) {
			return InternalEvent{}, false, nil, nil
		}
		return InternalEvent{}, false, nil, fmt.Errorf(
			"look up current task %q for task.review in workspace %q: %w", ev.EntityID, ws, err,
		)
	}
	snapshot = normalizeTaskReadySnapshot(snapshot)
	if snapshot.Status != reviewIssueStatus || strings.EqualFold(snapshot.IssueType, "epic") {
		return InternalEvent{}, false, &snapshot, nil
	}
	// The journal entity is the dispatch authority. Keep enrichment from the
	// lookup, but never let a malformed adapter response redirect the payload.
	snapshot.TaskID = strings.TrimSpace(ev.EntityID)
	payload, err := taskReviewSnapshotPayload(snapshot, journalSnapshotStatus(ev.Before))
	if err != nil {
		return InternalEvent{}, false, nil, fmt.Errorf(
			"marshal task.review event %q in workspace %q: %w", ev.ID, ws, err,
		)
	}
	attrs := map[string]string{"status": reviewIssueStatus}
	if snapshot.SourceRepo != "" {
		attrs["repo"] = snapshot.SourceRepo
	}
	return InternalEvent{
		EventID:      IssueJournalEventIDPrefix + ev.ID + taskReviewEventIDSuffix,
		EventType:    TaskReviewEventType,
		Origin:       automation.EventOriginSystem,
		ActorRef:     ev.Actor,
		SubjectRef:   IssueJournalSubjectRefPrefix + ev.EntityID,
		Payload:      payload,
		SubjectAttrs: attrs,
	}, true, &snapshot, nil
}

func rebuildTaskReviewEvent(event InternalEvent, snapshot TaskReadySnapshot, previousStatus string) (InternalEvent, error) {
	payload, err := taskReviewSnapshotPayload(snapshot, previousStatus)
	if err != nil {
		return InternalEvent{}, fmt.Errorf("marshal canonical task.review event %q: %w", event.EventID, err)
	}
	event.Payload = payload
	event.SubjectAttrs = map[string]string{"status": reviewIssueStatus}
	if snapshot.SourceRepo != "" {
		event.SubjectAttrs["repo"] = snapshot.SourceRepo
	}
	return event, nil
}

func taskReviewSnapshotPayload(snapshot TaskReadySnapshot, previousStatus string) (json.RawMessage, error) {
	snapshot = normalizeTaskReadySnapshot(snapshot)
	return json.Marshal(map[string]any{
		"taskId":             snapshot.TaskID,
		"status":             snapshot.Status,
		"previousStatus":     previousStatus,
		"hasDesign":          snapshot.HasDesign,
		"labels":             snapshot.Labels,
		"issueType":          snapshot.IssueType,
		"sourceRepo":         snapshot.SourceRepo,
		"repositoryRequired": snapshot.RepositoryRequired,
	})
}
