import type { AgentEvent } from "./types";

/**
 * The browser's cache of each agent's saved events, in seq order. Events from
 * ListEvents pages and from the live stream both go through merge, which drops
 * any EventID it already has, so overlapping replays never duplicate.
 */
export class AgentHistory {
  private agents = new Map<
    string,
    { events: AgentEvent[]; ids: Set<string> }
  >();

  events(agentId: string): readonly AgentEvent[] {
    return this.agents.get(agentId)?.events ?? [];
  }

  /** The highest seq held for agentId: the cursor to page or stream after. */
  lastSeq(agentId: string): number {
    const events = this.events(agentId);
    return events[events.length - 1]?.seq ?? 0;
  }

  /** Adds the events not already held and returns them. */
  merge(events: AgentEvent[]): AgentEvent[] {
    const added: AgentEvent[] = [];
    for (const e of events) {
      let h = this.agents.get(e.agent_id);
      if (!h) {
        h = { events: [], ids: new Set() };
        this.agents.set(e.agent_id, h);
      }
      if (h.ids.has(e.event_id)) continue;
      h.ids.add(e.event_id);
      const last = h.events[h.events.length - 1];
      h.events.push(e);
      if (last && last.seq > e.seq) h.events.sort((a, b) => a.seq - b.seq);
      added.push(e);
    }
    return added;
  }
}
