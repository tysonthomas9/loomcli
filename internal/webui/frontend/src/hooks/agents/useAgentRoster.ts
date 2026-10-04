import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { AgentEventStream, listAgents } from "@/api/agentsv1";
import type { Agent, AgentHistory, ListAgentsQuery } from "@/api/agentsv1";
import {
  ROSTER_KINDS,
  applyActivity,
  applyEvents,
  newChildParents,
  upsert,
} from "./agentRoster";
import type { Activities, Roster } from "./agentRoster";

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

// The sidebar's roster, shared so a lead's chat reads its children's live
// state from the one roster stream rather than opening another.
let shared: Roster = new Map();
let sharedActivity: Activities = new Map();
const listeners = new Set<() => void>();
const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => listeners.delete(l);
};

/** One agent from the sidebar's live roster, if it is listed. */
export const useRosterAgent = (id: string): Agent | undefined =>
  useSyncExternalStore(subscribe, () => shared.get(id));

/** The whole live roster (a stable map until it changes). */
export const useRoster = (): Roster =>
  useSyncExternalStore(subscribe, () => shared);

/** Each listed agent's latest step this turn, from the roster stream. */
export const useRosterActivity = (): Activities =>
  useSyncExternalStore(subscribe, () => sharedActivity);

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
  const [activity, setActivity] = useState<Activities>(new Map());
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

  useEffect(() => {
    shared = roster;
    sharedActivity = activity;
    listeners.forEach((l) => l());
  }, [roster, activity]);

  // The stream reopens only when the set of agents changes.
  const ids = useMemo(() => [...roster.keys()].sort().join(","), [roster]);
  // The last stream's cache, so the next one opens at the cursors it
  // reached; a new agent opens at 0 (RR1).
  const history = useRef<{ ws: string; h: AgentHistory } | null>(null);
  const purged = useMemo(
    () =>
      [...roster.values()]
        .filter((a) => a.history_purged_at)
        .map((a) => a.agent_id)
        .sort()
        .join(","),
    [roster],
  );

  useEffect(() => {
    if (!ids) return;
    const stream = new AgentEventStream(workspaceId, {
      agents: ids.split(","),
      types: ROSTER_KINDS,
      live: true,
      ...(history.current?.ws === workspaceId
        ? { history: history.current.h }
        : {}),
      expired: purged ? purged.split(",") : [],
      onEvents: (added) => {
        setRoster((r) => applyEvents(r, added));
        setActivity((m) => applyActivity(m, added));
        for (const parent of newChildParents(added))
          listAll(workspaceId, { parent })
            .then((kids) => setRoster((r) => upsert(r, kids)))
            .catch((err) => setError(message(err)));
      },
      onNotice: (n) => {
        if (n.kind === "tool.started")
          setActivity((m) => applyActivity(m, [n]));
      },
      // Steps missed while away are not caught up: "Working…" until the next.
      onResync: () => {
        setActivity(new Map());
        relist();
      },
    });
    history.current = { ws: workspaceId, h: stream.history };
    void stream.connect();
    return () => stream.close();
  }, [workspaceId, ids, purged, relist]);

  return { roster, error };
}
