/**
 * Type-safe API functions for issue operations.
 * Acts as the primary interface between React components and the Go backend.
 */

import { get, post, patch, del, ApiError } from './client';
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
} from '@/types';

// ============= Response Types =============

/**
 * API success response wrapper.
 * Matches backend pattern for /api/ready, /api/stats, /api/issues endpoints.
 */
interface ApiSuccess<T> {
  success: true;
  data: T;
}

/**
 * API error response wrapper.
 * Matches backend pattern when success is false.
 */
interface ApiFailure {
  success: false;
  error: string;
  code?: string;
}

/**
 * Union type for wrapped API responses.
 */
type ApiResult<T> = ApiSuccess<T> | ApiFailure;

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
      // Arrays become comma-separated: labels=a,b,c
      if (value.length > 0) {
        searchParams.set(key, value.join(','));
      }
    } else if (typeof value === 'boolean') {
      searchParams.set(key, value ? 'true' : 'false');
    } else {
      searchParams.set(key, String(value));
    }
  }
  const queryString = searchParams.toString();
  return queryString ? `?${queryString}` : '';
}

/**
 * Unwrap API response, throwing ApiError on failure.
 * Used for endpoints that return wrapped responses.
 */
function unwrap<T>(response: ApiResult<T>): T {
  if (!response.success) {
    throw new ApiError(0, response.error);
  }
  return response.data;
}

/**
 * Map WorkFilter to backend query parameter names.
 * WorkFilter uses 'sort_policy' but backend expects 'sort'.
 */
