/**
 * Loom Agent API client.
 * Uses openapi-fetch generated client (monitor endpoints are in the spec).
 */

import type {
  LoomAgentStatus,
  LoomTaskSummary,
  LoomTaskInfo,
  LoomTaskLists,
  LoomSyncInfo,
  LoomStats,
} from "@/types";
import { api, apiErrorFromResponse, get } from "@/api/common";
import {
  DEFAULT_TASKS,
  DEFAULT_SYNC,
  DEFAULT_STATS,
  DEFAULT_TASK_LISTS,
} from "./defaults";

/**
 * Fetch agents for a specific workspace. Empty or unknown wsID returns an
 * empty list rather than firing a doomed /api/workspaces//monitor/agents
 * request — matches fetchStatus / fetchTasks. Callers without an active
 * workspace must handle the empty case themselves.
 *
 * Optional `opts.signal` is merged with the 15s timeout via AbortSignal.any
 * so callers (e.g. agentStore) can cancel in-flight requests from a disposer
 * while the timeout still applies.
 *
 * Throws on network errors or non-OK responses so callers can handle
 * connection state.
 */
export async function fetchAgents(
  wsID: string,
  opts?: { signal?: AbortSignal },
): Promise<LoomAgentStatus[]> {
  if (wsID === "") {
    return [];
  }
  const signal = opts?.signal
    ? AbortSignal.any([opts.signal, AbortSignal.timeout(15000)])
    : AbortSignal.timeout(15000);
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/monitor/agents",
    {
      params: { path: { ws: wsID } },
      signal,
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return (data!.agents ?? []) as unknown as LoomAgentStatus[];
}

/**
 * Check if the loom server is available.
 */
export async function checkLoomHealth(): Promise<boolean> {
  try {
    await get<unknown>("/health", { timeout: 15000 });
    return true;
  } catch {
    return false;
  }
}

/**
 * Fetched status result type.
 */
export interface FetchStatusResult {
  agents: LoomAgentStatus[];
  tasks: LoomTaskSummary;
  agentTasks: Record<string, LoomTaskInfo>;
  sync: LoomSyncInfo;
  stats: LoomStats;
  timestamp: string;
}

/**
 * Fetch full status for a specific workspace. Empty or unknown wsID returns
 * empty defaults rather than falling back to the launch workspace — callers
 * that don't yet have an active workspace ID must handle the empty case
 * themselves (matches fetchAgents).
 *
 * Throws on network errors or invalid responses so callers can handle
 * connection state.
 */
export async function fetchStatus(
  wsID: string,
  opts?: { signal?: AbortSignal },
): Promise<FetchStatusResult> {
  if (wsID === "") {
    return emptyStatus();
  }
  const signal = opts?.signal
    ? AbortSignal.any([opts.signal, AbortSignal.timeout(15000)])
    : AbortSignal.timeout(15000);
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/monitor/status",
    {
      params: { path: { ws: wsID } },
      signal,
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  const d = data!;
  return {
    agents: (d.agents ?? []) as unknown as LoomAgentStatus[],
    tasks: d.tasks as unknown as LoomTaskSummary,
    agentTasks: (d.agent_tasks ?? {}) as unknown as Record<
      string,
      LoomTaskInfo
    >,
    sync: d.sync as unknown as LoomSyncInfo,
    stats: d.stats as unknown as LoomStats,
    timestamp: d.timestamp,
  };
}

/**
 * Fetch task lists for a specific workspace. Same empty-wsID semantics as
 * fetchStatus — the old behavior of falling through to the unscoped
 * /api/monitor/tasks leaked the launch workspace's queue into every sidebar.
 */
export async function fetchTasks(
  wsID: string,
  opts?: { signal?: AbortSignal },
): Promise<LoomTaskLists> {
  if (wsID === "") {
    return emptyTaskLists();
  }
  const signal = opts?.signal
    ? AbortSignal.any([opts.signal, AbortSignal.timeout(15000)])
    : AbortSignal.timeout(15000);
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/monitor/tasks",
    {
      params: { path: { ws: wsID } },
      signal,
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  const d = data!;
  return {
    needsPlanning: d.needs_planning ?? [],
    readyToImplement: d.ready_to_implement ?? [],
    needsReview: d.needs_review ?? [],
    inProgress: d.in_progress ?? [],
    backlog: d.backlog ?? [],
    done: d.closed ?? [],
  } as unknown as LoomTaskLists;
}

function emptyStatus(): FetchStatusResult {
  return {
    agents: [],
    tasks: DEFAULT_TASKS,
    agentTasks: {},
    sync: DEFAULT_SYNC,
    stats: DEFAULT_STATS,
    timestamp: "",
  };
}

function emptyTaskLists(): LoomTaskLists {
  return DEFAULT_TASK_LISTS;
}
