import type { MutationPayload } from "@/types/workspace";
import type { ActivityFilters, AuditEvent } from "@/types/activity";

export interface ActivityDescription {
  actor: string;
  beforeEntity: string;
  entityId: string;
  afterEntity?: string;
  text: string;
}

export interface ActivityTimelineState {
  history: AuditEvent[];
  live: AuditEvent[];
  nextCursor: string;
}

export type ActivityTimelineAction =
  | {
      type: "history";
      events: AuditEvent[];
      nextCursor: string;
      append: boolean;
    }
  | { type: "live"; event: AuditEvent };

function humanizeValue(value: string): string {
  return value.replace(/_/g, " ");
}

function stringDetail(event: AuditEvent, key: string): string | undefined {
  const value = event.details[key];
  return typeof value === "string" && value.length > 0 ? value : undefined;
}

function description(
  event: AuditEvent,
  beforeEntity: string,
  afterEntity?: string,
): ActivityDescription {
  const text = [
    event.actor || "system",
    beforeEntity,
    event.entity_id,
    afterEntity,
  ]
    .filter(Boolean)
    .join(" ");

  return {
    actor: event.actor || "system",
    beforeEntity,
    entityId: event.entity_id,
    ...(afterEntity ? { afterEntity } : {}),
    text,
  };
}

/** Convert an audit action into the sentence parts rendered by a timeline row. */
export function describeActivityEvent(event: AuditEvent): ActivityDescription {
  const oldStatus = stringDetail(event, "old_status");
  const newStatus = stringDetail(event, "new_status");
  if (oldStatus && newStatus) {
    return description(event, "moved", `to ${humanizeValue(newStatus)}`);
  }

  switch (event.action) {
    case "issue.create":
      return description(event, "created");
    case "issue.update":
      return description(event, "updated");
    case "issue.claim":
      return description(event, "claimed");
    case "issue.release":
      return description(event, "released");
    case "issue.assign":
      return description(event, "assigned");
    case "issue.close":
      return description(event, "closed");
    case "issue.reopen":
      return description(event, "reopened");
    case "label.add": {
      const label = stringDetail(event, "label") ?? "label";
      return description(event, `label ${label} added to`);
    }
    case "label.remove": {
      const label = stringDetail(event, "label") ?? "label";
      return description(event, `label ${label} removed from`);
    }
    case "comment.add":
      return description(event, "commented on");
    case "agent.create":
      return description(event, "created agent");
    case "agent.update":
      return description(event, "updated agent");
    case "workspace.create":
      return description(event, "created workspace");
    default:
      return description(event, `${event.action} on`);
  }
}

function fallbackIdentity(event: AuditEvent): string {
  return `${event.action}\u0000${event.entity_id}\u0000${event.timestamp}`;
}

export function activityEventsMatch(a: AuditEvent, b: AuditEvent): boolean {
  if (a.cursor && b.cursor) return a.cursor === b.cursor;
  return fallbackIdentity(a) === fallbackIdentity(b);
}

function newestFirst(a: AuditEvent, b: AuditEvent): number {
  return new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime();
}

/**
 * Merge history/live collections, preserving the first copy of each event.
 *
 * Kept events are indexed rather than rescanned, so merging stays linear as the
 * history grows page by page. The index mirrors activityEventsMatch exactly:
 * two cursor-backed events match only on cursor, while any comparison involving
 * a cursor-less event (the shape the live SSE feed produces) falls back to
 * action/entity/timestamp identity.
 */
export function mergeActivityEvents(
  ...collections: readonly AuditEvent[][]
): AuditEvent[] {
  const merged: AuditEvent[] = [];
  const keptCursors = new Set<string>();
  const keptFallbacks = new Set<string>();
  const keptCursorlessFallbacks = new Set<string>();

  for (const candidate of collections.flat()) {
    const fallback = fallbackIdentity(candidate);
    const duplicate = candidate.cursor
      ? keptCursors.has(candidate.cursor) ||
        keptCursorlessFallbacks.has(fallback)
      : keptFallbacks.has(fallback);
    if (duplicate) continue;

    merged.push(candidate);
    keptFallbacks.add(fallback);
    if (candidate.cursor) keptCursors.add(candidate.cursor);
    else keptCursorlessFallbacks.add(fallback);
  }
  return merged.sort(newestFirst);
}

export function filterActivityEvents(
  events: readonly AuditEvent[],
  filters: ActivityFilters,
): AuditEvent[] {
  return events.filter(
    (event) =>
      (!filters.actor || event.actor === filters.actor) &&
      (!filters.entity || event.entity_id === filters.entity),
  );
}

export function activityTimelineReducer(
  state: ActivityTimelineState,
  action: ActivityTimelineAction,
): ActivityTimelineState {
  if (action.type === "live") {
    if (
      [...state.history, ...state.live].some((event) =>
        activityEventsMatch(event, action.event),
      )
    ) {
      return state;
    }
    return { ...state, live: [action.event, ...state.live].slice(0, 200) };
  }

  return {
    ...state,
    history: action.append
      ? mergeActivityEvents(state.history, action.events)
      : mergeActivityEvents(action.events),
    nextCursor: action.nextCursor,
  };
}

export function selectActivityEvents(
  state: ActivityTimelineState,
  filters: ActivityFilters,
): AuditEvent[] {
  return filterActivityEvents(
    mergeActivityEvents(state.history, state.live),
    filters,
  );
}

/** Convert only complete audit-shaped mutation payloads from the shared SSE. */
export function toAuditEvent(mutation: MutationPayload): AuditEvent | null {
  if (mutation.action === "agent.refresh") return null;
  if (
    !mutation.action ||
    !mutation.entity_type ||
    !mutation.entity_id ||
    !mutation.actor
  ) {
    return null;
  }

  const details: Record<string, unknown> = {};
  if (mutation.old_status) details.old_status = mutation.old_status;
  if (mutation.new_status) details.new_status = mutation.new_status;

  return {
    cursor: mutation.cursor ?? "",
    timestamp: mutation.timestamp,
    actor: mutation.actor,
    action: mutation.action,
    entity_type: mutation.entity_type,
    entity_id: mutation.entity_id,
    details,
  };
}

export const EMPTY_ACTIVITY_TIMELINE: ActivityTimelineState = {
  history: [],
  live: [],
  nextCursor: "",
};
