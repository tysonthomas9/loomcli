/**
 * API client for workspace endpoint with module-level caching.
 * Interfaces with GET /api/workspace endpoint.
 */

import { get, patch, ApiError } from "./client";

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
  name: string;
  path: string;
  active: boolean;
  repo_count: number;
}

export interface WorkspaceData {
  name: string;
  path: string;
  repos: RepoInfo[];
  groups: string[];
  agents: WorkspaceAgentInfo[];
  workspaces: WorkspaceSummary[];
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
export async function fetchWorkspace(): Promise<WorkspaceData> {
  if (workspaceCache !== null) {
    return workspaceCache;
  }
  if (fetchPromise !== null) {
    return fetchPromise;
  }
  const gen = cacheGeneration;
  fetchPromise = get<ApiResult<WorkspaceData>>("/api/workspace").then(
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
 * Synchronous getter for current cache state.
 * Returns null if not yet fetched, or the cached workspace data.
 */
export function getCachedWorkspace(): WorkspaceData | null {
  return workspaceCache;
}
