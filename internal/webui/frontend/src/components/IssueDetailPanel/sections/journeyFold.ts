import type { Event } from "@/types";

import {
  hasDisplayableJourneyDuration,
  journeyStageForStatus,
  type JourneyStage,
} from "./journeyPresentation";

export type { JourneyStage } from "./journeyPresentation";

export interface JourneySpan {
  stage: JourneyStage;
  owner: string | null;
  start: string;
  end: string | null;
  durationMs: number;
}

interface MutableSpan extends JourneySpan {
  startMs: number;
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

function orderedEvents(events: readonly Event[]): OrderedEvent[] {
  return events
    .map((event, index): OrderedEvent | null => {
      const atMs = Date.parse(event.created_at);
      return Number.isFinite(atMs) ? { event, index, atMs } : null;
    })
    .filter((item): item is OrderedEvent => item !== null)
    .sort((a, b) => a.atMs - b.atMs || a.index - b.index);
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
  // Undefined means the bounded event window has not established ownership;
  // null means an event explicitly established that the issue is unassigned.
  let owner: string | null | undefined;

  const finishCurrent = (end: string, endMs: number) => {
    if (!state.current) return;
    state.current.end = end;
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
      startMs: atMs,
    };
    state.current = span;
    spans.push(span);
  };

  const closeJourney = (event: Event, atMs: number) => {
    finishCurrent(event.created_at, atMs);
    spans.push({
      stage: "Closed",
      owner: owner === undefined ? ownerName(event.actor) : owner,
      start: event.created_at,
      end: event.created_at,
      durationMs: 0,
      startMs: atMs,
    });
    state.current = null;
  };

  for (const { event, atMs } of orderedEvents(events)) {
    const action = event.event_type;

    switch (action) {
      case "issue.create":
      case "issue.created":
        owner = ownerName(event.actor);
        beginStage("Open", event.created_at, atMs, owner);
        break;

      case "issue.claim":
        owner = ownerName(event.actor);
        beginStage("In progress", event.created_at, atMs, owner);
        break;

      case "issue.release":
        owner = null;
        beginStage("Open", event.created_at, atMs, owner);
        break;

      case "issue.defer":
      case "issue.deferred":
        beginStage("Deferred", event.created_at, atMs, owner ?? null);
        break;

      case "issue.undefer":
        beginStage("Open", event.created_at, atMs, owner ?? null);
        break;

      case "issue.assign": {
        const nextOwner = assignmentAfter(event);
        if (nextOwner === undefined || nextOwner === owner) break;
        owner = nextOwner;
        const stage =
          state.current?.stage === "Closed"
            ? "Open"
            : (state.current?.stage ?? "Open");
        beginStage(stage, event.created_at, atMs, owner);
        break;
      }

      case "issue.close":
      case "issue.closed":
        closeJourney(event, atMs);
        break;

      case "issue.reopen":
      case "issue.reopened":
        owner = null;
        beginStage("Open", event.created_at, atMs, owner);
        break;

      case "issue.update":
      case "issue.updated":
      case "issue.status_changed": {
        const stage = journeyStageForStatus(statusAfter(event));
        if (!stage) break;
        if (stage === "Closed") {
          closeJourney(event, atMs);
          break;
        }
        if (owner === undefined) owner = ownerName(event.actor);
        beginStage(stage, event.created_at, atMs, owner);
        break;
      }

      // Labels are part of the available lifecycle window, but labels are
      // tags rather than stages. Recognizing them explicitly prevents an
      // add/remove action from disturbing the current status span.
      case "label.add":
      case "label.remove":
      case "issue.label_added":
      case "issue.label_removed":
        break;

      default:
        // Future fleet-db actions must not invalidate the journey we can fold.
        break;
    }
  }

  const openSpan = state.current;
  if (openSpan) {
    openSpan.durationMs = Math.max(0, nowMs - openSpan.startMs);
  }

  // Filter only after every event has updated the fold. A sub-resolution stage
  // remains state.current while later events are processed, and its ownership
  // changes remain available to the Closed marker. Live spans and the explicit
  // zero-length Closed marker are deliberately retained. A live sub-resolution
  // span is retained here to keep its clock running; Journey withholds it from
  // the rail until the shared formatter can express its duration.
  return spans
    .filter(
      (span) =>
        span.stage === "Closed" ||
        span.end === null ||
        hasDisplayableJourneyDuration(span.durationMs),
    )
    .map(({ startMs: _startMs, ...span }) => span);
}
