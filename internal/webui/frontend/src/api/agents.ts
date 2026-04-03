/**
 * Loom Agent API client.
 * Fetches agent status from the loom server.
 */

import type {
  LoomAgentStatus,
  LoomAgentsResponse,
  LoomStatusResponse,
  LoomTasksResponse,
  LoomTaskSummary,
  LoomTaskInfo,
  LoomTaskLists,
  LoomSyncInfo,
  LoomStats,
} from "@/types";
import { get, wsUrl } from "./client";

/**
 * Default loom server URL.
 * Can be overridden via environment variable or config.
 */
const LOOM_SERVER_URL = import.meta.env.VITE_LOOM_SERVER_URL ?? "/api/loom";

/**
 * Fetch agents from the loom server.
 * Throws on network errors or non-OK responses so callers can handle connection state.
 */
export async function fetchAgents(): Promise<LoomAgentStatus[]> {
  const data = await get<LoomAgentsResponse>(`${LOOM_SERVER_URL}/api/agents`, {
    timeout: 15000,
  });
  return data.agents ?? [];
}

/**
 * Fetch agents for a specific workspace by scanning its worktrees directory.
 * Returns agents scoped to the workspace (not the global loom server).
 */
export async function fetchWorkspaceAgents(
  workspaceId: string,
): Promise<LoomAgentStatus[]> {
  const data = await get<{ agents: LoomAgentStatus[] }>(
    wsUrl(workspaceId, "/agents"),
    { timeout: 10000 },
  );
  return data.agents ?? [];
}

/**
 * Check if the loom server is available.
 */
export async function checkLoomHealth(): Promise<boolean> {
  try {
    await get<unknown>(`${LOOM_SERVER_URL}/health`, { timeout: 15000 });
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
 * Fetch full status from the loom server.
 * Throws on network errors or invalid responses so callers can handle connection state.
 */
export async function fetchStatus(): Promise<FetchStatusResult> {
  const data = await get<LoomStatusResponse>(`${LOOM_SERVER_URL}/api/status`, {
    timeout: 15000,
  });
  return {
    agents: data.agents ?? [],
    tasks: data.tasks,
    agentTasks: data.agent_tasks ?? {},
    sync: data.sync,
    stats: data.stats,
    timestamp: data.timestamp,
  };
}

/**
 * Fetch task lists from the loom server.
 * Throws on network errors or invalid responses so callers can handle connection state.
 */
export async function fetchTasks(): Promise<LoomTaskLists> {
  const data = await get<LoomTasksResponse>(`${LOOM_SERVER_URL}/api/tasks`, {
    timeout: 15000,
  });
  return {
    needsPlanning: data.needs_planning ?? [],
    readyToImplement: data.ready_to_implement ?? [],
    needsReview: data.needs_review ?? [],
    inProgress: data.in_progress ?? [],
    backlog: data.backlog ?? [],
    done: data.closed ?? [],
  };
}
