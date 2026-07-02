package trigger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
)

// Task-ready internal events (Phase 4 prompt-agent packaging, flag-gated).
//
// WHAT THE JOURNAL RECORDS. fleet-db does not journal a dedicated "ready"
// action; its issue lifecycle is issue.create, issue.update and issue.close
// (fleet-db internal/models/event.go). A task is READY when it is open,
// unblocked and non-deferred — a Redis ready-queue state maintained by status
// transitions, not a distinct journal action. The faithful loopback signal for
// "a task became ready" is therefore an entry whose After snapshot shows the
// task in the OPEN status: issue.create of a task that is open on creation, and
// issue.update transitioning a task to open (e.g. an unblock/reopen). We match
// on the snapshot status only — full unblocked/non-deferred eligibility is not
// determinable from a single journal snapshot, so a rare deferred/still-blocked
// open task may emit a task.ready the claim then declines (claim-by-id returns
// 409 → the prompt-agent treats it as unclaimable). That is honest and safe: a
// spurious ready never runs the wrong task, it just no-ops the claim.
//
// COMPOSITION WITH THE RUN INPUT. The task.ready event carries the task id in
// its payload EXPLICITLY (the journal After snapshot may not surface the id at
// top level, and the dispatch subject_ref does not reach the run payload). The
// InternalSource wraps the emitter payload in its provenance envelope, so the
// fired run's input is {origin, hopDepth, event:{taskId, status}}; the
// prompt-agent reads the task id from input.event.taskId (and input.taskId for
// the flat/cron shape). This is where ITEM D's payload path and the event
// payload meet.

// TaskReadyEventType is the normalized internal event type emitted when a task
// becomes ready. Its final segment ("ready") is not an action verb in
// NormalizeInternalEventType's table, so it passes through unchanged and routes
// on "internal.task.ready".
const TaskReadyEventType = "task.ready"

// taskReadyEventIDSuffix distinguishes the task-ready loopback event id from the
// same journal entry's normal issue.* re-emission, so both dispatch (and each
// dedups independently on its own deterministic id across replays).
const taskReadyEventIDSuffix = "-ready"

// readyIssueStatus is the issue status that marks a task ready-eligible in the
// After snapshot. fleet-db's ready queue admits open, unblocked, non-deferred
// issues; open is the snapshot-observable part.
const readyIssueStatus = "open"

// taskReadyJournalActions are the journal actions that can mark a task becoming
// ready: creation (ready on creation) and update (unblock/reopen to open).
var taskReadyJournalActions = map[string]bool{
	"issue.create": true,
	"issue.update": true,
}

// isTaskReadyEntry reports whether a journal entry marks a task entering the
// ready-eligible (open) state: a create/update whose After snapshot status is
// open. Close/block/claim transitions (status != open) are not ready.
func isTaskReadyEntry(ev store.JournalEvent) bool {
	if !taskReadyJournalActions[strings.ToLower(strings.TrimSpace(ev.Action))] {
		return false
	}
	return journalSnapshotStatus(ev.After) == readyIssueStatus
}

// journalSnapshotStatus extracts the lowercased "status" scalar from a journal
// After snapshot; absent/non-object/non-scalar yields "".
func journalSnapshotStatus(after json.RawMessage) string {
	if len(after) == 0 {
		return ""
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(after, &fields); err != nil {
		return ""
	}
	raw, ok := fields["status"]
	if !ok {
		return ""
	}
	if v, ok := scalarString(raw); ok {
		return strings.ToLower(strings.TrimSpace(v))
	}
	return ""
}

// emitTaskReady re-enters one entry into the router as a system-origin
// task.ready event. A no-listener dispatch (domain.ErrNotFound — no binding on
// internal.task.ready) is NOT a failure: the cursor still advances so a missing
// binding never stalls the bridge, mirroring emitOne. Any other Emit error is
// returned so the drain stops and the entry is retried.
func (b *IssueJournalBridge) emitTaskReady(ctx context.Context, ws string, ev store.JournalEvent) error {
	_, err := b.Source.Emit(ctx, ws, b.toTaskReadyEvent(ev))
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrNotFound):
		b.logger().Debug("issue journal bridge: no binding for task.ready event, advancing past it",
			"workspace", ws, "event_id", IssueJournalEventIDPrefix+ev.ID+taskReadyEventIDSuffix, "task_id", ev.EntityID)
		return nil
	default:
		return fmt.Errorf("emit task.ready event %q in workspace %q: %w", ev.ID, ws, err)
	}
}

// toTaskReadyEvent maps a ready-marking journal entry to the loopback
// task.ready InternalEvent. EventID is deterministic (fleet-journal-{id}-ready)
// so replay dedups; Origin is system (a depth-0 root); ActorRef is the journal
// actor verbatim; SubjectRef is issue:{entityID}; Payload carries the task id
// explicitly so the fired run can claim by id.
func (b *IssueJournalBridge) toTaskReadyEvent(ev store.JournalEvent) InternalEvent {
	payload, _ := json.Marshal(map[string]string{
		"taskId": ev.EntityID,
		"status": journalSnapshotStatus(ev.After),
	})
	return InternalEvent{
		EventID:      IssueJournalEventIDPrefix + ev.ID + taskReadyEventIDSuffix,
		EventType:    TaskReadyEventType,
		Origin:       domain.TriggerEventOriginSystem,
		ActorRef:     ev.Actor,
		SubjectRef:   IssueJournalSubjectRefPrefix + ev.EntityID,
		Payload:      payload,
		SubjectAttrs: issueSubjectAttrs(ev.After),
	}
}
