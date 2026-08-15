/**
 * TypeScript types for session audit trail data.
 *
 * SessionRecord is aliased from the generated OpenAPI schema.
 *
 * TranscriptEntry is hand-written as the canonical backend-agnostic event
 * shape emitted by internal/sessions/transcript.Event. The Go side parses
 * Claude Code / Codex / OpenCode native JSONL into this uniform shape before
 * returning it on the session transcript endpoint.
 */

import type { components } from "@/types/generated/openapi";

/** Lifecycle state of a session. */
export type SessionStatus = "running" | "completed" | "failed" | "aborted";

/** A single agent session record. Aliased from generated SessionResponse schema. */
export type SessionRecord = components["schemas"]["SessionResponse"];

/** Canonical role values in the transcript event stream. */
export type TranscriptEntryRole = "user" | "assistant" | "tool" | "system";

/** Canonical event type values in the transcript event stream. */
export type TranscriptEntryType =
  | "text"
  | "reasoning"
  | "tool_use"
  | "tool_result"
  | "result"
  | "session_meta";

/**
 * A single entry (event) in a session transcript. Matches the Go
 * transcript.Event wire format. Entries are emitted in monotonic seq order
 * from the captured native JSONL.
 */
export interface TranscriptEntry {
  seq: number;
  timestamp?: string;
  role: TranscriptEntryRole;
  type: TranscriptEntryType;
  text?: string;
  tool_name?: string;
  tool_use_id?: string;
  /** Raw JSON of the tool's arguments (varies per tool). */
  tool_input?: unknown;
  /** tool_result content (plain text). */
  output?: string;
  /** Native message UUID when the backend provides one (Claude Code). */
  uuid?: string;
}

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

/** Response from GET .../sessions/{sessionId}/transcript
 *  or          GET .../sessions/{sessionId}/subagents/{subagentId}/transcript
 */
export interface TranscriptResponse {
  success: boolean;
  data: {
    session_id: string;
    entries: TranscriptEntry[];
  };
}

/** Response from GET .../sessions/{sessionId}/subagents */
export interface SubagentListResponse {
  success: boolean;
  data: {
    session_id: string;
    subagent_ids: string[];
  };
}
