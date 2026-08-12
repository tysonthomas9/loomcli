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
  wsUrl,
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
  remote_url?: string;
  source_repo_id?: string;
  groups: string[];
  is_linked_worktree?: boolean;
}

export interface WorkspaceAgentInfo {
  name: string;
  repos: string[];
  repo_groups: string[];
  cross_repo: boolean;
  role_name?: string;
  backend?: string;
}

export interface CreateAgentRequest {
  name: string;
  role_name: string;
  kind?: string;
  prompt?: string;
  prompt_file?: string;
  auto?: boolean;
  backend?: string;
  repos?: string[];
  repo_groups?: string[];
  cross_repo?: boolean;
}

export interface InteractivePromptInfo {
  id: string;
  label: string;
}

interface InteractivePromptsResponse {
  prompts: InteractivePromptInfo[];
}

export interface RunOnboardingFirstTaskRequest {
  agent_name: string;
  title: string;
  description?: string;
  issue_type?: "task" | "bug" | "feature" | "epic" | "chore";
  priority?: number;
  source_repo?: string;
}

export interface RunOnboardingFirstTaskResponse {
  success: boolean;
  issue: Issue;
  agent_name: string;
  started: boolean;
  queued?: boolean;
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
  design_format?: "markdown" | "html";
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

function unwrap<T>(
  response: ApiResult<T> | null | undefined,
  httpResponse?: Response,
): T {
  if (response == null) {
    throw new ApiError(
      httpResponse?.status ?? 0,
      httpResponse?.statusText || "Invalid API response",
      "missing response envelope",
    );
  }
  if (!response.success) {
    throw new ApiError(
      httpResponse?.status ?? 0,
      httpResponse?.statusText || response.error,
      response.error,
    );
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
    return unwrap(data as unknown as ApiResult<WorkspaceData>, response);
  }
  const { data, error, response } = await api.GET("/api/workspaces/active");
  if (error) throw apiErrorFromResponse(error, response);
  return unwrap(data as unknown as ApiResult<WorkspaceData>, response);
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
  return unwrap(response);
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
  repos?: string[];
  clone_urls?: string[];
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

export interface WorkspaceRole {
  workspace_key: string;
  name: string;
  kind?: "interactive" | "worker";
  description?: string;
  prompt?: string;
  prompt_file?: string;
  model?: string;
  task_filter?: string;
  backend?: string;
  effort?: string;
  path_patterns?: string[];
  skills?: string[];
  max_priority?: number;
  max_concurrency?: number;
  read_only?: boolean;
  allowed_tools?: string[];
  denied_tools?: string[];
  max_budget_usd?: number;
  created_at?: string;
  updated_at?: string;
}

/**
 * GET/PATCH single-role response: the stored role plus its current prompt-file
 * body (empty string for builtins that carry no prompt file).
 */
export interface RoleWithPrompt {
  role: WorkspaceRole;
  prompt: string;
}

/**
 * Partial role update — every field is optional so the UI can PATCH just the
 * prompt without resending the whole role. Sending `prompt` rewrites the
 * role's prompt file (reusing its existing filename when `prompt_filename` is
 * omitted). Changes take effect on the agent's NEXT start/restart; a running
 * agent keeps the prompt it read at launch.
 */
export interface UpdateRoleRequest {
  description?: string;
  prompt?: string;
  prompt_filename?: string;
  model?: string;
  task_filter?: string;
  backend?: string;
  effort?: string;
  read_only?: boolean;
  allowed_tools?: string[];
  denied_tools?: string[];
  skills?: string[];
}

/** Duplicate an existing role (config + prompt) under a new name. */
export interface CloneRoleRequest {
  target_name: string;
  description?: string;
}

export interface CreateRoleRequest {
  name: string;
  description?: string;
  /** Prompt body; the backend writes it to disk and records the path. */
  prompt?: string;
  prompt_filename?: string;
  model?: string;
  task_filter?: string;
  backend?: string;
  effort?: string;
  read_only?: boolean;
  allowed_tools?: string[];
  denied_tools?: string[];
  skills?: string[];
}

/**
 * Ensure a custom agent Role exists (idempotent). Used by the create-agent
 * gallery to provision custom supervised templates (e.g. bug triage) before
 * the agent row is created with that role.
 */
export async function createWorkspaceRole(
  workspaceId: string,
  req: CreateRoleRequest,
): Promise<WorkspaceRole> {
  return post<WorkspaceRole>(wsUrl(workspaceId, "/roles"), req, {
    timeout: 60_000,
  });
}

/**
 * List every role in the workspace (builtins + custom). Used by the prompt-agent
 * create path to offer a dropdown of existing roles to wear. Returns the raw
 * role records (no prompt body — fetch that per-role with getWorkspaceRole).
 */
export async function listWorkspaceRoles(
  workspaceId: string,
): Promise<WorkspaceRole[]> {
  return get<WorkspaceRole[]>(wsUrl(workspaceId, "/roles"));
}

/**
 * Read a single role plus its current prompt body so the UI can populate an
 * editor. 404 when the role does not exist.
 */
export async function getWorkspaceRole(
  workspaceId: string,
  name: string,
): Promise<RoleWithPrompt> {
  return get<RoleWithPrompt>(
    wsUrl(workspaceId, `/roles/${encodeURIComponent(name)}`),
  );
}

/**
 * Apply a partial edit to a role. Returns the updated role plus its (possibly
 * rewritten) prompt body. Sending `prompt` rewrites the prompt file; the change
 * takes effect on the agent's next start/restart, not on a running agent.
 */
export async function updateWorkspaceRole(
  workspaceId: string,
  name: string,
  req: UpdateRoleRequest,
): Promise<RoleWithPrompt> {
  return patch<RoleWithPrompt>(
    wsUrl(workspaceId, `/roles/${encodeURIComponent(name)}`),
    req,
    { timeout: 60_000 },
  );
}

/**
 * Clone a role (config + prompt) under a new name. Throws `ApiError` with
 * status 409 when the target name is already taken, 400 for an empty/self
 * target, 404 when the source role is missing.
 */
export async function cloneWorkspaceRole(
  workspaceId: string,
  name: string,
  req: CloneRoleRequest,
): Promise<WorkspaceRole> {
  return post<WorkspaceRole>(
    wsUrl(workspaceId, `/roles/${encodeURIComponent(name)}/clone`),
    req,
    { timeout: 60_000 },
  );
}

/**
 * Delete a custom role. Throws `ApiError` with status 400 for a builtin
 * (plan/task) refusal, 404 when the role is missing.
 */
export async function deleteWorkspaceRole(
  workspaceId: string,
  name: string,
): Promise<void> {
  await del<void>(wsUrl(workspaceId, `/roles/${encodeURIComponent(name)}`));
}

export async function fetchInteractivePrompts(
  workspaceId: string,
): Promise<InteractivePromptInfo[]> {
  const response = await get<InteractivePromptsResponse>(
    `/api/workspaces/${encodeURIComponent(workspaceId)}/interactive-prompts`,
  );
  return Array.isArray(response.prompts) ? response.prompts : [];
}

export async function deleteWorkspaceAgent(
  workspaceId: string,
  name: string,
): Promise<void> {
  await del<unknown>(
    `/api/workspaces/${encodeURIComponent(workspaceId)}/agents/${encodeURIComponent(name)}`,
  );
}

export async function runOnboardingFirstTask(
  workspaceId: string,
  req: RunOnboardingFirstTaskRequest,
): Promise<RunOnboardingFirstTaskResponse> {
  return post<RunOnboardingFirstTaskResponse>(
    `/api/workspaces/${encodeURIComponent(workspaceId)}/onboarding/first-task`,
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
