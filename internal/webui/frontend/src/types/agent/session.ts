/**
 * TypeScript types for session audit trail data.
 *
 * SessionRecord is aliased from the generated OpenAPI schema.
 *
 * TranscriptEntry is also aliased from the generated contract so parser
 * vocabulary additions (for example reasoning and result events) cannot drift
 * from the UI's accepted wire shape.
 */

import type { components, operations } from "@/types/generated/openapi";

/** Lifecycle state of a session. */
export type SessionStatus = "running" | "completed" | "failed" | "aborted";

/** A single agent session record. Aliased from generated SessionResponse schema. */
export type SessionRecord = components["schemas"]["SessionResponse"];

/** A single canonical transcript event from the generated API contract. */
export type TranscriptEntry = components["schemas"]["TranscriptEntry"];

/** Canonical role values in the transcript event stream. */
export type TranscriptEntryRole = TranscriptEntry["role"];

/** Canonical event type values in the transcript event stream. */
export type TranscriptEntryType = TranscriptEntry["type"];

type Assert<Condition extends true> = Condition;
type IsAssignable<Actual, Expected> = [Actual] extends [Expected]
  ? true
  : false;

/**
 * Compile-only contract fixture. This module is part of the production
 * TypeScript program, unlike Vitest files, so `npm run typecheck` fails if the
 * generated transcript entry stops accepting the canonical UI shape.
 */
export type TranscriptEntryCompatibilityTypecheck = Assert<
  IsAssignable<
    {
      seq: number;
      timestamp: string;
      role: "assistant";
      type: "reasoning";
      text: string;
    },
    TranscriptEntry
  >
>;

type TaskTranscriptErrorStatus = 400 | 404 | 500 | 503;
type TaskTranscriptErrorPayload =
  operations["getSessionTranscript"]["responses"][TaskTranscriptErrorStatus]["content"]["application/json"];

/** Compile-only lock for the documented task-transcript error envelope. */
export type TaskTranscriptErrorContractTypecheck = Assert<
  IsAssignable<
    TaskTranscriptErrorPayload,
    components["schemas"]["TranscriptResponse"]
  >
>;

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

/** Response from GET .../sessions/{sessionId}/transcript */
export interface TranscriptResponse {
  success: boolean;
  data: {
    session_id: string;
    entries: TranscriptEntry[];
  };
}
