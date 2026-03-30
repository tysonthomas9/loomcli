/**
 * TypeScript types for session audit trail data.
 * Mirrors Go types from internal/sessions/ (SessionRecord, TranscriptEntry, etc.)
 */

/** Lifecycle state of a session. */
export type SessionStatus = "running" | "completed" | "failed" | "aborted";

/** A single agent session record. */
export interface SessionRecord {
  id: string;
  agent_name: string;
  backend: string;
  model?: string;
  phase?: string; // "planning" | "implementation"
  status: SessionStatus;
  started_at: string;
  ended_at?: string;
  duration_s?: number;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  estimated_cost_usd: number;
  exit_code: number;
  files_changed: number;
  lines_added: number;
  lines_removed: number;
  files_touched?: string[];
  attempt_num: number;
  error_class?: string;
  has_transcript: boolean;
  has_diff: boolean;
  is_active: boolean;
}

/** A single entry in a session transcript. */
export interface TranscriptEntry {
  seq: number;
  ts: string;
  role: "user" | "assistant" | "system" | "tool";
  type: string;
  content?: string;
  tool_name?: string;
  tool_input?: string;
  raw?: string;
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

/** Response from GET /api/workspaces/{ws}/tasks/{taskId}/sessions/{sessionId}/transcript */
export interface TranscriptResponse {
  success: boolean;
  data: {
    session_id: string;
    entries: TranscriptEntry[];
  };
}
