/**
 * Type-safe API functions for issue operations.
 * Acts as the primary interface between React components and the Go backend.
 * Uses openapi-fetch generated client for type-safe requests.
 */

import type {
  Issue,
  IssueDetails,
  BlockedIssue,
  Statistics,
  WorkFilter,
  Priority,
  IssueType,
  Status,
  DependencyType,
  Comment,
} from "@/types";

import { api, ApiError, apiErrorFromResponse, cleanQuery } from "@/api/common";

// ============= Helper Functions =============

/**
 * Build query string from filter object.
 * Omits undefined/null values.
 */
function buildQueryString(params: Record<string, unknown>): string {
  const searchParams = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null) continue;
    if (Array.isArray(value)) {
      if (value.length > 0) {
        searchParams.set(key, value.join(","));
      }
    } else if (typeof value === "boolean") {
      searchParams.set(key, value ? "true" : "false");
    } else {
      searchParams.set(key, String(value));
    }
  }
  const queryString = searchParams.toString();
  return queryString ? `?${queryString}` : "";
}

/**
 * Unwrap API response, throwing ApiError on failure.
 */
function unwrap<T>(
  response: { success: boolean; data?: T; error?: string } | undefined,
  httpResponse?: Response,
): T {
  if (!response) throw new ApiError(0, "Empty response");
  if (!response.success) {
    throw new ApiError(
      httpResponse?.status ?? 0,
      httpResponse?.statusText || response.error || "Unknown error",
      response.error,
    );
  }
  return response.data as T;
}

type IssueWireFields = {
  parent_id?: string;
  parent?: string | null;
  parent_title?: string | null;
  type?: IssueType | null;
  issue_type?: IssueType | null;
};

function getWireIssueType(issue: Issue | IssueDetails): IssueType | undefined {
  const wire = issue as IssueWireFields;
  return wire.issue_type ?? wire.type ?? undefined;
}

function normalizeIssue(
  issue: Issue | IssueDetails,
  epicTitles?: Map<string, string>,
): Issue | IssueDetails {
  const wire = issue as IssueWireFields;
  const parent = wire.parent ?? wire.parent_id;
  const parentTitle =
    wire.parent_title ?? (parent ? epicTitles?.get(parent) : undefined);
  const issueType = getWireIssueType(issue);

  if (
    (!issue.repo && issue.source_repo) ||
    (issueType !== undefined && issue.issue_type !== issueType) ||
    (parent !== undefined && wire.parent !== parent) ||
    (parentTitle !== undefined && wire.parent_title !== parentTitle)
  ) {
    return {
      ...issue,
      ...(!issue.repo && issue.source_repo ? { repo: issue.source_repo } : {}),
      ...(issueType !== undefined ? { issue_type: issueType } : {}),
      ...(parent !== undefined ? { parent } : {}),
      ...(parentTitle !== undefined ? { parent_title: parentTitle } : {}),
    };
  }
  return issue;
}

function normalizeIssueRepo<T extends Issue | IssueDetails>(issue: T): T {
  return normalizeIssue(issue) as T;
}

function normalizeIssueRepos<T extends Issue | IssueDetails>(issues: T[]): T[] {
  const epicTitles = new Map<string, string>();
  for (const issue of issues) {
    if (getWireIssueType(issue) === "epic") {
      epicTitles.set(issue.id, issue.title);
    }
  }
  return issues.map((issue) => normalizeIssue(issue, epicTitles) as T);
}

/**
 * Map WorkFilter to backend query parameter names.
 */
function mapWorkFilterToQueryParams(
  filter: WorkFilter,
): Record<string, unknown> {
  const { sort_policy, ...rest } = filter;
  const params: Record<string, unknown> = { ...rest };
  if (sort_policy !== undefined) {
    params.sort = sort_policy;
  }
  return params;
}

// ============= READ OPERATIONS =============

/**
 * Get a single issue by ID with full details.
 */
