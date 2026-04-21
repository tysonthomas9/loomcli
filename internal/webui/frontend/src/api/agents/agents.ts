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

/**
 * Fetch agents for a specific workspace. Returns only agents belonging to
 * that workspace — if wsID is empty or unknown the server returns an empty
 * list rather than leaking another workspace's agents. The old
 * /api/monitor/agents (un-scoped) endpoint remains for the cross-workspace
 * monitor dashboard; per-workspace views must pass wsID here.
 *
 * Throws on network errors or non-OK responses so callers can handle
 * connection state.
 */
export async function fetchAgents(wsID: string): Promise<LoomAgentStatus[]> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/monitor/agents",
    {
      params: { path: { ws: wsID } },
      signal: AbortSignal.timeout(15000),
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
export async function fetchStatus(wsID: string): Promise<FetchStatusResult> {
  if (wsID === "") {
    return emptyStatus();
  }
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/monitor/status",
    {
      params: { path: { ws: wsID } },
      signal: AbortSignal.timeout(15000),
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
export async function fetchTasks(wsID: string): Promise<LoomTaskLists> {
  if (wsID === "") {
    return emptyTaskLists();
  }
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/monitor/tasks",
    {
      params: { path: { ws: wsID } },
      signal: AbortSignal.timeout(15000),
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

// emptyStatus matches FetchStatusResult's shape with zero-valued fields so
// callers with an empty wsID can treat the return identically to a
// successful fetch against an empty workspace.
function emptyStatus(): FetchStatusResult {
  return {
    agents: [],
    tasks: {
      needs_planning: 0,
      ready_to_implement: 0,
      in_progress: 0,
      need_review: 0,
      backlog: 0,
      epics: 0,
    } as unknown as LoomTaskSummary,
    agentTasks: {},
    sync: {
      db_synced: true,
      db_last_sync: "",
      git_needs_push: 0,
      git_needs_pull: 0,
    } as unknown as LoomSyncInfo,
    stats: {
      open: 0,
      closed: 0,
      total: 0,
      completion: 0,
      remaining: 0,
      in_progress: 0,
      review: 0,
      blocked: 0,
    } as unknown as LoomStats,
    timestamp: "",
  };
}

function emptyTaskLists(): LoomTaskLists {
  return {
    needsPlanning: [],
    readyToImplement: [],
    needsReview: [],
    inProgress: [],
    backlog: [],
    done: [],
  } as unknown as LoomTaskLists;
}
