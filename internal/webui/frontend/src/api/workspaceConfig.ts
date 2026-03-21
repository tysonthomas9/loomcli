/**
 * API client for per-workspace configuration endpoints.
 * Interfaces with PATCH /api/workspace/{name}/config/backend.
 */

import { patch, ApiError } from "./client";
import { refreshWorkspace, type WorkspaceData } from "./workspace";

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
 * Update a workspace's AI backend. On success, invalidates the workspace cache
 * and returns refreshed workspace data.
 */
export async function updateWorkspaceBackend(
  workspaceName: string,
  backend: string,
): Promise<WorkspaceData> {
  const response = await patch<ApiResult<WorkspaceData>>(
    `/api/workspace/${encodeURIComponent(workspaceName)}/config/backend`,
    { backend },
  );
  if (!response.success) {
    throw new ApiError(0, response.error);
  }
  // Refresh cache so subsequent reads pick up the new backend
  return refreshWorkspace();
}
