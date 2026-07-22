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
// ready: creation, update (including unblock), explicit reopen/undefer, and the
// typed Work Item release emitted by terminal-parent recovery. Recovery can run
// after the startup Ready snapshot, so omitting issue.release would strand the
// newly reopened card until the next serve restart.
var taskReadyJournalActions = map[string]bool{
	"issue.create":  true,
	"issue.release": true,
	"issue.reopen":  true,
	"issue.undefer": true,
	"issue.update":  true,
}

// isTaskReadyEntry reports whether a journal entry marks a task entering the
// ready-eligible (open) state: one of the actions above whose After snapshot is
// open. Close/block/claim/assign transitions are not ready-entry actions.
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
// returned so the drain stops and the entry is retried. The boolean reports
// whether the event reached Source.Emit; live epics are deliberately suppressed
// before that boundary because prompt-agent bindings must never claim epics.
func (b *IssueJournalBridge) emitTaskReady(ctx context.Context, ws string, ev store.JournalEvent) (bool, bool, error) {
	event, dispatch, snapshot, err := b.toTaskReadyEvent(ctx, ws, ev)
	if err != nil {
		return false, false, err
	}
	if !dispatch {
		b.logger().Debug("issue journal bridge: suppressing task.ready event for ineligible or missing task",
			"workspace", ws, "event_id", IssueJournalEventIDPrefix+ev.ID+taskReadyEventIDSuffix, "task_id", ev.EntityID)
		return false, false, nil
	}
	// Every non-epic task without an explicit repository crosses the Work
	// Items owner's commit-time admission command. RepositoryRequired is only a
	// pre-read hint: a snapshot that observed the single-repository fallback can
	// race deletion of that sole Repo before dispatch. The command serializes
	// its repository-count decision with repository create/delete and either
	// returns the canonical dispatchable task or blocks it.
	if snapshot != nil && taskReadyNeedsRepositoryAdmission(*snapshot) && b.RepositoryRequiredBlocker != nil {
		admission, blockErr := b.RepositoryRequiredBlocker(ctx, ws, snapshot.TaskID)
		if blockErr != nil {
			return false, false, fmt.Errorf("block repository-required task %q in workspace %q: %w", snapshot.TaskID, ws, blockErr)
		}
		if admission.DispatchReady == nil {
			return false, admission.Blocked, nil
		}
		// The original ready snapshot raced a repository assignment or a
		// workspace repository-count change. Rebuild the event from the atomic
		// command's canonical projection: the stale payload may have an empty repo
		// and the count change has no later issue.update event to trigger dispatch.
		canonical := normalizeTaskReadySnapshot(*admission.DispatchReady)
		canonical.RepositoryRequired = false
		if canonical.Status != readyIssueStatus || strings.EqualFold(canonical.IssueType, "epic") {
			return false, admission.Blocked, nil
		}
		event, err = rebuildTaskReadyEvent(event, canonical)
		if err != nil {
			return false, false, err
		}
		snapshot = &canonical
	}
	_, err = b.Source.Emit(ctx, ws, event)
	switch {
	case err == nil:
		if snapshot != nil {
			b.rememberTaskReadyGeneration(ws, *snapshot)
		}
		return true, false, nil
	case errors.Is(err, domain.ErrNotFound):
		if snapshot != nil {
			b.rememberTaskReadyGeneration(ws, *snapshot)
		}
		b.logger().Debug("issue journal bridge: no binding for task.ready event, advancing past it",
			"workspace", ws, "event_id", IssueJournalEventIDPrefix+ev.ID+taskReadyEventIDSuffix, "task_id", ev.EntityID)
		return true, false, nil
	default:
		return false, false, fmt.Errorf("emit task.ready event %q in workspace %q: %w", ev.ID, ws, err)
	}
}

func taskReadyNeedsRepositoryAdmission(snapshot TaskReadySnapshot) bool {
	snapshot = normalizeTaskReadySnapshot(snapshot)
	return !strings.EqualFold(snapshot.IssueType, "epic") && snapshot.SourceRepo == ""
}

