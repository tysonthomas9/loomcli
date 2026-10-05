import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { AgentEventStream, getAgent, listAgents } from "@/api/agentsv1";
import type {
  Agent,
  AgentEvent,
  AgentHistory,
  ListAgentsQuery,
} from "@/api/agentsv1";
import {
  ROSTER_KINDS,
  applyActivity,
  applyEvents,
  newChildParents,
  upsert,
} from "./agentRoster";
import type { Activities, Roster } from "./agentRoster";
import { ApiError } from "@/types/common";

async function listAll(
  ws: string,
  q: ListAgentsQuery = {},
  open?: string,
): Promise<Agent[]> {
  const out: Agent[] = [];
  let after = "";
  do {
    const page = await listAgents(ws, { ...q, after });
    out.push(...page.agents);
    after = page.next;
  } while (after);
  // List leaves archived agents out; the open chat's agent is fetched, so the
  // stream follows it and its unarchive shows its row again. A deleted or
  // missing one stays out.
  if (open && !out.some((a) => a.agent_id === open))
    out.push(
      ...(await getAgent(ws, open).then(
        (a) => (a.deleted_at ? [] : [a]),
        (err) => {
          if (err instanceof ApiError && err.status === 404) return [];
          throw err;
        },
      )),
    );
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

  // Each List in flight collects the stream's events that arrive while it
  // runs, and its result is the List with those events applied on top: a
  // List read before a replayed or live change never undoes it (RR1).
  const inflight = useRef(new Set<AgentEvent[]>());
  const list = useCallback(
    (
      q: ListAgentsQuery,
      merge: (r: Roster, agents: Agent[]) => Roster | null,
      open?: string,
    ) => {
      const seen: AgentEvent[] = [];
      inflight.current.add(seen);
      return listAll(workspaceId, q, open)
        .then((agents) =>
          setRoster((r) => {
            const next = merge(r, agents);
            return next ? applyEvents(next, seen) : r;
          }),
        )
        .finally(() => inflight.current.delete(seen));
    },
    [workspaceId],
  );

  // A full List replaces the roster, unless a later one already did.
  const sent = useRef(0);
  const done = useRef(0);
  const open = useRef(openId);
  const relist = useCallback(() => {
    const n = ++sent.current;
    list(
      {},
      (_, agents) => {
        if (n < done.current) return null;
        done.current = n;
        return upsert(new Map(), agents);
      },
      open.current,
    )
      .then(() => setError(null))
      .catch((err) => setError(message(err)));
  }, [list]);

  useEffect(() => {
    open.current = openId;
    relist();
  }, [relist, openId]);

  // The roster as of the last render, for the stream's starting cursors.
  const latest = useRef(roster);
  useEffect(() => {
    latest.current = roster;
    shared = roster;
    sharedActivity = activity;
    listeners.forEach((l) => l());
  }, [roster, activity]);

  // The stream reopens only when the set of agents changes.
  const ids = useMemo(() => [...roster.keys()].sort().join(","), [roster]);
  // The last stream's cache, so the next one opens at the cursors it
  // reached, and never before an agent's listed last_seq: the row already
  // shows everything up to it (RR1).
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
      from: Object.fromEntries(
        [...latest.current.values()].map((a) => [a.agent_id, a.last_seq ?? 0]),
      ),
      expired: purged ? purged.split(",") : [],
      onEvents: (added) => {
        inflight.current.forEach((seen) => seen.push(...added));
        setRoster((r) => applyEvents(r, added));
        setActivity((m) => applyActivity(m, added));
        for (const parent of newChildParents(added))
          list({ parent }, upsert).catch((err) => setError(message(err)));
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
  }, [workspaceId, ids, purged, relist, list]);

  return { roster, error };
}
