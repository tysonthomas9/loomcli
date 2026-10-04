// The AgentChat view model (design v2 §9.2): saved agent events plus live
// deltas become chat items. Nothing here depends on the harness; every
// harness maps its native events to the same Loom kinds before they get here.

import type { AgentEvent, Completion, WaitingMessage } from "@/api/agentsv1";

/** The fields of a saved native event's payload that the chat reads. */
interface NativePayload {
  itemId?: string;
  itemKind?: string;
  text?: string;
  stopReason?: string;
  /** A failed turn's reason, as its harness gave it. */
  error?: string;
  /** A delivery's slot sender, such as user:<id> or agent:<AgentID>. */
  sender?: string;
  /**
   * An agent's delivery: the child task_completed records its text carries
   * (empty when none; absent on rows saved before DF1) and the text without
   * them.
   */
  completions?: Completion[];
  message?: string;
  /** A tool item's call, on its tool.started notice and item.completed. */
  tool?: ToolCall;
}

/**
 * A tool call as every harness reports it: the harness's tool name, its
 * input as text (JSON when structured) and, once it ended, its output.
 */
export interface ToolCall {
  name?: string;
  input?: string;
  output?: string;
  failed?: boolean;
}

export type ToolStatus = "running" | "completed" | "failed";

export type ChatItem =
  | { key: string; kind: "user"; text: string }
  /** A message another agent sent on purpose, such as a child's agent_send. */
  | {
      key: string;
      kind: "from_agent";
      agent: string;
      name: string;
      text: string;
    }
  | { key: string; kind: "agent"; text: string; streaming?: boolean }
  | { key: string; kind: "reasoning"; text: string; streaming?: boolean }
  | { key: string; kind: "tool"; tool: ToolCall; status: ToolStatus }
  | { key: string; kind: "turn_end"; reason: string; error?: string }
  /** One marker for children started back to back. */
  | { key: string; kind: "started"; children: StartedChild[]; at: string }
  | {
      key: string;
      kind: "completion";
      record: TaskCompleted;
      /** The child's name from its child.created, else its id. */
      name: string;
      at: string;
      /** Whether the record waits for this agent, or it was delivered. */
      delivery?: Delivery;
    };

export interface StartedChild {
  child: string;
  name: string;
}

export type Delivery = "waiting" | "delivered";

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

/** A child attempt's key, as its record's task_completed:<child>:<attempt>. */
export const attemptKey = (c: Completion) => `${c.child}:${c.attempt}`;

/** The agent id of an agent:<id> sender, else null. */
export const senderAgent = (sender?: string) =>
  sender?.startsWith("agent:") ? sender.slice("agent:".length) : null;

/** One chat item for a saved event, or null for kinds the chat skips. */
function itemFor(
  e: AgentEvent,
  names: ReadonlyMap<string, string>,
): ChatItem | null {
  const p = payload(e);
  const key = e.event_id;
  switch (e.kind) {
    case "message.delivered": {
      const from = senderAgent(p.sender);
      if (!from) return { key, kind: "user", text: p.text ?? "" };
      // Records show on their completion cards: only the rest is a message.
      const text = p.completions ? (p.message ?? "") : (p.text ?? "");
      if (!text.trim()) return null;
      return {
        key,
        kind: "from_agent",
        agent: from,
        name: names.get(from) ?? from,
        text,
      };
    }
    case "item.completed":
      if (p.itemKind === "tool") {
        const tool = p.tool ?? {};
        const status = tool.failed ? "failed" : "completed";
        return { key, kind: "tool", tool, status };
      }
      if (p.itemKind === "reasoning")
        return { key, kind: "reasoning", text: p.text ?? "" };
      return { key, kind: "agent", text: p.text ?? "" };
    case "agent.turn_completed":
      return p.stopReason && p.stopReason !== "completed"
        ? {
            key,
            kind: "turn_end",
            reason: p.stopReason,
            ...(p.error ? { error: p.error } : {}),
          }
        : null;
    case "child.created": {
      const c = e.payload as StartedChild;
      return {
        key,
        kind: "started",
        children: [{ child: c.child, name: c.name }],
        at: e.created_at,
      };
    }
    // Keyed task_completed:<child>:<attempt>, so each attempt shows once.
    case "task_completed":
      return {
        key,
        kind: "completion",
        record: e.payload as TaskCompleted,
        name:
          names.get((e.payload as TaskCompleted).child) ??
          (e.payload as TaskCompleted).child,
        at: e.created_at,
      };
    default:
      return null;
  }
}

