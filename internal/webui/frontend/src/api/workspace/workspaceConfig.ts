/**
 * API client for per-workspace configuration endpoints.
 * Uses openapi-fetch generated client.
 */

import {
  api,
  ApiError,
  apiErrorFromResponse,
  del,
  patch,
  post,
} from "@/api/common";
import type { WorkspaceData } from "./workspace";

/**
 * Body for POST /api/workspaces/{ws}/repos. The path is required; the name
 * defaults to the directory's basename if omitted.
 */
export type AddRepoRequest = {
  name?: string;
  path: string;
  default_branch?: string;
  remote?: string;
  groups?: string[];
};

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

/**
 * Add a new repo entry to a workspace. Returns the refreshed workspace data.
 * The server validates that the path exists, is a directory, and contains .git.
 */
export async function addRepoToWorkspace(
  workspaceId: string,
  repo: AddRepoRequest,
): Promise<WorkspaceData> {
  const response = await post<
    { success: true; data: WorkspaceData } | { success: false; error: string }
  >(`/api/workspaces/${encodeURIComponent(workspaceId)}/repos`, repo);
  if (!response.success) {
    throw new ApiError(0, response.error);
  }
  return response.data;
}

/**
 * Remove a repo entry from a workspace. Does NOT delete files on disk or
 * run git worktree remove. Returns the refreshed workspace data.
 */
export async function removeRepoFromWorkspace(
  workspaceId: string,
  repoName: string,
): Promise<WorkspaceData> {
  const response = await del<
    { success: true; data: WorkspaceData } | { success: false; error: string }
  >(
    `/api/workspaces/${encodeURIComponent(workspaceId)}/repos/${encodeURIComponent(repoName)}`,
  );
  if (!response.success) {
    throw new ApiError(0, response.error);
  }
  return response.data;
}
