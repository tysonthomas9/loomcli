/**
 * API client for workspace endpoints.
 * Uses openapi-fetch for typed endpoints and raw fetch where the spec diverges.
 */

import {
  api,
  apiErrorFromResponse,
  get,
  post,
  patch,
  put,
  del,
  ApiError,
} from "@/api/common";
import { createIssue } from "@/api/issues";
import type { Issue } from "@/types";

// ============= Types =============

export interface RepoInfo {
  name: string;
  path: string;
  default_branch: string;
  current_branch?: string;
  remote: string;
  source_repo_id?: string;
  groups: string[];
  is_linked_worktree?: boolean;
}

export interface WorkspaceAgentInfo {
  name: string;
  repos: string[];
  repo_groups: string[];
  cross_repo: boolean;
}

export interface CreateAgentRequest {
  name: string;
  role_name: string;
  auto?: boolean;
  backend?: string;
  repos?: string[];
  repo_groups?: string[];
  cross_repo?: boolean;
}

export type WorkspaceLifecycleState =
  | "creating"
  | "cloning"
  | "initializing"
  | "ready"
  | "error";

export interface WorkspaceSummary {
  id: string;
  name: string;
  path: string;
  active: boolean;
  repo_count: number;
  is_default: boolean;
  backend?: string;
  state?: WorkspaceLifecycleState;
  error_message?: string;
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
  warnings?: string[];
}

interface ApiFailure {
  success: false;
  error: string;
}

type ApiResult<T> = ApiSuccess<T> | ApiFailure;

function unwrap<T>(response: ApiResult<T>): T {
  if (!response.success) {
    throw new ApiError(0, response.error);
  }
  return response.data;
}

// ============= API Functions =============

/**
 * Fetch workspace data from the API. Always hits the network.
 */
export async function fetchWorkspaceApi(
  workspaceId?: string,
): Promise<WorkspaceData> {
  if (workspaceId) {
    const { data, error, response } = await api.GET("/api/workspaces/{ws}", {
      params: { path: { ws: workspaceId } },
    });
    if (error) throw apiErrorFromResponse(error, response);
    return unwrap(data as unknown as ApiResult<WorkspaceData>);
  }
  const { data, error, response } = await api.GET("/api/workspaces/active");
  if (error) throw apiErrorFromResponse(error, response);
  return unwrap(data as unknown as ApiResult<WorkspaceData>);
}

/**
 * Rename a workspace by ID. Returns the updated workspace data.
 */
export async function renameWorkspace(
  workspaceId: string,
  newName: string,
): Promise<WorkspaceData> {
  const response = await patch<ApiResult<WorkspaceData>>(
    `/api/workspaces/${encodeURIComponent(workspaceId)}/name`,
    { new_name: newName },
  );
  const data = unwrap(response);
  if (data == null) {
    return await fetchWorkspaceApi(workspaceId);
  }
  return data;
}

/**
 * Delete a workspace by ID.
 */
export async function deleteWorkspace(
  workspaceId: string,
): Promise<WorkspaceData | null> {
  const response = await del<ApiResult<WorkspaceData>>(
    `/api/workspaces/${encodeURIComponent(workspaceId)}`,
  );
  if (!response.success) {
    throw new ApiError(0, response.error);
  }
  return response.data;
}

/**
 * Reorder workspaces.
 */
export async function reorderWorkspaces(
  order: string[],
): Promise<WorkspaceData> {
  const response = await put<ApiResult<WorkspaceData>>(
    "/api/workspaces/order",
    { order },
  );
  return unwrap(response);
}

/**
 * Deprecated: default workspace selection has been removed.
 */
export async function setDefaultWorkspace(
  name: string,
): Promise<WorkspaceData> {
  const response = await put<ApiResult<WorkspaceData>>(
    "/api/workspaces/default",
    { name },
  );
  return unwrap(response);
}

/**
 * Deprecated: default workspace selection has been removed.
 */
export async function clearDefaultWorkspace(): Promise<WorkspaceData> {
  const response = await del<ApiResult<WorkspaceData>>(
    "/api/workspaces/default",
  );
  return unwrap(response);
}

// ============= Workspace Creation =============

export interface CreateWorkspaceRequest {
  name: string;
  type: "empty" | "clone" | "template";
  repos?: string[];
  clone_urls?: string[];
  branch?: string;
  path?: string;
}

export interface AddWorkspaceReposRequest {
  repos: string[];
  branch?: string;
}

/** 201 sync result: workspace was created immediately. */
export interface WorkspaceCreateSync {
  kind: "sync";
  data: WorkspaceData;
  warnings?: string[];
}

/** 202 async result: clone job was started, poll for progress. */
export interface WorkspaceCreateAsync {
  kind: "async";
  jobId: string;
}

export type WorkspaceCreateResult = WorkspaceCreateSync | WorkspaceCreateAsync;

/** Shape returned by the backend for 202 Accepted (async clone). */
interface WorkspaceJobAcceptedResponse {
  success: boolean;
  job_id: string;
}

/** Timeout for clone requests (5 minutes) to cover the initial POST. */
const CLONE_CREATE_TIMEOUT = 300_000;

/**
 * Create a new workspace.
 *
 * Returns a discriminated union:
 *  - `{ kind: "sync", data }` for 201 (empty workspaces) — cache is updated.
 *  - `{ kind: "async", jobId }` for 202 (clone workspaces) — caller should poll.
 */
export async function createWorkspace(
  req: CreateWorkspaceRequest,
): Promise<WorkspaceCreateResult> {
  const options = req.type === "clone" ? { timeout: CLONE_CREATE_TIMEOUT } : {};

  const response = await post<
    ApiResult<WorkspaceData> | WorkspaceJobAcceptedResponse
  >("/api/workspaces", req, options);

  // Async path: backend returned 202 with { success, job_id }.
  if ("job_id" in response && typeof response.job_id === "string") {
    return { kind: "async", jobId: response.job_id };
  }

  // Sync path: backend returned 201 with { success, data }.
  const syncResponse = response as ApiResult<WorkspaceData>;
  const data = unwrap(syncResponse);
  const warnings = syncResponse.success ? syncResponse.warnings : undefined;
  const result: WorkspaceCreateSync = {
    kind: "sync",
    data,
  };
  if (warnings && warnings.length > 0) {
    result.warnings = warnings;
  }
  return result;
}

export async function addWorkspaceRepos(
  workspaceId: string,
  req: AddWorkspaceReposRequest,
): Promise<WorkspaceData> {
  const response = await post<ApiResult<WorkspaceData>>(
    `/api/workspaces/${encodeURIComponent(workspaceId)}/repos`,
    req,
  );
  return unwrap(response);
}

export async function createWorkspaceAgent(
  workspaceId: string,
  req: CreateAgentRequest,
): Promise<WorkspaceAgentInfo> {
  return post<WorkspaceAgentInfo>(
    `/api/workspaces/${encodeURIComponent(workspaceId)}/agents`,
    req,
    { timeout: 120_000 },
  );
}

// ============= Workspace Job Polling =============

/** Status values for an async workspace creation job. */
export type WorkspaceJobStatus = "running" | "done" | "failed";

/** Response shape from GET /api/workspaces/jobs/:id. */
export interface WorkspaceJobState {
  status: WorkspaceJobStatus;
  progress?: string;
  workspace_id?: string;
  error?: string;
}

/**
 * Poll the status of an async workspace creation job.
 */
export async function pollWorkspaceJob(
  jobId: string,
): Promise<WorkspaceJobState> {
  return get<WorkspaceJobState>(
    `/api/workspaces/jobs/${encodeURIComponent(jobId)}`,
  );
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