function mapWorkFilterToQueryParams(filter: WorkFilter): Record<string, unknown> {
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
export async function getIssue(id: string): Promise<IssueDetails> {
  const response = await get<ApiResult<IssueDetails>>(`/api/issues/${encodeURIComponent(id)}`);
  return unwrap(response);
}

/**
 * Get issues ready for work (no blocking dependencies).
 */
export async function getReadyIssues(options?: WorkFilter): Promise<Issue[]> {
  const query = buildQueryString(mapWorkFilterToQueryParams(options ?? {}));
  const response = await get<ApiResult<Issue[]>>(`/api/ready${query}`);
  return unwrap(response);
}

/**
 * Get project statistics.
 */
export async function getStats(): Promise<Statistics> {
  const response = await get<ApiResult<Statistics>>('/api/stats');
  return unwrap(response);
}

/**
 * Filter options for blocked issues.
 */
export interface BlockedFilter {
  /** Filter to descendants of this parent issue/epic */
  parent_id?: string;
  /** Filter by priority (0-4) */
  priority?: number;
  /** Filter by issue type */
  type?: string;
  /** Filter by assignee */
  assignee?: string;
  /** Max results to return */
  limit?: number;
}

/**
 * Get issues that have blocking dependencies (waiting on other issues).
 */
export async function getBlockedIssues(options?: BlockedFilter): Promise<BlockedIssue[]> {
  const params: Record<string, unknown> = {};
  if (options?.parent_id) {
    params.parent_id = options.parent_id;
  }
  if (options?.priority !== undefined) {
    params.priority = options.priority;
  }
  if (options?.type) {
    params.type = options.type;
  }
  if (options?.assignee) {
    params.assignee = options.assignee;
  }
  if (options?.limit !== undefined) {
    params.limit = options.limit;
  }
  const query = buildQueryString(params);
  const response = await get<ApiResult<BlockedIssue[]>>(`/api/blocked${query}`);
  return unwrap(response);
}

// ============= GRAPH OPERATIONS =============

/**
 * Filter options for graph issues.
 */
export interface GraphFilter {
  /** Status filter: 'all', 'open', or 'closed' (default: 'all') */
  status?: 'all' | 'open' | 'closed';
  /** Include closed issues when status is 'all' (default: true) */
  includeClosed?: boolean;
}

/**
 * Response structure from /api/issues/graph endpoint.
 * Note: Uses simplified dependency format from backend.
 */
interface GraphApiResponse {
  success: boolean;
  issues?: GraphApiIssue[];
  error?: string;
}

/**
 * Slim issue as returned by the graph API (only fields needed for rendering).
 * The backend returns a slim payload to reduce bandwidth and latency.
 */
interface GraphApiIssue {
  id: string;
  title: string;
  status: string;
  priority: number;
  issue_type: string;
  labels?: string[];
  dependencies?: { depends_on_id: string; type: string }[];
  defer_until?: string;
  due_at?: string;
}

/**
 * Get all issues with dependency data for graph visualization.
 * Transforms the slim backend response to full Issue objects for the frontend.
 *
 * NOTE: The backend returns a slim payload (id, title, status, priority,
 * issue_type, labels, dependencies). Missing Issue fields are set to defaults.
 */
export async function fetchGraphIssues(options?: GraphFilter): Promise<Issue[]> {
  const params: Record<string, unknown> = {};
  if (options?.status) {
    params.status = options.status;
  }
  if (options?.includeClosed !== undefined) {
    params.include_closed = options.includeClosed;
  }
  const query = buildQueryString(params);
  const response = await get<GraphApiResponse>(`/api/issues/graph${query}`);

  if (!response.success) {
    throw new ApiError(0, response.error || 'Unknown error');
  }

  // Warn in development if backend returns success without issues field
  if (response.issues === undefined && process.env.NODE_ENV === 'development') {
    console.warn('[fetchGraphIssues] Backend returned success without issues field');
  }

  // Transform slim graph API response to full Issue objects
  const issues = response.issues ?? [];
  return issues.map((issue): Issue => {
    const result: Issue = {
      id: issue.id,
      title: issue.title,
      status: issue.status as Status,
      priority: issue.priority as Priority,
      issue_type: issue.issue_type as IssueType,
      labels: issue.labels ?? [],
      created_at: '', // Not available in slim payload
      updated_at: '', // Not available in slim payload
      defer_until: issue.defer_until ?? null,
      due_at: issue.due_at ?? null,
    };
    if (issue.dependencies) {
      result.dependencies = issue.dependencies.map((dep) => ({
        issue_id: issue.id,
        depends_on_id: dep.depends_on_id,
        type: dep.type as DependencyType,
        created_at: '', // Not available in slim payload
      }));
    }
    return result;
  });
}

// ============= WRITE OPERATIONS =============
// These endpoints depend on T013, T014, T015 being complete.

/**
 * Request body for creating an issue.
 */
export interface CreateIssueRequest {
  // Required
  title: string;
  issue_type: IssueType;
  priority: Priority;

  // Optional
  id?: string;
  parent?: string;
  description?: string;
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
  due_at?: string;
  defer_until?: string;
}

/**
 * Request body for updating an issue.
 */
export interface UpdateIssueRequest {
  title?: string;
  description?: string;
  design?: string;
  notes?: string;
  priority?: Priority;
  status?: Status;
  assignee?: string;
  labels?: string[];
  issue_type?: IssueType;
}

/**
 * Create a new issue.
 */
export async function createIssue(data: CreateIssueRequest): Promise<Issue> {
  const response = await post<ApiResult<Issue>>('/api/issues', data);
  return unwrap(response);
}

/**
 * Update an existing issue.
 */
export async function updateIssue(id: string, data: UpdateIssueRequest): Promise<Issue> {
  const response = await patch<ApiResult<Issue>>(`/api/issues/${encodeURIComponent(id)}`, data);
  return unwrap(response);
}

/**
 * Close an issue with optional reason.
 */
export async function closeIssue(id: string, reason?: string): Promise<void> {
  const response = await post<ApiResult<null>>(
    `/api/issues/${encodeURIComponent(id)}/close`,
    reason ? { reason } : {}
  );
  unwrap(response);
}

// ============= DEPENDENCY OPERATIONS =============

/**
 * Add a dependency to an issue.
 * @param issueId - The issue that will depend on another
 * @param dependsOnId - The issue being depended on
 * @param depType - Type of dependency (defaults to "blocks")
 */
export async function addDependency(
  issueId: string,
  dependsOnId: string,
  depType: DependencyType = 'blocks'
): Promise<void> {
  const response = await post<ApiResult<null>>(
    `/api/issues/${encodeURIComponent(issueId)}/dependencies`,
    { depends_on_id: dependsOnId, dep_type: depType }
  );
  unwrap(response);
}

/**
 * Remove a dependency from an issue.
 * @param issueId - The issue to remove the dependency from
 * @param dependsOnId - The issue that was being depended on
 */
export async function removeDependency(issueId: string, dependsOnId: string): Promise<void> {
  const response = await del<ApiResult<null>>(
    `/api/issues/${encodeURIComponent(issueId)}/dependencies/${encodeURIComponent(dependsOnId)}`
  );
  unwrap(response);
}

// ============= COMMENT OPERATIONS =============

/**
 * Request body for adding a comment.
 */
export interface AddCommentRequest {
  text: string;
}

/**
 * Add a comment to an issue.
 */
export async function addComment(issueId: string, text: string): Promise<Comment> {
  const response = await post<ApiResult<Comment>>(
    `/api/issues/${encodeURIComponent(issueId)}/comments`,
    { text }
  );
  return unwrap(response);
}

// ============= EXPORTS FOR TESTING =============

/**
 * Exported for unit testing.
 * @internal
 */
export { buildQueryString, unwrap, mapWorkFilterToQueryParams };
