package driver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/tysonthomas9/loomcli/internal/domain"
	"github.com/tysonthomas9/loomcli/internal/store"
	"github.com/tysonthomas9/loomcli/internal/trigger"
)

// run.finished lifecycle emission (ARCHITECTURE-PROPOSAL §7 step 8, chunk
// AW6). loom serve is the publisher: every server-side DriverRun terminal
// transition — finish (completed/failed/needs_review/cancelled) and
// stale-sweep recovery — emits one run.finished event so composition awaits
// (pattern "run.finished:{childRunId}", AW10) have a matchable,
// journal-backed record.
//
// The emission is best-effort-but-journaled-first:
//
//  1. JOURNAL: the event is appended directly to the trigger-event journal
//     (store.TriggerEventAppender) that the await registration scan reads
//     (RULE 2). A child that finishes before its parent registers the await
//     is found by that scan — the "already-terminal child resolves
//     immediately" guarantee — and the append is UNCONDITIONAL: neither
//     binding configuration nor the loop guard can suppress composition.
//     The dispatch-time await matcher (AW7) hooks in right after this append.
//  2. LOOPBACK: the event then feeds the C14 internal loopback (route key
//     "internal.run.finished") for binding fan-out. Bindings opt in
//     explicitly via the internal.* namespace, and the structural guard
//     applies: origin=system (server-originated lifecycle, never
//     workflow-forged) with C19 hop-depth stamping — a run admitted by a
//     trigger event emits run.finished at the admitting event's depth + 1,
//     capped, so internal.run.finished bindings cannot recursively amplify.
//
// Both steps are best-effort: failures are logged and never fail the
// transition (the watch reconciliation + stale sweeps converge state; a
// re-finish replays the deterministic event ID idempotently).

// RunFinishedEventType is the lifecycle event type terminal DriverRun
// transitions emit. Already normalized (NormalizeInternalEventType is a
// no-op on it), so the journaled type and the loopback route suffix match.
const RunFinishedEventType = "run.finished"

// runFinishedActor is the ActorRef stamped on run.finished events: the
// server itself. Composition awaits use no actor predicate (AW10's
// actor=system carve-out).
const runFinishedActor = "system"

// runFinishedSourceKind marks journal records produced by the lifecycle
// lane rather than an ingress connector.
const runFinishedSourceKind = "internal"

// RunFinishedEventID is the deterministic event ID for a terminal
// transition: "run-finished:{runID}:{status}". Deterministic so a re-run of
// the finish path (double-finish, sweep retry) re-emits idempotently — the
// journal append and the loopback idempotency key both dedup on it.
func RunFinishedEventID(runID string, status domain.DriverRunStatus) string {
	return "run-finished:" + runID + ":" + string(status)
}

// RunFinishedSubjectKey renders the await-matchable subject key for a run's
// terminal event — the exact pattern composition awaits register
// (domain.AwaitEventKey over the run.finished type and the run ID).
func RunFinishedSubjectKey(runID string) string {
	return domain.AwaitEventKey(RunFinishedEventType, runID)
}

// runFinishedPayload is the camelCase driver-wire payload of a run.finished
// event: enough for a resumed parent to branch on the child's outcome
// without a second fetch.
type runFinishedPayload struct {
	RunID       string `json:"runId"`
	Status      string `json:"status"`
	Summary     string `json:"summary,omitempty"`
	ErrorClass  string `json:"errorClass,omitempty"`
	ParentRunID string `json:"parentRunId,omitempty"`
}

// emitRunFinishedEvent publishes one terminal transition: journal-first
// append, then internal-loopback dispatch. Nil-safe and best-effort; a
// non-terminal (or suspended) run is ignored. src may be nil — a zero-config
// loopback over the same store is used; passing the serve-shared
// InternalSource keeps the hop-depth ledger warm across emissions.
func emitRunFinishedEvent(ctx context.Context, s store.Store, src *trigger.InternalSource, run *domain.DriverRun) {
	if s == nil || run == nil || !run.Status.IsTerminal() {
		return
	}
	if src == nil {
		src = &trigger.InternalSource{Store: s}
	}
	eventID := RunFinishedEventID(run.RunID, run.Status)
	parentEventID, hopDepth := runFinishedProvenance(ctx, s, src, run)
	payload := marshalRunFinishedPayload(ctx, run)
	appendRunFinishedJournal(ctx, s, run, eventID, hopDepth)
	dispatchRunFinishedAwaits(ctx, s, run, eventID, payload)
	emitRunFinishedLoopback(ctx, src, run, eventID, parentEventID, payload)
}

// dispatchRunFinishedAwaits runs the dispatch-time await matcher (AW7)
// directly off the journaled lifecycle event, BEFORE the loopback:
// composition awaits (pattern "run.finished:{runID}") resolve even when no
// internal.* binding listens or the hop-depth guard suppresses binding
// fan-out — matching the unconditional journal append above. When the
// loopback does dispatch, its own matcher pass replays as an idempotent
// no-op (the await is already terminal). Best-effort like every leg here.
func dispatchRunFinishedAwaits(ctx context.Context, s store.Store, run *domain.DriverRun, eventID string, payload json.RawMessage) {
	matcher := &trigger.AwaitMatcher{Store: s}
	if _, err := matcher.Dispatch(ctx, run.WorkspaceKey, trigger.AwaitDispatchEvent{
		EventID:    eventID,
		EventType:  RunFinishedEventType,
		SubjectRef: run.RunID,
		ActorRef:   runFinishedActor,
		Payload:    payload,
	}); err != nil {
		slog.WarnContext(ctx, "run.finished await dispatch failed",
			"runID", run.RunID, "status", string(run.Status), "error", err)
	}
}

