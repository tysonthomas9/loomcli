import { useCallback, useEffect, useMemo, useState } from "react";
import { AgentEventStream, listAgents } from "@/api/agentsv1";
import type { Agent, ListAgentsQuery } from "@/api/agentsv1";
import {
  ROSTER_KINDS,
  applyEvents,
  newChildParents,
  upsert,
} from "./agentRoster";
import type { Roster } from "./agentRoster";

async function listAll(ws: string, q: ListAgentsQuery = {}): Promise<Agent[]> {
  const out: Agent[] = [];
  let after = "";
  do {
    const page = await listAgents(ws, { ...q, after });
    out.push(...page.agents);
    after = page.next;
  } while (after);
  return out;
}

const message = (err: unknown) =>
  err instanceof Error ? err.message : String(err);

/**
 * The live agent roster: one List, then one live-only event stream over
 * every listed agent. child.created lists the parent's children; a
 * reconnect or feed.gap re-lists everything (§9.5 step 4). A new lead is no
 * event on a listed agent, so it re-lists whenever the open chat changes
 * (New Agent opens the new agent's chat).
 */
export function useAgentRoster(
  workspaceId: string,
  openId?: string,
): {
  roster: Roster;
  error: string | null;
} {
  const [roster, setRoster] = useState<Roster>(new Map());
  const [error, setError] = useState<string | null>(null);

  const relist = useCallback(() => {
    listAll(workspaceId)
      .then((agents) => {
        setRoster(upsert(new Map(), agents));
        setError(null);
      })
      .catch((err) => setError(message(err)));
  }, [workspaceId]);

  useEffect(() => relist(), [relist, openId]);

  // The stream reopens only when the set of agents changes.
  const ids = useMemo(() => [...roster.keys()].sort().join(","), [roster]);

  useEffect(() => {
    if (!ids) return;
    const stream = new AgentEventStream(workspaceId, {
      agents: ids.split(","),
      types: ROSTER_KINDS,
      live: true,
      onEvents: (added) => {
        setRoster((r) => applyEvents(r, added));
        for (const parent of newChildParents(added))
          listAll(workspaceId, { parent })
            .then((kids) => setRoster((r) => upsert(r, kids)))
            .catch((err) => setError(message(err)));
      },
      onResync: relist,
    });
    void stream.connect();
    return () => stream.close();
  }, [workspaceId, ids, relist]);

  return { roster, error };
}
