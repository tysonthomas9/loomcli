/**
 * API functions for session audit trail endpoints.
 */

import type {
  SessionRecord,
  SessionListResponse,
  SessionDetailResponse,
  TranscriptEntry,
  TranscriptResponse,
} from "../types/session";

import { ApiError, get, getText } from "./client";

/**
 * Fetch all sessions for a task.
 * @param taskId The task ID (e.g., "bd-abc123")
 * @returns Array of session records, newest first.
 */
export async function getTaskSessions(
  taskId: string,
): Promise<SessionRecord[]> {
  try {
    const resp = await get<SessionListResponse>(
      `/api/tasks/${encodeURIComponent(taskId)}/sessions`,
    );
    return resp.data?.sessions ?? [];
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
  taskId: string,
  sessionId: string,
): Promise<SessionRecord | null> {
  try {
    const resp = await get<SessionDetailResponse>(
      `/api/tasks/${encodeURIComponent(taskId)}/sessions/${encodeURIComponent(sessionId)}`,
    );
    return resp.data ?? null;
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
  taskId: string,
  sessionId: string,
): Promise<TranscriptEntry[]> {
  try {
    const resp = await get<TranscriptResponse>(
      `/api/tasks/${encodeURIComponent(taskId)}/sessions/${encodeURIComponent(sessionId)}/transcript`,
    );
    return resp.data?.entries ?? [];
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      return [];
    }
    throw err;
  }
}

/**
 * Fetch the git diff patch for a session.
 * The diff endpoint returns raw text (Content-Type: text/plain).
 * @returns The diff string, or null if not found.
 */
export async function getSessionDiff(
  taskId: string,
  sessionId: string,
): Promise<string | null> {
  try {
    return await getText(
      `/api/tasks/${encodeURIComponent(taskId)}/sessions/${encodeURIComponent(sessionId)}/diff`,
    );
  } catch (err) {
    if (err instanceof ApiError && err.status === 404) {
      return null;
    }
    throw err;
  }
}
