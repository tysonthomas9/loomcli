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
} from '@/types';

/**
 * Default loom server URL.
 * Can be overridden via environment variable or config.
 */
const LOOM_SERVER_URL = import.meta.env.VITE_LOOM_SERVER_URL ?? '/api/loom';
const LOOM_REQUEST_TIMEOUT_MS = 15000;

async function fetchWithTimeout(
  input: RequestInfo,
  init: RequestInit,
  timeoutMs = LOOM_REQUEST_TIMEOUT_MS
) {
  const controller = new AbortController();
  const timeoutId = window.setTimeout(() => controller.abort(), timeoutMs);

  try {
    return await fetch(input, { ...init, signal: controller.signal });
  } finally {
    window.clearTimeout(timeoutId);
  }
}

/**
 * Fetch agents from the loom server.
 * Returns an empty array if the server is unavailable.
 */
export async function fetchAgents(): Promise<LoomAgentStatus[]> {
  try {
    const response = await fetchWithTimeout(`${LOOM_SERVER_URL}/api/agents`, {
      method: 'GET',
      headers: {
        Accept: 'application/json',
      },
    });

    if (!response.ok) {
      console.warn(`Loom server returned ${response.status}: ${response.statusText}`);
      return [];
    }

    const data: LoomAgentsResponse = await response.json();
    return data.agents ?? [];
  } catch (error) {
    // Loom server not available - this is expected when not running agents
    console.warn(
      'Loom server not available:',
      error instanceof Error ? error.message : 'Unknown error'
    );
    return [];
  }
}

/**
 * Check if the loom server is available.
 */
export async function checkLoomHealth(): Promise<boolean> {
  try {
    const response = await fetchWithTimeout(`${LOOM_SERVER_URL}/health`, {
      method: 'GET',
      headers: {
        Accept: 'application/json',
      },
    });
    return response.ok;
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
  const response = await fetchWithTimeout(`${LOOM_SERVER_URL}/api/status`, {
    method: 'GET',
    headers: {
      Accept: 'application/json',
    },
  });

  if (!response.ok) {
    throw new Error(`Loom server returned ${response.status}: ${response.statusText}`);
  }

  const data: LoomStatusResponse = await response.json();
  // Map API field 'backlog' to frontend field 'blocked'
  return {
    agents: data.agents ?? [],
    tasks: {
      needs_planning: data.tasks.needs_planning,
      ready_to_implement: data.tasks.ready_to_implement,
      in_progress: data.tasks.in_progress,
      need_review: data.tasks.need_review,
      blocked: data.tasks.backlog, // Map backlog → blocked
    },
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
  const response = await fetchWithTimeout(`${LOOM_SERVER_URL}/api/tasks`, {
    method: 'GET',
    headers: {
      Accept: 'application/json',
    },
  });

  if (!response.ok) {
    throw new Error(`Loom server returned ${response.status}: ${response.statusText}`);
  }

  const data: LoomTasksResponse = await response.json();
  return {
    needsPlanning: data.needs_planning ?? [],
    readyToImplement: data.ready_to_implement ?? [],
    needsReview: data.needs_review ?? [],
    inProgress: data.in_progress ?? [],
    blocked: data.backlog ?? [], // Map API "backlog" to semantic "blocked"
  };
}