export async function getIssue(
  workspaceId: string,
  id: string,
  requestOptions?: { signal?: AbortSignal },
): Promise<IssueDetails> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/issues/{id}",
    {
      params: { path: { ws: workspaceId, id } },
      ...(requestOptions?.signal ? { signal: requestOptions.signal } : {}),
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return normalizeIssueRepo(unwrap(data, response) as unknown as IssueDetails);
}

/**
 * Get issues ready for work (no blocking dependencies).
 */
export async function getReadyIssues(
  workspaceId: string,
  options?: WorkFilter,
  requestOptions?: { signal?: AbortSignal },
): Promise<Issue[]> {
  const mapped = mapWorkFilterToQueryParams(options ?? {});
  const query = cleanQuery({
    sort: mapped.sort as string | undefined,
    assignee: mapped.assignee as string | undefined,
    type: mapped.type as string | undefined,
    priority: mapped.priority as number | undefined,
    limit: mapped.limit as number | undefined,
    labels: mapped.labels ? String(mapped.labels) : undefined,
    source_repos: mapped.source_repos
      ? Array.isArray(mapped.source_repos)
        ? (mapped.source_repos as string[]).join(",")
        : String(mapped.source_repos)
      : undefined,
  });
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/ready",
    {
      params: { path: { ws: workspaceId }, query },
      ...(requestOptions?.signal ? { signal: requestOptions.signal } : {}),
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return normalizeIssueRepos(unwrap(data, response) as unknown as Issue[]);
}

/**
 * Get project statistics.
 */
export async function getStats(workspaceId: string): Promise<Statistics> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/stats",
    { params: { path: { ws: workspaceId } } },
  );
  if (error) throw apiErrorFromResponse(error, response);
  // This endpoint returns Statistics directly, not wrapped
  return data as unknown as Statistics;
}

/**
 * Filter options for blocked issues.
 */
export interface BlockedFilter {
  parent_id?: string;
  priority?: number;
  type?: string;
  assignee?: string;
  limit?: number;
}

/**
 * Get issues that have blocking dependencies (waiting on other issues).
 */
