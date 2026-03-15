import { get, put, del, ApiError } from "./client";

// ============= Types =============

export interface IssueTab {
  id: string;
  type: "details" | "logs" | "terminal";
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
export async function fetchIssueTabState(
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
export async function saveIssueTabState(
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
export async function deleteIssueTabState(issueId: string): Promise<void> {
  await del<ApiResult<undefined>>(
    `/api/issues/${encodeURIComponent(issueId)}/tabs`,
  );
}