// toTaskReadyEvent maps a ready-marking journal entry to the loopback
// task.ready InternalEvent. EventID is deterministic (fleet-journal-{id}-ready)
// so replay dedups; Origin is system (a depth-0 root); ActorRef is the journal
// actor verbatim; SubjectRef is issue:{entityID}; Payload carries the task id
// explicitly so the fired run can claim by id, plus the role-gating hints below.
func (b *IssueJournalBridge) toTaskReadyEvent(ctx context.Context, ws string, ev store.JournalEvent) (InternalEvent, bool, *TaskReadySnapshot, error) {
	payloadFields, snapshot, err := b.taskReadyPayload(ctx, ws, ev)
	if err != nil {
		// A deleted issue can still have an older open journal entry waiting
		// behind the bridge cursor. That entry is durably stale: suppress it so
		// one tombstoned task cannot poison the workspace cursor forever. Other
		// lookup failures remain retryable and continue to pin the entry.
		if errors.Is(err, domain.ErrNotFound) {
			return InternalEvent{}, false, nil, nil
		}
		return InternalEvent{}, false, nil, err
	}
	// taskReadyPayload is sourced from the configured live IssueLookup when one
	// is available, so this check covers both create and update journal entries
	// without trusting a potentially stale/partial After snapshot. Alternate
	// hosts without a lookup keep the compatibility path: a known epic is still
	// suppressed, while an update delta with no type remains eligible because its
	// type is unknowable rather than falsely assumed.
	if issueType, _ := payloadFields["issueType"].(string); strings.EqualFold(strings.TrimSpace(issueType), "epic") {
		return InternalEvent{}, false, snapshot, nil
	}
	// A journal After snapshot can say open long after the live task was
	// claimed, closed, or blocked. Once a live lookup is configured, only its
	// canonical open status may cross the dispatch boundary.
	if snapshot != nil && snapshot.Status != readyIssueStatus {
		return InternalEvent{}, false, snapshot, nil
	}
	payload, err := json.Marshal(payloadFields)
	if err != nil {
		return InternalEvent{}, false, nil, fmt.Errorf("marshal task.ready event %q in workspace %q: %w", ev.ID, ws, err)
	}
	return InternalEvent{
		EventID:      IssueJournalEventIDPrefix + ev.ID + taskReadyEventIDSuffix,
		EventType:    TaskReadyEventType,
		Origin:       domain.TriggerEventOriginSystem,
		ActorRef:     ev.Actor,
		SubjectRef:   IssueJournalSubjectRefPrefix + ev.EntityID,
		Payload:      payload,
		SubjectAttrs: issueSubjectAttrs(ev.After),
	}, true, snapshot, nil
}

// taskReadyPayload assembles the task.ready emitter payload. Beyond the task id
// (the claim target) and ready status it carries the role-gating hints the
// prompt-agent claim lane compares against a worn role's task filter BEFORE
// spending any backend tokens — hasDesign, labels and issueType — so a planner
// binding (needs no design) and a coder binding (needs a design) can each
// decline a mismatched task without a claim-and-release round trip.
//
// SNAPSHOT SEMANTICS (the 2026-07-07 approve bug): an issue.create entry's
// After is the FULL card, so an absent key is a genuine zero value (fleet-db
// omits an empty design). An issue.update entry's After is a DELTA — only the
// changed fields — so an absent key means UNKNOWN, not false. For deltas the
// gating hints are enriched from IssueLookup (the live card). When a configured
// lookup fails, emission fails and is retried: silently dropping
// repositoryRequired would let a repo-less multi-repo task cross the pre-claim
// guard. When no lookup capability is configured, unknowable keys are omitted
// for compatibility. Pinned contract with
// the claim gate: { taskId, status, hasDesign?, labels?, issueType?,
// sourceRepo?, repositoryRequired? } — an absent gating key means unknown,
// never false/empty. RepositoryRequired is a workspace-level fact, so even a
// full create snapshot uses the live lookup when available rather than guessing
// from an empty sourceRepo (which is valid in a single-repo workspace).
func (b *IssueJournalBridge) taskReadyPayload(ctx context.Context, ws string, ev store.JournalEvent) (map[string]any, *TaskReadySnapshot, error) {
	fields := snapshotFields(ev.After)
	payload := map[string]any{
		"taskId": ev.EntityID,
		"status": journalSnapshotStatus(ev.After),
	}
	_, designKnown := fields["design"]
	_, labelsKnown := fields["labels"]
	_, typeKnown := fields["type"]
	repoValue, repoKnown := snapshotSourceRepo(fields)
	fullSnapshot := ev.Action == "issue.create"
	if b.IssueLookup != nil {
		issue, err := b.IssueLookup(ctx, ws, ev.EntityID)
		if err != nil {
			return nil, nil, fmt.Errorf("look up current task %q for task.ready in workspace %q: %w", ev.EntityID, ws, err)
		}
		issue = normalizeTaskReadySnapshot(issue)
		payload["status"] = issue.Status
		payload["hasDesign"] = issue.HasDesign
		payload["labels"] = normalizeLabelSlice(issue.Labels)
		payload["issueType"] = issue.IssueType
		payload["sourceRepo"] = strings.TrimSpace(issue.SourceRepo)
		payload["repositoryRequired"] = issue.RepositoryRequired
		return payload, &issue, nil
	}
	if fullSnapshot || designKnown {
		payload["hasDesign"] = snapshotFieldNonEmpty(fields, "design")
	}
	if fullSnapshot || labelsKnown {
		payload["labels"] = snapshotStringSlice(fields, "labels")
	}
	if fullSnapshot || typeKnown {
		payload["issueType"] = snapshotFieldScalar(fields, "type")
	}
	if fullSnapshot || repoKnown {
		payload["sourceRepo"] = repoValue
	}
	return payload, nil, nil
}

