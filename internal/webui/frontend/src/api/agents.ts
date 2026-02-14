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
import { getAuthToken } from './client';

/**
 * Default loom server URL.
 * Can be overridden via environment variable or config.
 */
const LOOM_SERVER_URL = import.meta.env.VITE_LOOM_SERVER_URL ?? '/api/loom';
const LOOM_REQUEST_TIMEOUT_MS = 15000;

function buildLoomHeaders(): Record<string, string> {
  const headers: Record<string, string> = {
    Accept: 'application/json',
  };
  const token = getAuthToken();
  if (token) {
    headers.Authorization = `Bearer ${token}`;
  }
  return headers;
}

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
 * Throws on network errors or non-OK responses so callers can handle connection state.
 */
export async function fetchAgents(): Promise<LoomAgentStatus[]> {
  const response = await fetchWithTimeout(`${LOOM_SERVER_URL}/api/agents`, {
    method: 'GET',
    headers: buildLoomHeaders(),
  });

  if (!response.ok) {
    throw new Error(`Loom agents: ${response.status} ${response.statusText}`);
  }

  const data: LoomAgentsResponse = await response.json();
  return data.agents ?? [];
}

/**
 * Check if the loom server is available.
 */
export async function checkLoomHealth(): Promise<boolean> {
  try {
    const response = await fetchWithTimeout(`${LOOM_SERVER_URL}/health`, {
      method: 'GET',
      headers: buildLoomHeaders(),
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
    headers: buildLoomHeaders(),
  });

  if (!response.ok) {
    throw new Error(`Loom server returned ${response.status}: ${response.statusText}`);
  }

  const data: LoomStatusResponse = await response.json();
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
  const response = await fetchWithTimeout(`${LOOM_SERVER_URL}/api/tasks`, {
    method: 'GET',
    headers: buildLoomHeaders(),
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
    backlog: data.backlog ?? [],
  };
}
