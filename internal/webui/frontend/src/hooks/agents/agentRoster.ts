// The agent roster projection (design v2 §9.4–§9.5): List fills it, and the
// stream's child.created, agent.state_changed and agent.deleted events keep it
// current. Agents are keyed by id, so a re-list never duplicates a row. The
// same stream's tool starts and completed items give each working agent its
// latest step (DF2), kept beside the roster and never saved.

import type { Agent, AgentEvent } from "@/api/agentsv1";

export type Roster = ReadonlyMap<string, Agent>;

/**
 * The kinds the roster subscribes to: saved ones, and tool.started, which
 * the server sends a stream that names it without asking for deltas.
 */
export const ROSTER_KINDS = [
  "child.created",
  "agent.state_changed",
  "agent.deleted",
  "tool.started",
  "item.completed",
];

/** An agent's latest step this turn, and when the turn started, if seen. */
export interface Activity {
  /** A tool.started, or a completed tool or reasoning item. */
  step?: AgentEvent;
  turnAt?: string;
}

export type Activities = ReadonlyMap<string, Activity>;

/** The states of an agent at work; an ask (waiting) is the same turn. */
const WORKING = new Set(["creating", "active", "waiting", "stopping"]);

/**
 * Applies stream events and notices to each agent's activity: a change to
 * active from a resting state starts a turn (its time, no step yet), one to
 * a resting state drops the agent, and a tool start or completed tool or
 * reasoning item is its latest step. Other events change nothing.
 */
export function applyActivity(m: Activities, events: AgentEvent[]): Activities {
  let next: Map<string, Activity> | null = null;
  const edit = () => (next ??= new Map(m));
  for (const e of events) {
    const p = (e.payload ?? {}) as {
      from?: string;
      to?: string;
      itemKind?: string;
    };
    if (e.kind === "agent.state_changed" && p.to) {
      if (!WORKING.has(p.to)) edit().delete(e.agent_id);
      else if (p.to === "active" && !WORKING.has(p.from ?? ""))
        edit().set(e.agent_id, { turnAt: e.created_at });
    } else if (
      e.kind === "tool.started" ||
      (e.kind === "item.completed" &&
        (p.itemKind === "tool" || p.itemKind === "reasoning"))
    ) {
      const was = (next ?? m).get(e.agent_id);
      edit().set(e.agent_id, { ...was, step: e });
    }
  }
  return next ?? m;
}

export const upsert = (r: Roster, agents: Agent[]): Roster =>
  new Map([...r, ...agents.map((a) => [a.agent_id, a] as const)]);

/** The parents whose child.created needs List{Parent}. */
export const newChildParents = (events: AgentEvent[]): string[] => [
  ...new Set(
    events.filter((e) => e.kind === "child.created").map((e) => e.agent_id),
  ),
];

/** Applies stream events: a state change sets the state, a delete drops it. */
export function applyEvents(r: Roster, events: AgentEvent[]): Roster {
  const next = new Map(r);
  for (const e of events) {
    const a = next.get(e.agent_id);
    if (e.kind === "agent.deleted") next.delete(e.agent_id);
    else if (e.kind === "agent.state_changed" && a) {
      const p = e.payload as { to?: string; reason?: string };
      if (p.to)
        next.set(a.agent_id, {
          ...a,
          state: p.to,
          state_reason: p.reason || null,
        });
    }
  }
  return next;
}

/**
 * Each agent's children, oldest first, keyed by parent id. Leads, and any
 * agent whose parent is not in the roster, are under "".
 */
export function childrenByParent(r: Roster): Map<string, Agent[]> {
  const out = new Map<string, Agent[]>();
  const sorted = [...r.values()].sort((a, b) =>
    a.created_at.localeCompare(b.created_at),
  );
  for (const a of sorted) {
    const p =
      a.parent_agent_id && r.has(a.parent_agent_id) ? a.parent_agent_id : "";
    out.set(p, [...(out.get(p) ?? []), a]);
  }
  return out;
}