// marshalRunFinishedPayload encodes the resume/fan-out payload; nil (with a
// log record) on the never-expected marshal failure so the lifecycle event
// still propagates without it.
func marshalRunFinishedPayload(ctx context.Context, run *domain.DriverRun) json.RawMessage {
	payload, err := json.Marshal(runFinishedPayload{
		RunID:       run.RunID,
		Status:      string(run.Status),
		Summary:     run.Summary,
		ErrorClass:  run.ErrorClass,
		ParentRunID: run.ParentRunID,
	})
	if err != nil {
		slog.WarnContext(ctx, "encode run.finished payload failed", "runID", run.RunID, "error", err)
		return nil
	}
	return payload
}

// runFinishedProvenance derives the C19 chain provenance for a run's
// terminal event. A run admitted by the trigger dispatch path carries the
// admitting event's ID in SourceRef; when that resolves to a persisted
// trigger event the run.finished continues its chain at depth parent+1.
// Anything else (CLI runs, epic runs, free-form source refs) is a depth-0
// system root.
func runFinishedProvenance(ctx context.Context, s store.Store, src *trigger.InternalSource, run *domain.DriverRun) (string, int) {
	parentEventID := strings.TrimSpace(run.SourceRef)
	if parentEventID == "" {
		return "", 0
	}
	if _, err := s.TriggerEvents().Get(ctx, run.WorkspaceKey, parentEventID); err != nil {
		return "", 0
	}
	return parentEventID, src.ChainHopDepth(ctx, run.WorkspaceKey, parentEventID) + 1
}

// appendRunFinishedJournal writes the journal record the await registration
// scan matches (subject key RunFinishedSubjectKey). Unconditional with
// respect to the loop guard: the stamped HopDepth may exceed the cap — the
// cap suppresses binding fan-out, never await visibility. Backends without
// the appender capability (fleet-db client) journal server-side in their
// dispatch wiring instead (IndexAwaitEvent, AW2/AW7).
func appendRunFinishedJournal(ctx context.Context, s store.Store, run *domain.DriverRun, eventID string, hopDepth int) {
	appender, ok := s.TriggerEvents().(store.TriggerEventAppender)
	if !ok {
		slog.DebugContext(ctx, "run.finished journal append skipped: backend journals server-side",
			"runID", run.RunID)
		return
	}
	now := time.Now().UTC()
	occurredAt := now
	if run.FinishedAt != nil && !run.FinishedAt.IsZero() {
		occurredAt = run.FinishedAt.UTC()
	}
	_, err := appender.AppendTriggerEvent(ctx, &domain.TriggerEvent{
		WorkspaceKey:    run.WorkspaceKey,
		EventID:         eventID,
		SourceKind:      runFinishedSourceKind,
		SourceEventID:   eventID,
		EventType:       RunFinishedEventType,
		SubjectRef:      run.RunID,
		ActorRef:        runFinishedActor,
		Origin:          domain.TriggerEventOriginSystem,
		HopDepth:        hopDepth,
		OccurredAt:      occurredAt,
		ReceivedAt:      now,
		IdempotencyKey:  trigger.InternalEventIdempotencyKey(run.WorkspaceKey, eventID),
		SignatureStatus: "internal",
	})
	if err != nil {
		slog.WarnContext(ctx, "append run.finished journal event failed",
			"runID", run.RunID, "status", string(run.Status), "error", err)
	}
}

// emitRunFinishedLoopback feeds the terminal event into the C14 loopback for
// binding fan-out. "Nobody listening" (domain.ErrNotFound) is the normal
// case and logged at debug; a guard drop was already audited by the source.
func emitRunFinishedLoopback(ctx context.Context, src *trigger.InternalSource, run *domain.DriverRun, eventID, parentEventID string, payload json.RawMessage) {
	_, err := src.Emit(ctx, run.WorkspaceKey, trigger.InternalEvent{
		EventID:       eventID,
		EventType:     RunFinishedEventType,
		Origin:        domain.TriggerEventOriginSystem,
		ParentEventID: parentEventID,
		SubjectRef:    run.RunID,
		ActorRef:      runFinishedActor,
		EpicID:        run.EpicID,
		Payload:       payload,
	})
	switch {
	case err == nil:
	case errors.Is(err, domain.ErrNotFound):
		slog.DebugContext(ctx, "run.finished loopback: no internal binding listening",
			"runID", run.RunID, "status", string(run.Status))
	default:
		slog.WarnContext(ctx, "run.finished loopback dispatch failed",
			"runID", run.RunID, "status", string(run.Status), "error", err)
	}
}
