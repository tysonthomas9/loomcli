/**
 * API functions for session audit trail endpoints.
 * Uses openapi-fetch generated client.
 */

import type { SessionRecord, TranscriptEntry } from "@/types/agent";

import { api, ApiError, apiErrorFromResponse } from "@/api/common";

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

/** Fetch all recorded sessions for a background agent, newest first. */
export async function getAgentSessions(
  workspaceId: string,
  agentName: string,
): Promise<SessionRecord[]> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/agents/{agentName}/sessions",
    { params: { path: { ws: workspaceId, agentName } } },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return (data?.data?.sessions ?? []) as unknown as SessionRecord[];
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
): Promise<TranscriptEntry[]> {
  try {
    const { data, error, response } = await api.GET(
      "/api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}/transcript",
      {
        params: { path: { ws: workspaceId, taskId, sessionId } },
      },
    );
    if (error) throw apiErrorFromResponse(error, response);
    return (data!.data?.entries ?? []) as unknown as TranscriptEntry[];
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      return [];
    }
    throw err;
  }
}

/** Fetch the canonical transcript for an agent-owned session. */
export async function getAgentSessionTranscript(
  workspaceId: string,
  agentName: string,
  sessionId: string,
): Promise<TranscriptEntry[]> {
  const { data, error, response } = await api.GET(
    "/api/workspaces/{ws}/agents/{agentName}/sessions/{sessionId}/transcript",
    { params: { path: { ws: workspaceId, agentName, sessionId } } },
  );
  if (error) throw apiErrorFromResponse(error, response);
  return (data?.data?.entries ?? []) as unknown as TranscriptEntry[];
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

/** Fetch the diff for an agent-owned session. */
export async function getAgentSessionDiff(
  workspaceId: string,
  agentName: string,
  sessionId: string,
): Promise<string | null> {
  try {
    const { data, error, response } = await api.GET(
      "/api/workspaces/{ws}/agents/{agentName}/sessions/{sessionId}/diff",
      {
        params: { path: { ws: workspaceId, agentName, sessionId } },
        parseAs: "text",
      },
    );
    if (error) throw apiErrorFromResponse(error, response);
    return data ?? null;
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) return null;
    throw err;
  }
}
