/**
 * API client for workspace endpoint with module-level caching.
 * Interfaces with GET /api/workspace endpoint.
 */

import { get, post, patch, put, del, ApiError } from "./client";
import { createIssue } from "./issues";
import type { Issue } from "@/types";

// ============= Types =============

export interface RepoInfo {
  name: string;
  path: string;
  default_branch: string;
  remote: string;
  source_repo_id?: string;
  groups: string[];
}

export interface WorkspaceAgentInfo {
  name: string;
  repos: string[];
  repo_groups: string[];
  cross_repo: boolean;
}

export interface WorkspaceSummary {
  id: string;
  name: string;
  path: string;
  active: boolean;
  repo_count: number;
  is_default: boolean;
  backend?: string;
}

export interface WorkspaceData {
  id: string;
  name: string;
  path: string;
  repos: RepoInfo[];
  groups: string[];
  agents: WorkspaceAgentInfo[];
  workspaces: WorkspaceSummary[];
  workspace_order?: string[];
  default_workspace: string;
}

// ============= Response Types =============

interface ApiSuccess<T> {
  success: true;
  data: T;
}

interface ApiFailure {
  success: false;
  error: string;
}

type ApiResult<T> = ApiSuccess<T> | ApiFailure;

// ============= Module-level Cache =============

let workspaceCache: WorkspaceData | null = null;
let fetchPromise: Promise<WorkspaceData> | null = null;
let cacheGeneration = 0; // incremented on refresh to discard stale in-flight responses

// ============= Helpers =============

function unwrap<T>(response: ApiResult<T>): T {
  if (!response.success) {
    throw new ApiError(0, response.error);
  }
  return response.data;
}

// ============= API Functions =============

/**
 * Fetch workspace data. Returns cached data if available.
 * Deduplicates concurrent in-flight requests.
 */
export async function fetchWorkspace(
  workspaceId?: string,
): Promise<WorkspaceData> {
  if (workspaceCache !== null) {
    return workspaceCache;
  }
  if (fetchPromise !== null) {
    return fetchPromise;
  }
  const gen = cacheGeneration;
  const path = workspaceId
    ? `/api/workspaces/${encodeURIComponent(workspaceId)}`
    : "/api/workspace";
  fetchPromise = get<ApiResult<WorkspaceData>>(path).then(
    (response) => {
      // Discard stale response if a refresh happened while we were in-flight
      if (gen !== cacheGeneration) {
        return fetchWorkspace();
      }
      workspaceCache = unwrap(response);
      fetchPromise = null;
      return workspaceCache;
    },
    (err) => {
      if (gen === cacheGeneration) {
        fetchPromise = null;
      }
      throw err;
    },
  );
  return fetchPromise;
}

/**
 * Invalidate the workspace cache without triggering a refetch.
 * Call during workspace switch to prevent stale data from the old workspace
 * being served to the new workspace's components on mount.
 */
export function invalidateWorkspaceCache(): void {
  cacheGeneration++;
  workspaceCache = null;
  fetchPromise = null;
}

/**
 * Invalidate the cache and re-fetch workspace data from the backend.
 */
export async function refreshWorkspace(): Promise<WorkspaceData> {
  cacheGeneration++; // Invalidate any in-flight fetch from prior generation
  workspaceCache = null;
  fetchPromise = null;
  return fetchWorkspace();
}

/**
 * Rename a workspace. On success, invalidates the cache and returns refreshed data.
 */
export async function renameWorkspace(
  oldName: string,
  newName: string,
): Promise<WorkspaceData> {
  const response = await patch<ApiResult<WorkspaceData>>(
    "/api/workspace/rename",
    { old_name: oldName, new_name: newName },
  );
  const data = unwrap(response);
  if (data == null) {
    // Same-name no-op: return current cache or re-fetch
    return workspaceCache ?? (await refreshWorkspace());
  }
  // Refresh cache with the returned data
  cacheGeneration++;
  workspaceCache = data;
  fetchPromise = null;
  return data;
}

/**
 * Delete a workspace by name. On success, invalidates the cache and returns refreshed data.
 */
export async function deleteWorkspace(
  name: string,
): Promise<WorkspaceData | null> {
  const response = await del<ApiResult<WorkspaceData>>(
    `/api/workspace/${encodeURIComponent(name)}`,
  );
  if (!response.success) {
    throw new ApiError(0, response.error);
  }
  // Refresh cache with the returned data
  cacheGeneration++;
  workspaceCache = response.data ?? null;
  fetchPromise = null;
  return workspaceCache;
}

/**
 * Reorder workspaces. On success, invalidates the cache and returns refreshed data.
 */
export async function reorderWorkspaces(
  order: string[],
): Promise<WorkspaceData> {
  const response = await put<ApiResult<WorkspaceData>>("/api/workspace/order", {
    order,
  });
  const data = unwrap(response);
  // Refresh cache with the returned data
  cacheGeneration++;
  workspaceCache = data;
  fetchPromise = null;
  return data;
}

/**
 * Set the default workspace. On success, invalidates cache and returns refreshed data.
 */
export async function setDefaultWorkspace(
  name: string,
): Promise<WorkspaceData> {
  const response = await put<ApiResult<WorkspaceData>>(
    "/api/workspace/default",
    { name },
  );
  const data = unwrap(response);
  cacheGeneration++;
  if (data) {
    workspaceCache = data;
  }
  fetchPromise = null;
  return workspaceCache ?? (await refreshWorkspace());
}

/**
 * Clear the default workspace. On success, invalidates cache and returns refreshed data.
 */
export async function clearDefaultWorkspace(): Promise<WorkspaceData> {
  const response = await del<ApiResult<WorkspaceData>>(
    "/api/workspace/default",
  );
  if (!response.success) {
    throw new ApiError(0, response.error);
  }
  cacheGeneration++;
  if (response.data) {
    workspaceCache = response.data;
  }
  fetchPromise = null;
  return workspaceCache ?? (await refreshWorkspace());
}

// ============= Workspace Creation =============

export interface CreateWorkspaceRequest {
  name: string;
  type: "empty" | "clone" | "template";
  repos?: string[];
  clone_url?: string;
  clone_urls?: string[];
  branch?: string;
  path?: string;
}

/**
 * Create a new workspace. On success, invalidates the cache and returns refreshed data.
 */
export async function createWorkspace(
  req: CreateWorkspaceRequest,
): Promise<WorkspaceData> {
  const response = await post<ApiResult<WorkspaceData>>(
    "/api/workspace/create",
    req,
  );
  const data = unwrap(response);
  cacheGeneration++;
  if (data) {
    workspaceCache = data;
  }
  fetchPromise = null;
  return workspaceCache ?? (await refreshWorkspace());
}

/**
 * Synchronous getter for current cache state.
 * Returns null if not yet fetched, or the cached workspace data.
 */
export function getCachedWorkspace(): WorkspaceData | null {
  return workspaceCache;
}

// ============= Workspace Issue Helpers =============

/**
 * Create a task under an epic with sensible defaults.
 */
export async function createWorkspaceTask(
  workspaceId: string,
  epicId: string,
  title: string,
): Promise<Issue> {
  return createIssue(workspaceId, {
    title,
    issue_type: "task",
    priority: 3,
    parent: epicId,
  });
}

/**
 * Create an epic with sensible defaults.
 * Note: workspace association via source_repos is handled at the backend level.
 */
export async function createWorkspaceEpic(
  workspaceId: string,
  title: string,
): Promise<Issue> {
  return createIssue(workspaceId, {
    title,
    issue_type: "epic",
    priority: 2,
  });
}