// rebuildTaskReadyEvent replaces every readiness-sensitive field on an event
// with a canonical live snapshot while retaining the original journal-derived
// identity and actor. It is used only after the Work Items owner returns a
// commit-time DispatchReady result.
func rebuildTaskReadyEvent(event InternalEvent, snapshot TaskReadySnapshot) (InternalEvent, error) {
	payload, err := taskReadySnapshotPayload(snapshot)
	if err != nil {
		return InternalEvent{}, fmt.Errorf("marshal canonical task.ready payload for %q: %w", snapshot.TaskID, err)
	}
	event.Payload = payload
	event.SubjectRef = IssueJournalSubjectRefPrefix + snapshot.TaskID
	event.SubjectAttrs = map[string]string{"status": snapshot.Status}
	if snapshot.SourceRepo != "" {
		event.SubjectAttrs["repo"] = snapshot.SourceRepo
	}
	return event, nil
}

// snapshotSourceRepo resolves fleet-db's canonical repo key and the legacy
// source_repo alias. The boolean distinguishes a known empty repo from an
// absent field in an update delta.
func snapshotSourceRepo(fields map[string]json.RawMessage) (string, bool) {
	for _, key := range []string{"repo", "source_repo"} {
		raw, ok := fields[key]
		if !ok {
			continue
		}
		value, scalar := scalarString(raw)
		if !scalar {
			return "", true
		}
		return strings.TrimSpace(value), true
	}
	return "", false
}

// normalizeLabelSlice mirrors snapshotStringSlice's stable-[] guarantee for
// lookup-sourced labels: never nil, blanks dropped.
func normalizeLabelSlice(labels []string) []string {
	out := []string{}
	for _, l := range labels {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// snapshotFields decodes a journal After snapshot into its top-level field map;
// a nil/empty/non-object snapshot yields nil, which every accessor below treats
// as "no fields present".
func snapshotFields(after json.RawMessage) map[string]json.RawMessage {
	if len(after) == 0 {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(after, &fields); err != nil {
		return nil
	}
	return fields
}

// snapshotFieldScalar renders one scalar snapshot field to its string form,
// yielding "" when the key is absent or its value is not a scalar.
func snapshotFieldScalar(fields map[string]json.RawMessage, key string) string {
	if raw, ok := fields[key]; ok {
		if v, ok := scalarString(raw); ok {
			return v
		}
	}
	return ""
}

// snapshotFieldNonEmpty reports whether a snapshot field is a non-empty scalar
// string. Used for hasDesign: a task carries a design when its design body is
// set (fleet-db omits an empty design from the snapshot).
func snapshotFieldNonEmpty(fields map[string]json.RawMessage, key string) bool {
	return snapshotFieldScalar(fields, key) != ""
}

// snapshotStringSlice decodes a snapshot field as a JSON string array, dropping
// non-string/blank elements. It always returns a non-nil slice so the wire field
// is a stable [] (never null) when the snapshot carries no labels.
func snapshotStringSlice(fields map[string]json.RawMessage, key string) []string {
	out := []string{}
	raw, ok := fields[key]
	if !ok {
		return out
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return out
	}
	for _, item := range arr {
		if v, ok := scalarString(item); ok {
			out = append(out, v)
		}
	}
	return out
}
