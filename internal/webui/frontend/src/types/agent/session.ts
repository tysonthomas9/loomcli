/**
 * TypeScript types for session audit trail data.
 * SessionRecord and TranscriptEntry aliased from generated OpenAPI schemas.
 * Drift: generated SessionResponse uses session_id (not id); has additional task_id, epic_id, last_error fields.
 * Envelope response types (SessionListResponse, SessionDetailResponse, TranscriptResponse) kept hand-written.
 */

import type { components } from "@/types/generated/openapi";

/** Lifecycle state of a session. */
export type SessionStatus = "running" | "completed" | "failed" | "aborted";

/** A single agent session record. Aliased from generated SessionResponse schema. */
export type SessionRecord = components["schemas"]["SessionResponse"];

/** A single entry in a session transcript. Aliased from generated TranscriptEntry schema. */
export type TranscriptEntry = components["schemas"]["TranscriptEntry"];

/** Response from GET /api/workspaces/{ws}/tasks/{taskId}/sessions */
export interface SessionListResponse {
  success: boolean;
  data: {
    task_id: string;
    sessions: SessionRecord[];
  };
}

/** Response from GET /api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId} */
export interface SessionDetailResponse {
  success: boolean;
  data: SessionRecord;
}

/** Response from GET /api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}/transcript */
export interface TranscriptResponse {
  success: boolean;
  data: {
    session_id: string;
    entries: TranscriptEntry[];
  };
}
