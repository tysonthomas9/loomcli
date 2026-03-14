/**
 * API client for workspace endpoint with module-level caching.
 * Interfaces with GET /api/workspace endpoint.
 */

import { get, ApiError } from "./client";

// ============= Types =============

export interface RepoInfo {
  name: string;
  path: string;
  default_branch: string;
  remote: string;
  source_repo_id?: string;
  groups?: string[];
}

export interface WorkspaceAgentInfo {
  name: string;
  repos: string[];
  repo_groups: string[];
  cross_repo: boolean;
}

export interface WorkspaceData {
  name: string;
  path: string;
  repos: RepoInfo[];
  groups?: string[];
  agents?: WorkspaceAgentInfo[];
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
  fetchPromise = get<ApiResult<WorkspaceData>>("/api/workspace").then(
    (response) => {
      workspaceCache = unwrap(response);
      fetchPromise = null;
      return workspaceCache;
    },
    (err) => {
      fetchPromise = null;
      throw err;
    },
  );
  return fetchPromise;
}

/**
 * Invalidate the cache and re-fetch workspace data from the backend.
 */
export async function refreshWorkspace(): Promise<WorkspaceData> {
  workspaceCache = null;
  // Do not reset fetchPromise — deduplication still applies for concurrent callers.
  return fetchWorkspace();
}

/**
 * Synchronous getter for current cache state.
 * Returns null if not yet fetched, or the cached workspace data.
 */
export function getCachedWorkspace(): WorkspaceData | null {
  return workspaceCache;
}
