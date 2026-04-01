/**
 * API client for per-workspace configuration endpoints.
 * Interfaces with PATCH /api/workspaces/{ws}/config/backend.
 */

import { patch, ApiError, wsUrl } from "./client";
import type { WorkspaceData } from "./workspace";

interface ApiSuccess<T> {
  success: true;
  data: T;
}

interface ApiFailure {
  success: false;
  error: string;
}

type ApiResult<T> = ApiSuccess<T> | ApiFailure;

/**
 * Update a workspace's AI backend. Returns the updated workspace data.
 * Callers are responsible for triggering a refetch on the workspace context.
 */
export async function updateWorkspaceBackend(
  workspaceId: string,
  backend: string,
): Promise<WorkspaceData> {
  const response = await patch<ApiResult<WorkspaceData>>(
    wsUrl(workspaceId, "/config/backend"),
    { backend },
  );
  if (!response.success) {
    throw new ApiError(0, response.error);
  }
  return response.data;
}
