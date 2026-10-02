// The AgentChat view model (design v2 §9.2): saved agent events plus live
// deltas become chat items. Nothing here depends on the harness; every
// harness maps its native events to the same Loom kinds before they get here.

import type { AgentEvent } from "@/api/agentsv1";

/** The fields of a saved native event's payload that the chat reads. */
interface NativePayload {
  itemId?: string;
  itemKind?: string;
  text?: string;
  stopReason?: string;
  /** A delivery's slot sender, such as user:<id> or agent:<AgentID>. */
  sender?: string;
}

export type ChatItem =
  | { key: string; kind: "user"; text: string }
  | { key: string; kind: "agent"; text: string; streaming?: boolean }
  | { key: string; kind: "reasoning"; text: string }
  | { key: string; kind: "tool"; text: string }
  | { key: string; kind: "turn_end"; reason: string }
  | { key: string; kind: "child"; child: string; name: string }
  | { key: string; kind: "completion"; record: TaskCompleted };

/** A child attempt's completion record, saved once per attempt (§10.3). */
export interface TaskCompleted {
  child: string;
  attempt: number;
  outcome: string;
  branch?: string;
  head?: string;
  summary?: string;
}

function payload(e: AgentEvent): NativePayload {
  return e.payload && typeof e.payload === "object"
    ? (e.payload as NativePayload)
    : {};
}

/** One chat item for a saved event, or null for kinds the chat skips. */
function itemFor(e: AgentEvent): ChatItem | null {
  const p = payload(e);
  const key = e.event_id;
  switch (e.kind) {
    case "message.delivered":
      return { key, kind: "user", text: p.text ?? "" };
    case "item.completed":
      if (p.itemKind === "tool")
        return { key, kind: "tool", text: p.text ?? "" };
      if (p.itemKind === "reasoning")
        return { key, kind: "reasoning", text: p.text ?? "" };
      return { key, kind: "agent", text: p.text ?? "" };
    case "agent.turn_completed":
      return p.stopReason && p.stopReason !== "completed"
        ? { key, kind: "turn_end", reason: p.stopReason }
        : null;
    case "child.created": {
      const c = e.payload as { child: string; name: string };
      return { key, kind: "child", child: c.child, name: c.name };
    }
    // Keyed task_completed:<child>:<attempt>, so each attempt shows once.
    case "task_completed":
      return { key, kind: "completion", record: e.payload as TaskCompleted };
    default:
      return null;
  }
}

/** Live text per item id, built from delta notices until the item completes. */
export type Streaming = ReadonlyMap<string, string>;

/** Appends one delta notice's text to its item; ignores other notices. */
export function addDelta(streaming: Streaming, notice: AgentEvent): Streaming {
  const p = payload(notice);
  if (notice.kind !== "delta" || !p.itemId || p.itemKind === "reasoning")
    return streaming;
  const next = new Map(streaming);
  next.set(p.itemId, (streaming.get(p.itemId) ?? "") + (p.text ?? ""));
  return next;
}

/** Drops the live text of items that now have a saved item.completed. */
export function settle(streaming: Streaming, events: AgentEvent[]): Streaming {
  const done = events
    .filter((e) => e.kind === "item.completed")
    .map((e) => payload(e).itemId)
    .filter((id): id is string => !!id && streaming.has(id));
  if (!done.length) return streaming;
  const next = new Map(streaming);
  done.forEach((id) => next.delete(id));
  return next;
}

/** The chat transcript: saved items in seq order, then any streaming text. */
export function chatItems(
  events: readonly AgentEvent[],
  streaming: Streaming,
): ChatItem[] {
  // A child's slot carries only its completion records, which show as
  // records; its delivery to the lead is not shown again.
  const kids = new Set(
    events
      .filter((e) => e.kind === "child.created")
      .map((e) => `agent:${(e.payload as { child: string }).child}`),
  );
  const items = events
    .filter(
      (e) =>
        e.kind !== "message.delivered" || !kids.has(payload(e).sender ?? ""),
    )
    .map(itemFor)
    .filter((i): i is ChatItem => i !== null);
  for (const [id, text] of streaming)
    items.push({ key: `live:${id}`, kind: "agent", text, streaming: true });
  return items;
}

/** Saved kinds after which the agent's state, waiting messages or asks change. */
export const REFRESH_KINDS = new Set([
  "message.waiting",
  "message.withdrawn",
  "message.delivered",
  "agent.state_changed",
  "agent.idle",
  "turn.started",
  "agent.turn_completed",
  "ask.opened",
  "ask.resolved",
  "ask.lost",
]);

/**
 * The caller's own waiting-slot sender, as the server names it: user:<JWT
 * subject> (the session user's id) when signed in, else user:local. Only that
 * slot can be edited or cleared from here.
 */
export const ownSender = (userId?: string | null) =>
  `user:${userId || "local"}`;
