/**
 * API functions for session audit trail endpoints.
 * Uses openapi-fetch generated client.
 */

import type { SessionRecord, TranscriptEntry } from "@/types/agent";

import { api, ApiError, apiErrorFromResponse, get, wsUrl } from "@/api/common";

interface TranscriptResponse {
  success: boolean;
  data?: {
    session_id?: string;
    entries?: TranscriptEntry[] | null;
  } | null;
}

export interface TranscriptFetchOptions {
  /**
   * Preserve a 404 so polling hooks can distinguish a transcript that is not
   * projected/available from a valid transcript containing zero entries.
   * Existing direct API callers retain the historical empty-array behavior.
   */
  preserveNotFound?: boolean;
}

/**
 * Fetch all sessions for a task.
 * @param taskId The task ID (e.g., "loomcli-abc123")
 * @returns Array of session records, newest first.
 */
export async function getTaskSessions(
  workspaceId: string,
  taskId: string,
): Promise<SessionRecord[]> {
  try {
    const { data, error, response } = await api.GET(
      "/api/workspaces/{ws}/tasks/{taskId}/sessions",
      {
        params: { path: { ws: workspaceId, taskId } },
      },
    );
    if (error) throw apiErrorFromResponse(error, response);
    return (data!.data?.sessions ?? []) as unknown as SessionRecord[];
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      return [];
    }
    throw err;
  }
}

/**
 * Fetch a single session's metadata.
 * @returns The session record, or null if not found.
 */
export async function getSession(
  workspaceId: string,
  taskId: string,
  sessionId: string,
): Promise<SessionRecord | null> {
  try {
    const { data, error, response } = await api.GET(
      "/api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}",
      {
        params: { path: { ws: workspaceId, taskId, sessionId } },
      },
    );
    if (error) throw apiErrorFromResponse(error, response);
    return (data!.data as unknown as SessionRecord) ?? null;
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      return null;
    }
    throw err;
  }
}

/**
 * Fetch the transcript for a session.
 * @returns Array of transcript entries, or empty array if not found.
 */
export async function getSessionTranscript(
  workspaceId: string,
  taskId: string,
  sessionId: string,
  options: TranscriptFetchOptions = {},
): Promise<TranscriptEntry[]> {
  const pathTaskId = requiredPathID(taskId, "taskId");
  const pathSessionId = requiredPathID(sessionId, "sessionId");
  try {
    const { data, error, response } = await api.GET(
      "/api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}/transcript",
      {
        params: {
          path: {
            ws: workspaceId,
            taskId: pathTaskId,
            sessionId: pathSessionId,
          },
        },
      },
    );
    if (error) throw apiErrorFromResponse(error, response);
    return (data!.data?.entries ?? []) as unknown as TranscriptEntry[];
  } catch (err) {
    if (
      err instanceof ApiError &&
      err.status === 404 &&
      !options.preserveNotFound
    ) {
      return [];
    }
    throw err;
  }
}

/**
 * Fetch a transcript by agent-session ownership rather than task ownership.
 *
 * Interactive terminal sessions are not task-scoped, so they cannot use the
 * task session route above. The response intentionally shares the canonical
 * transcript envelope with that route.
 */
export async function getAgentSessionTranscript(
  workspaceId: string,
  agentId: string,
  sessionId: string,
  options: TranscriptFetchOptions = {},
): Promise<TranscriptEntry[]> {
  const pathAgentId = requiredPathID(agentId, "agentId");
  const pathSessionId = requiredPathID(sessionId, "sessionId");
  try {
    const response = await get<TranscriptResponse>(
      wsUrl(
        workspaceId,
        `/agents/${encodeURIComponent(pathAgentId)}/sessions/${encodeURIComponent(pathSessionId)}/transcript`,
      ),
    );
    return response.data?.entries ?? [];
  } catch (err) {
    if (
      err instanceof ApiError &&
      err.status === 404 &&
      !options.preserveNotFound
    ) {
      return [];
    }
    throw err;
  }
}

function requiredPathID(value: string, name: string): string {
  const normalized = value.trim();
  if (!normalized) {
    throw new Error(`${name} is required to fetch a session transcript`);
  }
  return normalized;
}

/**
 * Fetch the git diff patch for a session.
 * The diff endpoint returns raw text (Content-Type: text/plain).
 * @returns The diff string, or null if not found.
 */
export async function getSessionDiff(
  workspaceId: string,
  taskId: string,
  sessionId: string,
): Promise<string | null> {
  try {
    const { data, error, response } = await api.GET(
      "/api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}/diff",
      {
        params: { path: { ws: workspaceId, taskId, sessionId } },
        parseAs: "text",
      },
    );
    if (error) throw apiErrorFromResponse(error, response);
    return data ?? null;
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      return null;
    }
    throw err;
  }
}
