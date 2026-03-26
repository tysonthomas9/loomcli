/**
 * API functions for log streaming endpoints.
 */

import { ApiError, get, wsUrl } from "./client";

/**
 * Response from GET /api/tasks/{id}/logs
 */
interface LogPhaseResponse {
  success: boolean;
  data: { phases: string[] };
}

interface TaskLogContentResponse {
  success: boolean;
  data?: {
    lines: string[];
    line_count: number;
  };
  error?: string;
}

interface AgentTerminalInfoResponse {
  success: boolean;
  data?: {
    agent: string;
    mode: "tmux" | "archive";
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
    start_line: number;
  };
  error?: string;
}

/**
 * Fetch available log phases for a task.
 * @param taskId The task ID (e.g., "beads-abc123")
 * @returns Array of available phases (e.g., ["planning", "implementation"])
 */
export async function getTaskLogPhases(
  workspaceId: string,
  taskId: string,
): Promise<string[]> {
  try {
    const data = await get<LogPhaseResponse>(
      wsUrl(workspaceId, `/tasks/${encodeURIComponent(taskId)}/logs`),
    );
    return data.data.phases;
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      return [];
    }
    throw err;
  }
}

/**
 * Fetch task log snapshot content for a single phase.
 */
export async function getTaskLogContent(
  workspaceId: string,
  taskId: string,
  phase: "planning" | "implementation",
  lines = 500,
): Promise<{ lines: string[]; lineCount: number }> {
  try {
    const data = await get<TaskLogContentResponse>(
      wsUrl(
        workspaceId,
        `/tasks/${encodeURIComponent(taskId)}/logs/${encodeURIComponent(phase)}?lines=${lines}`,
      ),
    );
    return {
      lines: Array.isArray(data.data?.lines) ? data.data.lines : [],
      lineCount:
        typeof data.data?.line_count === "number" ? data.data.line_count : 0,
    };
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      return { lines: [], lineCount: 0 };
    }
    throw err;
  }
}

/**
 * Fetch whether agent logs should use live tmux streaming or archive fallback.
 */
// TODO(workspace-routing): migrate to wsUrl when workspace-scoped route lands
export async function getAgentTerminalInfo(
  _workspaceId: string,
  agentName: string,
): Promise<"tmux" | "archive"> {
  const response = await get<AgentTerminalInfoResponse>(
    `/api/agents/${encodeURIComponent(agentName)}/terminal/info`,
  );
  if (!response.success || !response.data) {
    throw new Error(response.error || "Failed to fetch agent terminal info");
  }
  return response.data.mode;
}

/**
 * Fetch one-time WebSocket auth token for agent terminal stream.
 */
// TODO(workspace-routing): migrate to wsUrl when workspace-scoped route lands
export async function getAgentTerminalToken(
  _workspaceId: string,
  agentName: string,
): Promise<string> {
  const response = await get<AgentTerminalTokenResponse>(
    `/api/agents/${encodeURIComponent(agentName)}/terminal/token`,
  );
  if (!response.success || !response.data) {
    throw new Error(response.error || "Failed to fetch agent terminal token");
  }
  return response.data.token;
}

/**
 * Build WebSocket URL for agent terminal stream.
 */
// TODO(workspace-routing): migrate to wsUrl when workspace-scoped route lands
export function getAgentTerminalWsUrl(
  _workspaceId: string,
  agentName: string,
  token: string,
): string {
  const location =
    typeof window !== "undefined"
      ? window.location
      : (globalThis as { location?: Location }).location;
  const proto = location?.protocol === "https:" ? "wss:" : "ws:";
  const host = location?.host || "localhost";
  return `${proto}//${host}/api/agents/${encodeURIComponent(agentName)}/terminal/ws?token=${encodeURIComponent(token)}`;
}

/**
 * Read static archive log lines for an agent.
 * When beforeLine is provided, reads lines ending before that line number
 * (for paginated backward scrolling).
 */
export async function getAgentLogArchive(
  workspaceId: string,
  agentName: string,
  lines = 500,
  beforeLine?: number,
): Promise<{ lines: string[]; lineCount: number; startLine: number }> {
  let url = wsUrl(
    workspaceId,
    `/agents/${encodeURIComponent(agentName)}/logs?lines=${lines}`,
  );
  if (beforeLine !== undefined) {
    url += `&before_line=${beforeLine}`;
  }
  let response: AgentLogContentResponse;
  try {
    response = await get<AgentLogContentResponse>(url);
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      return { lines: [], lineCount: 0, startLine: 1 };
    }
    throw err;
  }
  if (!response.success || !response.data) {
    throw new Error(response.error || "Failed to fetch agent log archive");
  }
  return {
    lines: Array.isArray(response.data.lines) ? response.data.lines : [],
    lineCount:
      typeof response.data.line_count === "number"
        ? response.data.line_count
        : 0,
    startLine:
      typeof response.data.start_line === "number"
        ? response.data.start_line
        : 1,
  };
}
