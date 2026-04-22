/**
 * API client for per-workspace configuration endpoints.
 * Uses openapi-fetch generated client.
 */

import { api, ApiError, apiErrorFromResponse, patch } from "@/api/common";
import type { WorkspaceData } from "./workspace";

/**
 * Update a workspace's AI backend. Returns the updated workspace data.
 * Callers are responsible for triggering a refetch on the workspace context.
 */
export async function updateWorkspaceBackend(
  workspaceId: string,
  backend: string,
): Promise<WorkspaceData> {
  const { data, error, response } = await api.PATCH(
    "/api/workspaces/{ws}/config/backend",
    {
      params: { path: { ws: workspaceId } },
      body: { backend },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  // The response is a MessageResponse, refetch workspace data
  if (data && typeof data === "object" && "success" in data) {
    const msg = data as { success: boolean; message?: string };
    if (!msg.success) {
      throw new ApiError(0, msg.message ?? "Unknown error");
    }
  }
  // Refetch full workspace data after backend update
  const { fetchWorkspaceApi } = await import("./workspace");
  return fetchWorkspaceApi(workspaceId);
}

/**
 * Update a repo's default_branch within a workspace. Returns the refreshed
 * workspace data wrapped by the server in WorkspaceResponse.
 */
export async function updateRepoDefaultBranch(
  workspaceId: string,
  repoName: string,
  branch: string,
): Promise<WorkspaceData> {
  const response = await patch<
    { success: true; data: WorkspaceData } | { success: false; error: string }
  >(
    `/api/workspaces/${encodeURIComponent(workspaceId)}/repos/${encodeURIComponent(repoName)}/default-branch`,
    { branch },
  );
  if (!response.success) {
    throw new ApiError(0, response.error);
  }
  return response.data;
}
