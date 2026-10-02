// The agent roster projection (design v2 §9.4–§9.5): List fills it, and the
// stream's child.created, agent.state_changed and agent.deleted events keep it
// current. Agents are keyed by id, so a re-list never duplicates a row.

import type { Agent, AgentEvent } from "@/api/agentsv1";

export type Roster = ReadonlyMap<string, Agent>;

/** The saved kinds the roster subscribes to. */
export const ROSTER_KINDS = [
  "child.created",
  "agent.state_changed",
  "agent.deleted",
];

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