/** An item in progress: a message's or reasoning's text so far, or a tool call that started. */
export type LiveItem =
  | { kind: "message" | "reasoning"; text: string }
  | { kind: "tool"; tool: ToolCall };

/** Live items per item id, built from notices until the item completes. */
export type Streaming = ReadonlyMap<string, LiveItem>;

/**
 * Adds one notice: a delta appends its text to its message or reasoning, a
 * tool.started adds its tool call; other notices change nothing.
 */
export function addDelta(streaming: Streaming, notice: AgentEvent): Streaming {
  const p = payload(notice);
  if (!p.itemId) return streaming;
  let item: LiveItem;
  if (notice.kind === "tool.started") {
    item = { kind: "tool", tool: p.tool ?? {} };
  } else if (notice.kind === "delta" && p.itemKind !== "tool") {
    const kind = p.itemKind === "reasoning" ? "reasoning" : "message";
    const was = streaming.get(p.itemId);
    const text = was && was.kind !== "tool" ? was.text : "";
    item = { kind, text: text + (p.text ?? "") };
  } else {
    return streaming;
  }
  const next = new Map(streaming);
  next.set(p.itemId, item);
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

/** Each child's name, from its child.created. */
export function childNames(events: readonly AgentEvent[]): Map<string, string> {
  return new Map(
    events
      .filter((e) => e.kind === "child.created")
      .map((e) => {
        const c = e.payload as StartedChild;
        return [c.child, c.name] as const;
      }),
  );
}

/**
 * Each child attempt's delivery state: delivered once a saved delivery
 * carries its record, else waiting while a waiting message does. Matched on
 * the records the server names, never on message text.
 */
export function deliveries(
  events: readonly AgentEvent[],
  waiting: readonly WaitingMessage[],
): Map<string, Delivery> {
  const out = new Map<string, Delivery>();
  for (const w of waiting)
    for (const c of w.completions ?? []) out.set(attemptKey(c), "waiting");
  for (const e of events)
    if (e.kind === "message.delivered")
      for (const c of payload(e).completions ?? [])
        out.set(attemptKey(c), "delivered");
  return out;
}

/**
 * The chat transcript: saved items in seq order, then any streaming text.
 * waiting is the agent's waiting messages, for each completion's state.
 */
export function chatItems(
  events: readonly AgentEvent[],
  streaming: Streaming,
  waiting: readonly WaitingMessage[] = [],
): ChatItem[] {
  // A row saved before DF1 from a child carries only its completion
  // records, which show on their cards; it is not shown again.
  const names = childNames(events);
  const state = deliveries(events, waiting);
  const items: ChatItem[] = [];
  for (const e of events) {
    const p = payload(e);
    const from = e.kind === "message.delivered" ? senderAgent(p.sender) : null;
    if (from && !p.completions && names.has(from)) continue;
    const item = itemFor(e, names);
    if (!item) continue;
    const last = items[items.length - 1];
    if (item.kind === "started" && last?.kind === "started") {
      items[items.length - 1] = {
        ...last,
        children: [...last.children, ...item.children],
      };
      continue;
    }
    if (item.kind === "completion") {
      const d = state.get(attemptKey(item.record));
      if (d) item.delivery = d;
    }
    items.push(item);
  }
  // A notice can come after its item's saved completion (a subscriber
  // replaying history gets saved rows before live notices): a completed
  // item never shows live again.
  const completed = new Set(
    events
      .filter((e) => e.kind === "item.completed")
      .map((e) => payload(e).itemId),
  );
  for (const [id, live] of streaming) {
    if (completed.has(id)) continue;
    const key = `live:${id}`;
    if (live.kind === "tool")
      items.push({ key, kind: "tool", tool: live.tool, status: "running" });
    else if (live.kind === "reasoning")
      items.push({ key, kind: "reasoning", text: live.text, streaming: true });
    else items.push({ key, kind: "agent", text: live.text, streaming: true });
  }
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

/**
 * The failure reason of the agent's latest turn, while no later message
 * started another; null when it did not fail or gave no reason.
 */
export function latestTurnError(items: readonly ChatItem[]): string | null {
  for (const item of [...items].reverse()) {
    if (item.kind === "user" || item.kind === "from_agent") return null;
    if (item.kind === "turn_end") return item.error ?? null;
  }
  return null;
}
