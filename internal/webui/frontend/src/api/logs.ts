/**
 * API functions for log streaming endpoints.
 * Uses openapi-fetch generated client where types are available,
 * legacy fetch wrapper for untyped endpoints.
 */

import {
  api,
  ApiError,
  apiErrorFromResponse,
  get,
  wsUrl,
  getWsBaseUrl,
} from "./client";

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
    const { data, error, response } = await api.GET(
      "/api/workspaces/{ws}/tasks/{id}/logs",
      {
        params: { path: { ws: workspaceId, id: taskId } },
      },
    );
    if (error) throw apiErrorFromResponse(error, response);
    return data.data?.phases ?? [];
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
    const { data, error, response } = await api.GET(
      "/api/workspaces/{ws}/tasks/{id}/logs/{phase}",
      {
        params: {
          path: { ws: workspaceId, id: taskId, phase },
          query: { lines },
        },
      },
    );
    if (error) throw apiErrorFromResponse(error, response);
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
 * Uses legacy client (spec response is untyped).
 */
export async function getAgentTerminalInfo(
  workspaceId: string,
  agentName: string,
): Promise<"tmux" | "archive"> {
  const response = await get<{
    success: boolean;
    data?: { agent: string; mode: "tmux" | "archive" };
    error?: string;
  }>(
    wsUrl(
      workspaceId,
      `/agents/${encodeURIComponent(agentName)}/terminal/info`,
    ),
  );
  if (!response.success || !response.data) {
    throw new Error(response.error || "Failed to fetch agent terminal info");
  }
  return response.data.mode;
}

/**
 * Fetch one-time WebSocket auth token for agent terminal stream.
 */
export async function getAgentTerminalToken(
  workspaceId: string,
  agentName: string,
): Promise<string> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/agents/{name}/terminal/token",
    {
      params: { path: { ws: workspaceId, name: agentName } },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return data.token;
}

/**
 * Build WebSocket URL for agent terminal stream.
 */
export function getAgentTerminalWsUrl(
  workspaceId: string,
  agentName: string,
  token: string,
): string {
  const path = wsUrl(
    workspaceId,
    `/agents/${encodeURIComponent(agentName)}/terminal/ws`,
  );
  return `${getWsBaseUrl()}${path}?token=${encodeURIComponent(token)}`;
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
  try {
    const { data, error, response } = await api.GET(
      "/api/workspaces/{ws}/agents/{name}/logs",
      {
        params: {
          path: { ws: workspaceId, name: agentName },
          query:
            beforeLine !== undefined
              ? { lines, before_line: beforeLine }
              : { lines },
        },
      },
    );
    if (error) throw apiErrorFromResponse(error, response);
    if (!data.success || !data.data) {
      throw new Error(
        (data as { error?: string }).error ||
          "Failed to fetch agent log archive",
      );
    }
    return {
      lines: Array.isArray(data.data.lines) ? data.data.lines : [],
      lineCount:
        typeof data.data.line_count === "number" ? data.data.line_count : 0,
      startLine:
        typeof data.data.start_line === "number" ? data.data.start_line : 1,
    };
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      return { lines: [], lineCount: 0, startLine: 1 };
    }
    throw err;
  }
}
