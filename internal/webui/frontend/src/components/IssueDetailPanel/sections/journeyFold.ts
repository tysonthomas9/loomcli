import type { Event } from "@/types";

import {
  actorKindFor,
  describeEvent,
  type JourneyActorKind,
} from "./eventDescription";
import {
  journeyStageForStatus,
  type JourneyStage,
} from "./journeyPresentation";

export type { JourneyStage } from "./journeyPresentation";

export interface JourneyAuditEvent {
  id: string;
  at: string;
  actor: string | null;
  actorKind: JourneyActorKind;
  text: string;
}

export interface JourneyHalt {
  start: string;
  end: string | null;
  durationMs: number;
  note: string | null;
  clearedNote: string | null;
  events: JourneyAuditEvent[];
  startFraction: number;
  endFraction: number;
}

export interface JourneySpan {
  stage: JourneyStage;
  owner: string | null;
  start: string;
  end: string | null;
  durationMs: number;
  haltedMs: number;
  halts: JourneyHalt[];
  events: JourneyAuditEvent[];
}

interface MutableHalt extends JourneyHalt {
  startMs: number;
  endMs: number | null;
}

interface MutableSpan extends Omit<JourneySpan, "halts"> {
  startMs: number;
  endMs: number | null;
  halts: MutableHalt[];
}

interface OrderedEvent {
  event: Event;
  index: number;
  atMs: number;
}

function ownerName(value: string | null | undefined): string | null {
  const trimmed = value?.trim();
  return trimmed ? trimmed : null;
}

function statusAfter(event: Event): string | null {
  const change = event.changes?.find(
    ({ field }) => field.trim().toLowerCase() === "status",
  );
  if (change) return change.after ?? null;

  // Older Loom event producers used old_value/new_value for status changes.
  if (event.event_type === "issue.status_changed") {
    return event.new_value ?? null;
  }
  return null;
}

function assignmentAfter(event: Event): string | null | undefined {
  if (event.metadata && "assignee" in event.metadata) {
    return ownerName(event.metadata.assignee);
  }
  return undefined;
}

function changeValueFor(
  event: Event,
  fieldName: string,
): string | null | undefined {
  const change = event.changes?.find(
    ({ field }) => field.trim().toLowerCase() === fieldName,
  );
  return change ? (change.after ?? null) : undefined;
}

function isNotesOnlyUpdate(event: Event): boolean {
  if (
    event.event_type !== "issue.update" &&
    event.event_type !== "issue.updated"
  ) {
    return false;
  }

  const changes = event.changes?.filter(
    ({ field }) => field.trim().toLowerCase() !== "updated_at",
  );
  return (
    changes?.length === 1 && changes[0]?.field.trim().toLowerCase() === "notes"
  );
}

function blockedNoteFor(notes: string | null | undefined): string | null {
  const trimmed = notes?.trim();
  if (!trimmed || !/^blocked:/i.test(trimmed)) return null;
  return trimmed.replace(/^blocked:\s*/i, "").trim();
}

function clearingNoteFor(notes: string | null | undefined): string | null {
  const trimmed = notes?.trim();
  if (!trimmed || /^blocked:/i.test(trimmed)) return null;
  return trimmed;
}

function auditEventFor(event: Event): JourneyAuditEvent {
  return {
    id: event.id,
    at: event.created_at,
    actor: ownerName(event.actor),
    actorKind: actorKindFor(event.actor),
    text: describeEvent(event),
  };
}

function orderedEvents(events: readonly Event[]): OrderedEvent[] {
  return events
    .map((event, index): OrderedEvent => {
      const parsed = Date.parse(event.created_at);
      return {
        event,
        index,
        atMs: Number.isFinite(parsed) ? parsed : Number.POSITIVE_INFINITY,
      };
    })
    .sort((a, b) => a.atMs - b.atMs || a.index - b.index);
}

function auditTime(event: JourneyAuditEvent): number {
  const parsed = Date.parse(event.at);
  return Number.isFinite(parsed) ? parsed : Number.POSITIVE_INFINITY;
}

