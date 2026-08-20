import type { Event } from "@/types";

export type JourneyStage =
  | "Open"
  | "In progress"
  | "Stuck"
  | "Deferred"
  | "Review"
  | "Closed";

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

function stageForStatus(
  status: string | null | undefined,
): JourneyStage | null {
  switch (status?.trim().toLowerCase()) {
    case "open":
      return "Open";
    case "in_progress":
      return "In progress";
    case "blocked":
      return "Stuck";
    case "deferred":
      return "Deferred";
    case "review":
      return "Review";
    case "closed":
      return "Closed";
    default:
      return null;
  }
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

function assignmentAfter(event: Event): string | null {
  if (event.metadata && "assignee" in event.metadata) {
    return ownerName(event.metadata.assignee);
  }
  return null;
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
  let owner: string | null = null;

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
      owner: owner ?? ownerName(event.actor),
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

      case "issue.assign": {
        owner = assignmentAfter(event);
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
        owner = ownerName(event.actor);
        beginStage("Open", event.created_at, atMs, owner);
        break;

      case "issue.update":
      case "issue.updated":
      case "issue.status_changed": {
        const stage = stageForStatus(statusAfter(event));
        if (!stage) break;
        if (stage === "Closed") {
          closeJourney(event, atMs);
          break;
        }
        owner = owner ?? ownerName(event.actor);
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

  return spans.map(({ startMs: _startMs, ...span }) => span);
}