export async function getBlockedIssues(
  workspaceId: string,
  options?: BlockedFilter,
): Promise<BlockedIssue[]> {
  const query = cleanQuery({
    parent_id: options?.parent_id,
    priority: options?.priority,
    type: options?.type as string | undefined,
    assignee: options?.assignee,
    limit: options?.limit,
  });
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/blocked",
    {
      params: { path: { ws: workspaceId }, query },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return unwrap(data, response) as unknown as BlockedIssue[];
}

/**
 * The kanban board renders the workspace's full working set; without an
 * explicit limit the server defaults to 50, silently truncating boards past
 * 50 issues (sort order decides which cards vanish — live incident: the only
 * in_progress issue, at position 88 of 98, left the In Progress column empty
 * while an agent worked it). 500 matches the issue-events endpoint cap.
 */
const KANBAN_ISSUE_FETCH_LIMIT = 500;

/**
 * Get issues for the Kanban board view.
 */
export async function getKanbanIssues(
  workspaceId: string,
  options?: WorkFilter,
  requestOptions?: { signal?: AbortSignal },
): Promise<Issue[]> {
  const mapped = options ? mapWorkFilterToQueryParams(options) : {};
  const query = cleanQuery({
    exclude_status: "tombstone",
    include_blocked: true,
    status: mapped.status as string | undefined,
    type: mapped.type as string | undefined,
    assignee: mapped.assignee as string | undefined,
    priority: mapped.priority as number | undefined,
    limit: (mapped.limit as number | undefined) ?? KANBAN_ISSUE_FETCH_LIMIT,
    labels: mapped.labels ? String(mapped.labels) : undefined,
    source_repos: mapped.source_repos
      ? Array.isArray(mapped.source_repos)
        ? (mapped.source_repos as string[]).join(",")
        : String(mapped.source_repos)
      : undefined,
  });
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/issues",
    {
      params: { path: { ws: workspaceId }, query },
      ...(requestOptions?.signal ? { signal: requestOptions.signal } : {}),
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return normalizeIssueRepos(unwrap(data, response) as unknown as Issue[]);
}

// ============= GRAPH OPERATIONS =============

export interface GraphFilter {
  status?: "all" | "open" | "closed";
  includeClosed?: boolean;
  source_repos?: string[];
}

/**
 * Get all issues with dependency data for graph visualization.
 * Transforms the slim backend response to full Issue objects for the frontend.
 */
export async function fetchGraphIssues(
  workspaceId: string,
  options?: GraphFilter,
  requestOptions?: { signal?: AbortSignal },
): Promise<Issue[]> {
  const query = cleanQuery({
    status: options?.status,
    include_closed: options?.includeClosed,
    source_repos: options?.source_repos?.length
      ? options.source_repos.join(",")
      : undefined,
  });
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/issues/graph",
    {
      params: { path: { ws: workspaceId }, query },
      ...(requestOptions?.signal ? { signal: requestOptions.signal } : {}),
    },
  );
  if (error) throw apiErrorFromResponse(error, response);

  if (!data || !data.success) {
    throw new ApiError(
      response?.status ?? 0,
      response?.statusText || "Unknown error",
      "Unknown error",
    );
  }

  if (data.data === undefined && process.env.NODE_ENV === "development") {
    console.warn(
      "[fetchGraphIssues] Backend returned success without data field",
    );
  }

  const issues = data.data ?? [];
  return issues.map((issue): Issue => {
    const result: Issue = {
      id: issue.id,
      title: issue.title,
      status: (issue.status ?? "open") as Status,
      priority: issue.priority as Priority,
      issue_type: (issue.issue_type ?? "task") as IssueType,
      labels: issue.labels ?? [],
      created_at: issue.created_at ?? "",
      updated_at: issue.updated_at ?? "",
      defer_until: issue.defer_until ?? null,
      due_at: issue.due_at ?? null,
    };
    if (issue.dependencies) {
      result.dependencies = issue.dependencies.map((dep) => ({
        issue_id: issue.id,
        depends_on_id: dep.depends_on_id,
        type: dep.type as DependencyType,
        created_at: dep.created_at ?? "",
      }));
    }
    return result;
  });
}

// ============= WRITE OPERATIONS =============

export interface CreateIssueRequest {
  title: string;
  issue_type: IssueType;
  priority: Priority;
  id?: string;
  parent?: string;
  description?: string;
  status?: "open" | "deferred";
  design?: string;
  acceptance_criteria?: string;
  notes?: string;
  assignee?: string;
  owner?: string;
  created_by?: string;
  external_ref?: string;
  estimated_minutes?: number;
  labels?: string[];
  dependencies?: string[];
  source_repo?: string;
  due_at?: string;
  defer_until?: string;
}

export interface UpdateIssueRequest {
  title?: string;
  description?: string;
  design?: string;
  notes?: string;
  priority?: Priority;
  status?: Status;
  assignee?: string;
  owner?: string;
  external_ref?: string;
  labels?: string[];
  add_labels?: string[];
  remove_labels?: string[];
  issue_type?: IssueType;
}

/**
 * Create a new issue.
 */
export async function createIssue(
  workspaceId: string,
  reqData: CreateIssueRequest,
): Promise<Issue> {
  const body = cleanQuery({
    title: reqData.title,
    issue_type: reqData.issue_type as
      | "bug"
      | "feature"
      | "task"
      | "epic"
      | "chore",
    priority: reqData.priority as number,
    id: reqData.id,
    parent: reqData.parent,
    description: reqData.description,
    design: reqData.design,
    acceptance_criteria: reqData.acceptance_criteria,
    notes: reqData.notes,
    assignee: reqData.assignee,
    owner: reqData.owner,
    created_by: reqData.created_by,
    external_ref: reqData.external_ref,
    estimated_minutes: reqData.estimated_minutes,
    labels: reqData.labels,
    dependencies: reqData.dependencies,
    source_repo: reqData.source_repo,
    due_at: reqData.due_at,
    defer_until: reqData.defer_until,
  });
  const { data, error, response } = await api.POST(
    "/api/workspaces/{ws}/issues",
    {
      params: { path: { ws: workspaceId } },
      body,
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return normalizeIssueRepo(unwrap(data, response) as unknown as Issue);
}

/**
 * Update an existing issue.
 */
export async function updateIssue(
  workspaceId: string,
  id: string,
  reqData: UpdateIssueRequest,
): Promise<Issue> {
  const body = cleanQuery({
    title: reqData.title,
    description: reqData.description,
    design: reqData.design,
    notes: reqData.notes,
    priority: reqData.priority as number | undefined,
    status: reqData.status as string | undefined,
    assignee: reqData.assignee,
    owner: reqData.owner,
    external_ref: reqData.external_ref,
    set_labels: reqData.labels,
    add_labels: reqData.add_labels,
    remove_labels: reqData.remove_labels,
    issue_type: reqData.issue_type,
  });
  const { data, error, response } = await api.PATCH(
    "/api/workspaces/{ws}/issues/{id}",
    {
      params: { path: { ws: workspaceId, id } },
      body,
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return normalizeIssueRepo(unwrap(data, response) as unknown as Issue);
}

/**
 * Assign an issue's canonical workspace repository. FleetDB owns the atomic
 * repository-required blocked-to-open recovery and returns the authoritative
 * post-command issue; callers must not issue a separate reopen mutation.
 */
export async function setIssueRepository(
  workspaceId: string,
  id: string,
  repo: string,
): Promise<Issue> {
  const { data, error, response } = await api.PUT(
    "/api/workspaces/{ws}/issues/{id}/repository",
    {
      params: { path: { ws: workspaceId, id } },
      body: { repo },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return normalizeIssueRepo(unwrap(data, response) as unknown as Issue);
}

/**
 * Close an issue with optional reason.
 */
export async function closeIssue(
  workspaceId: string,
  id: string,
  reason?: string,
): Promise<void> {
  const { error, response } = await api.POST(
    "/api/workspaces/{ws}/issues/{id}/close",
    {
      params: { path: { ws: workspaceId, id } },
      body: reason ? { reason } : {},
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
}

// ============= DEPENDENCY OPERATIONS =============

export async function addDependency(
  workspaceId: string,
  issueId: string,
  dependsOnId: string,
  depType: DependencyType = "blocks",
): Promise<void> {
  const { error, response } = await api.POST(
    "/api/workspaces/{ws}/issues/{id}/dependencies",
    {
      params: { path: { ws: workspaceId, id: issueId } },
      body: { depends_on_id: dependsOnId, dep_type: depType },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
}

export async function removeDependency(
  workspaceId: string,
  issueId: string,
  dependsOnId: string,
): Promise<void> {
  const { error, response } = await api.DELETE(
    "/api/workspaces/{ws}/issues/{id}/dependencies/{depId}",
    {
      params: { path: { ws: workspaceId, id: issueId, depId: dependsOnId } },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
}

// ============= MOVE OPERATIONS =============

export interface MoveIssueResult {
  source_id: string;
  target_id: string;
  warnings?: string[];
}

export async function moveIssue(
  workspaceId: string,
  id: string,
  targetWorkspace: string,
): Promise<MoveIssueResult> {
  const { data, error, response } = await api.POST(
    "/api/workspaces/{ws}/issues/{id}/move",
    {
      params: { path: { ws: workspaceId, id } },
      body: { target_workspace: targetWorkspace },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return unwrap(data, response) as MoveIssueResult;
}

// ============= COMMENT OPERATIONS =============

export interface AddCommentRequest {
  text: string;
}

export async function addComment(
  workspaceId: string,
  issueId: string,
  text: string,
): Promise<Comment> {
  const { data, error, response } = await api.POST(
    "/api/workspaces/{ws}/issues/{id}/comments",
    {
      params: { path: { ws: workspaceId, id: issueId } },
      body: { text },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return unwrap(data, response) as unknown as Comment;
}

// ============= EXPORTS FOR TESTING =============

/** @internal */
export { buildQueryString, unwrap, mapWorkFilterToQueryParams };