function clampFraction(value: number): number {
  return Math.min(1, Math.max(0, value));
}

/**
 * Fold issue history into task-stage spans. The input is never mutated and is
 * sorted defensively because remote backends are not required to preserve the
 * endpoint's chronological order.
 */
export function foldJourney(
  events: readonly Event[],
  nowMs = Date.now(),
): JourneySpan[] {
  const spans: MutableSpan[] = [];
  const state: { current: MutableSpan | null } = { current: null };
  const haltState: { current: MutableHalt | null } = { current: null };
  let pendingEvents: JourneyAuditEvent[] = [];
  let pendingBlockNoteAudit: JourneyAuditEvent | null = null;
  let notes: string | null | undefined;
  // Undefined means the bounded event window has not established ownership;
  // null means an event explicitly established that the issue is unassigned.
  let owner: string | null | undefined;

  const finishHalt = (end: string, endMs: number) => {
    if (!haltState.current) return;
    haltState.current.end = end;
    haltState.current.endMs = endMs;
    haltState.current.durationMs = Math.max(
      0,
      endMs - haltState.current.startMs,
    );
    haltState.current = null;
  };

  const finishCurrent = (end: string, endMs: number) => {
    if (!state.current) return;
    finishHalt(end, endMs);
    state.current.end = end;
    state.current.endMs = endMs;
    state.current.durationMs = Math.max(0, endMs - state.current.startMs);
    state.current = null;
  };

  const beginStage = (
    stage: Exclude<JourneyStage, "Closed">,
    at: string,
    atMs: number,
    nextOwner: string | null,
  ) => {
    if (state.current?.stage === stage && state.current.owner === nextOwner) {
      return;
    }
    finishCurrent(at, atMs);
    const span: MutableSpan = {
      stage,
      owner: nextOwner,
      start: at,
      end: null,
      durationMs: Math.max(0, nowMs - atMs),
      haltedMs: 0,
      halts: [],
      events: pendingEvents,
      startMs: atMs,
      endMs: null,
    };
    pendingEvents = [];
    state.current = span;
    spans.push(span);
  };

  const latestSpan = (): MutableSpan | null => {
    return spans[spans.length - 1] ?? null;
  };

  const attachAudit = (audit: JourneyAuditEvent) => {
    if (haltState.current) {
      haltState.current.events.push(audit);
      return;
    }
    const target = state.current ?? latestSpan();
    if (target) target.events.push(audit);
    else pendingEvents.push(audit);
  };

  const attachPendingBlockNoteAudit = () => {
    if (!pendingBlockNoteAudit) return;
    attachAudit(pendingBlockNoteAudit);
    pendingBlockNoteAudit = null;
  };

  const closeHaltWithAudit = (
    audit: JourneyAuditEvent,
    end: string,
    endMs: number,
  ): boolean => {
    if (!haltState.current) return false;
    haltState.current.events.push(audit);
    finishHalt(end, endMs);
    return true;
  };

  const openHalt = (event: Event, atMs: number) => {
    if (!state.current) {
      if (owner === undefined) owner = ownerName(event.actor);
      beginStage("In progress", event.created_at, atMs, owner ?? null);
    }
    const note = blockedNoteFor(notes);
    if (haltState.current) {
      haltState.current.note ??= note;
      if (note !== null) pendingBlockNoteAudit = null;
      return;
    }

    const halt: MutableHalt = {
      start: event.created_at,
      end: null,
      durationMs: Math.max(0, nowMs - atMs),
      note,
      clearedNote: null,
      events: [],
      startFraction: 0,
      endFraction: 0,
      startMs: atMs,
      endMs: null,
    };
    // Fleet writes a BLOCKED note in the event immediately before the status
    // transition. The quote in this band is the user-facing representation of
    // that write, so keep it out of the ordinary audit rows.
    if (note !== null) pendingBlockNoteAudit = null;
    haltState.current = halt;
    state.current?.halts.push(halt);
  };

  const closeJourney = (event: Event, atMs: number) => {
    finishCurrent(event.created_at, atMs);
    spans.push({
      stage: "Closed",
      owner: owner === undefined ? ownerName(event.actor) : owner,
      start: event.created_at,
      end: event.created_at,
      durationMs: 0,
      haltedMs: 0,
      halts: [],
      events: [],
      startMs: atMs,
      endMs: atMs,
    });
    state.current = null;
  };

  for (const { event, atMs } of orderedEvents(events)) {
    const audit = auditEventFor(event);
    if (!Number.isFinite(atMs)) {
      attachPendingBlockNoteAudit();
      attachAudit(audit);
      continue;
    }

    const action = event.event_type;
    const status = statusAfter(event)?.trim().toLowerCase() ?? "";
    const opensHalt =
      action === "issue.block" ||
      action === "issue.blocked" ||
      status === "blocked";
    if (!opensHalt) attachPendingBlockNoteAudit();

    const nextNotes = changeValueFor(event, "notes");
    const notesOnlyUpdate = isNotesOnlyUpdate(event);
    let suppressAudit = false;
    if (nextNotes !== undefined) {
      notes = nextNotes;
      if (haltState.current) {
        const note = blockedNoteFor(notes);
        if (note !== null) {
          haltState.current.note ??= note;
        } else {
          haltState.current.clearedNote = clearingNoteFor(notes);
        }
        suppressAudit = notesOnlyUpdate;
      } else if (notesOnlyUpdate && blockedNoteFor(notes) !== null) {
        // Hold a candidate blocker note until the following event establishes
        // whether it really is the notes-first half of a block transition.
        pendingBlockNoteAudit = audit;
        suppressAudit = true;
      }
    }

    switch (action) {
      case "issue.create":
      case "issue.created":
        // Creation establishes a queued, unassigned issue unless the event
        // explicitly carries an assignee. The actor is the creator, not the
        // owner of the work.
        owner = assignmentAfter(event) ?? null;
        beginStage("Open", event.created_at, atMs, owner);
        attachAudit(audit);
        break;

      case "issue.claim": {
        const attachedToHalt = closeHaltWithAudit(
          audit,
          event.created_at,
          atMs,
        );
        owner = ownerName(event.actor);
        beginStage("In progress", event.created_at, atMs, owner);
        if (!attachedToHalt) attachAudit(audit);
        break;
      }

      case "issue.release": {
        const attachedToHalt = closeHaltWithAudit(
          audit,
          event.created_at,
          atMs,
        );
        owner = null;
        beginStage("Open", event.created_at, atMs, owner);
        if (!attachedToHalt) attachAudit(audit);
        break;
      }

      case "issue.defer":
      case "issue.deferred": {
        const attachedToHalt = closeHaltWithAudit(
          audit,
          event.created_at,
          atMs,
        );
        beginStage("Deferred", event.created_at, atMs, owner ?? null);
        if (!attachedToHalt) attachAudit(audit);
        break;
      }

      case "issue.undefer": {
        const attachedToHalt = closeHaltWithAudit(
          audit,
          event.created_at,
          atMs,
        );
        beginStage("Open", event.created_at, atMs, owner ?? null);
        if (!attachedToHalt) attachAudit(audit);
        break;
      }

      case "issue.assign": {
        const nextOwner = assignmentAfter(event);
        if (nextOwner === undefined || nextOwner === owner) {
          attachAudit(audit);
          break;
        }
        const attachedToHalt = closeHaltWithAudit(
          audit,
          event.created_at,
          atMs,
        );
        owner = nextOwner;
        const currentStage = state.current?.stage;
        const stage =
          currentStage && currentStage !== "Closed" ? currentStage : "Open";
        beginStage(stage, event.created_at, atMs, owner);
        if (!attachedToHalt) attachAudit(audit);
        break;
      }

      case "issue.close":
      case "issue.closed": {
        const attachedToHalt = closeHaltWithAudit(
          audit,
          event.created_at,
          atMs,
        );
        closeJourney(event, atMs);
        if (!attachedToHalt) attachAudit(audit);
        break;
      }

      case "issue.reopen":
      case "issue.reopened": {
        const attachedToHalt = closeHaltWithAudit(
          audit,
          event.created_at,
          atMs,
        );
        owner = null;
        beginStage("Open", event.created_at, atMs, owner);
        if (!attachedToHalt) attachAudit(audit);
        break;
      }

      case "issue.block":
      case "issue.blocked":
        openHalt(event, atMs);
        if (!suppressAudit) attachAudit(audit);
        break;

      case "issue.unblock":
      case "issue.unblocked":
        if (!closeHaltWithAudit(audit, event.created_at, atMs)) {
          attachAudit(audit);
        }
        break;

      case "issue.update":
      case "issue.updated":
      case "issue.status_changed": {
        if (status === "blocked") {
          openHalt(event, atMs);
          if (!suppressAudit) attachAudit(audit);
          break;
        }

        const stage = journeyStageForStatus(status);
        if (!stage) {
          if (!suppressAudit) attachAudit(audit);
          break;
        }

        const attachedToHalt = closeHaltWithAudit(
          audit,
          event.created_at,
          atMs,
        );
        if (stage === "Closed") {
          closeJourney(event, atMs);
          if (!attachedToHalt) attachAudit(audit);
          break;
        }
        if (owner === undefined) owner = ownerName(event.actor);
        beginStage(stage, event.created_at, atMs, owner);
        if (!attachedToHalt && !suppressAudit) attachAudit(audit);
        break;
      }

      // Labels are part of the available lifecycle window, but labels are
      // tags rather than stages. They still remain visible as audit rows.
      case "label.add":
      case "label.remove":
      case "issue.label_added":
      case "issue.label_removed":
      default:
        if (!suppressAudit) attachAudit(audit);
        break;
    }
  }

  attachPendingBlockNoteAudit();

  const openSpan = state.current;
  if (openSpan) {
    openSpan.durationMs = Math.max(0, nowMs - openSpan.startMs);
  }

  if (haltState.current) {
    haltState.current.durationMs = Math.max(
      0,
      nowMs - haltState.current.startMs,
    );
  }

  if (pendingEvents.length > 0 && spans.length > 0) {
    const firstContentSpan =
      spans.find((span) => span.stage !== "Closed") ?? spans[0];
    firstContentSpan?.events.push(...pendingEvents);
    pendingEvents = [];
  }

  // Journey is an audit trace: retain every semantic stage or ownership
  // transition, even when its displayed duration rounds to 0s.
  return spans.map((span): JourneySpan => {
    span.events.sort((a, b) => auditTime(a) - auditTime(b));
    span.halts.sort((a, b) => a.startMs - b.startMs);

    let haltedMs = 0;
    for (const halt of span.halts) {
      const haltEndMs = halt.endMs ?? nowMs;
      halt.durationMs = Math.max(0, haltEndMs - halt.startMs);
      haltedMs += halt.durationMs;

      if (span.durationMs === 0) {
        halt.startFraction = 0;
        halt.endFraction = 0;
        continue;
      }
      halt.startFraction = clampFraction(
        (halt.startMs - span.startMs) / span.durationMs,
      );
      halt.endFraction = Math.max(
        halt.startFraction,
        clampFraction((haltEndMs - span.startMs) / span.durationMs),
      );
    }

    return {
      stage: span.stage,
      owner: span.owner,
      start: span.start,
      end: span.end,
      durationMs: span.durationMs,
      haltedMs,
      halts: span.halts.map((halt) => ({
        start: halt.start,
        end: halt.end,
        durationMs: halt.durationMs,
        note: halt.note,
        clearedNote: halt.clearedNote,
        events: halt.events.sort((a, b) => auditTime(a) - auditTime(b)),
        startFraction: halt.startFraction,
        endFraction: halt.endFraction,
      })),
      events: span.events,
    };
  });
}
