/**
 * API functions for log streaming endpoints.
 */

import { get, getAuthToken } from './client';

/**
 * Response from GET /api/tasks/{id}/logs
 */
interface LogPhaseResponse {
  success: boolean;
  data: { phases: string[] };
}

interface AgentTerminalInfoResponse {
  success: boolean;
  data?: {
    agent: string;
    mode: 'tmux' | 'archive';
  };
  error?: string;
}

interface AgentTerminalTokenResponse {
  success: boolean;
  data?: {
    token: string;
  };
  error?: string;
}

interface AgentLogContentResponse {
  success: boolean;
  data?: {
    lines: string[];
    line_count: number;
  };
  error?: string;
}

/**
 * Append auth token to a URL as a query parameter if available.
 */
function appendToken(url: string): string {
  const token = getAuthToken();
  if (!token) return url;
  const sep = url.includes('?') ? '&' : '?';
  return `${url}${sep}token=${encodeURIComponent(token)}`;
}

/**
 * Fetch available log phases for a task.
 * @param taskId The task ID (e.g., "beads-abc123")
 * @returns Array of available phases (e.g., ["planning", "implementation"])
 */
export async function getTaskLogPhases(taskId: string): Promise<string[]> {
  const headers: Record<string, string> = {};
  const token = getAuthToken();
  if (token) {
    headers['Authorization'] = `Bearer ${token}`;
  }

  const response = await fetch(`/api/tasks/${encodeURIComponent(taskId)}/logs`, { headers });

  if (!response.ok) {
    if (response.status === 404) {
      // No logs yet for this task
      return [];
    }
    throw new Error('Failed to fetch log phases');
  }

  const data: LogPhaseResponse = await response.json();
  return data.data.phases;
}

/**
 * Fetch whether agent logs should use live tmux streaming or archive fallback.
 */
export async function getAgentTerminalInfo(agentName: string): Promise<'tmux' | 'archive'> {
  const response = await get<AgentTerminalInfoResponse>(
    `/api/agents/${encodeURIComponent(agentName)}/terminal/info`
  );
  if (!response.success || !response.data) {
    throw new Error(response.error || 'Failed to fetch agent terminal info');
  }
  return response.data.mode;
}

/**
 * Fetch one-time WebSocket auth token for agent terminal stream.
 */
export async function getAgentTerminalToken(agentName: string): Promise<string> {
  const response = await get<AgentTerminalTokenResponse>(
    `/api/agents/${encodeURIComponent(agentName)}/terminal/token`
  );
  if (!response.success || !response.data) {
    throw new Error(response.error || 'Failed to fetch agent terminal token');
  }
  return response.data.token;
}

/**
 * Build WebSocket URL for agent terminal stream.
 */
export function getAgentTerminalWsUrl(agentName: string, token: string): string {
  const location =
    typeof window !== 'undefined'
      ? window.location
      : (globalThis as { location?: Location }).location;
  const proto = location?.protocol === 'https:' ? 'wss:' : 'ws:';
  const host = location?.host || 'localhost';
  return `${proto}//${host}/api/agents/${encodeURIComponent(agentName)}/terminal/ws?token=${encodeURIComponent(token)}`;
}

/**
 * Read static archive log lines for an agent.
 */
export async function getAgentLogArchive(
  agentName: string,
  lines = 500
): Promise<{ lines: string[]; lineCount: number }> {
  const response = await get<AgentLogContentResponse>(
    `/api/agents/${encodeURIComponent(agentName)}/logs?lines=${lines}`
  );
  if (!response.success || !response.data) {
    throw new Error(response.error || 'Failed to fetch agent log archive');
  }
  return {
    lines: Array.isArray(response.data.lines) ? response.data.lines : [],
    lineCount: typeof response.data.line_count === 'number' ? response.data.line_count : 0,
  };
}

/**
 * Get the SSE URL for task log streaming.
 * @param taskId The task ID (e.g., "beads-abc123")
 * @param phase The log phase ("planning" or "implementation")
 * @returns The SSE endpoint URL
 */
export function getTaskLogStreamUrl(taskId: string, phase: string): string {
  return appendToken(`/api/tasks/${encodeURIComponent(taskId)}/logs/${encodeURIComponent(phase)}/stream`);
}
