/**
 * API functions for task log snapshot endpoints.
 */

import { api, ApiError, apiErrorFromResponse } from "@/api/common";

/**
 * Fetch available log phases for a task.
 * @param taskId The task ID (e.g., "TASK-123")
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
