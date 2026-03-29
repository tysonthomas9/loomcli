import { get, ApiError, wsUrl } from "./client";

// ============= Types =============

export interface SessionRecord {
  id: string;
  session_name: string;
  issue_id: string;
  backend: string;
  status: "active" | "completed";
  launcher: string;
  started_at: string;
  ended_at?: string;
  scrollback_path?: string;
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
 * List session history records for an issue.
 * Returns records sorted by most recent first.
 */
export async function listSessionHistory(
  workspaceId: string,
  issueId: string,
): Promise<SessionRecord[]> {
  const response = await get<ApiResult<SessionRecord[]>>(
    wsUrl(workspaceId, `/issues/${encodeURIComponent(issueId)}/sessions`),
  );
  return unwrap(response);
}

/**
 * Get scrollback content for a completed session.
 */
export async function getSessionScrollback(
  workspaceId: string,
  issueId: string,
  recordId: string,
): Promise<{ content: string; lines: number }> {
  const response = await get<ApiResult<{ content: string; lines: number }>>(
    wsUrl(
      workspaceId,
      `/issues/${encodeURIComponent(issueId)}/sessions/${encodeURIComponent(recordId)}/scrollback`,
    ),
  );
  return unwrap(response);
}
