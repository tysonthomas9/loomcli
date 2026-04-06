/**
 * API functions for session history (Redis-backed, per-issue).
 * Uses openapi-fetch generated client.
 */

import { api, apiErrorFromResponse, unwrapResponse } from "./client";

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

// ============= API Functions =============

/**
 * List session history records for an issue.
 * Returns records sorted by most recent first.
 */
export async function listSessionHistory(
  workspaceId: string,
  issueId: string,
): Promise<SessionRecord[]> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/issues/{issueId}/sessions",
    {
      params: { path: { ws: workspaceId, issueId } },
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return (unwrapResponse(data) ?? []) as SessionRecord[];
}

/**
 * Get scrollback content for a completed session.
 */
export async function getSessionScrollback(
  workspaceId: string,
  issueId: string,
  recordId: string,
): Promise<{ content: string; lines: number }> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/issues/{issueId}/sessions/{recordId}/scrollback",
    {
      params: { path: { ws: workspaceId, issueId, recordId } },
      parseAs: "text",
    },
  );
  if (error) throw apiErrorFromResponse(error, response);
  // scrollback endpoint returns text/plain; wrap it for callers
  const content = typeof data === "string" ? data : "";
  const lines = content ? content.split("\n").length : 0;
  return { content, lines };
}
