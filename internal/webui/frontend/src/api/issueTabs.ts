import { get, put, del, ApiError } from "./client";

// ============= Types =============

export interface IssueTab {
  id: string;
  type: "details" | "logs" | "terminal" | "sessions";
  label: string;
  session_name?: string;
  sort_order: number;
}

export interface IssueTabState {
  issue_id: string;
  tabs: IssueTab[];
  active_tab_id: string;
  updated_at: string;
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

function unwrap<T>(response: ApiResult<T>): T {
  if (!response.success) {
    throw new ApiError(0, response.error);
  }
  return response.data;
}

// ============= API Functions =============

/**
 * Fetch persisted tab state for an issue.
 * Returns null if no saved state exists.
 */
// TODO(workspace-routing): migrate to wsUrl when workspace-scoped route lands
export async function fetchIssueTabState(
  _workspaceId: string,
  issueId: string,
): Promise<IssueTabState | null> {
  const response = await get<ApiResult<IssueTabState | null>>(
    `/api/issues/${encodeURIComponent(issueId)}/tabs`,
  );
  return unwrap(response);
}

/**
 * Save full tab state for an issue via PUT.
 */
// TODO(workspace-routing): migrate to wsUrl when workspace-scoped route lands
export async function saveIssueTabState(
  _workspaceId: string,
  issueId: string,
  tabs: IssueTab[],
  activeTabId: string,
): Promise<void> {
  await put<ApiResult<IssueTabState>>(
    `/api/issues/${encodeURIComponent(issueId)}/tabs`,
    { tabs, active_tab_id: activeTabId },
  );
}

/**
 * Delete persisted tab state for an issue.
 */
// TODO(workspace-routing): migrate to wsUrl when workspace-scoped route lands
export async function deleteIssueTabState(
  _workspaceId: string,
  issueId: string,
): Promise<void> {
  await del<ApiResult<undefined>>(
    `/api/issues/${encodeURIComponent(issueId)}/tabs`,
  );
}
