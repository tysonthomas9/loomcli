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

import type { EvalScores } from "./evals";

/** Lifecycle state of a session. */
export type SessionStatus = "running" | "completed" | "failed" | "aborted";

/** Raw control-plane status values accepted by workspace session filters. */
export type WorkspaceSessionStatusFilter =
  | "queued"
  | "leased"
  | "starting"
  | "running"
  | "idle"
  | "yielded"
  | "completed"
  | "failed"
  | "cancelled"
  | "expired";

/** Workspace-scoped agent session kind values. */
export type WorkspaceSessionKind =
  | "task"
  | "orchestration"
  | "terminal"
  | "maintenance"
  | "ad_hoc"
  | "judge";

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

export type WorkspaceSessionListItem = SessionRecord & {
  kind?: WorkspaceSessionKind;
  /** First-class task-plane run linkage; empty for daemon sessions. */
  task_run_id?: string;
  /** First-class task-plane invocation key; empty for legacy/daemon sessions. */
  invocation_key?: string;
  /** First-class task-plane attempt; absent for legacy/daemon sessions. */
  attempt?: number;
  /** Leaf-declared tags used by the Traces AND filter. */
  tags?: string[];
  /** eval_status metadata stamp (absent = never judged). */
  eval_status?: "done" | "failed";
  /** Scores joined from the session's newest eval record. */
  eval_scores?: EvalScores;
  /** Explicit subject-to-judge link from the displayed eval record. */
  judge_session_id?: string;
  /** Explicit judge-to-subject link for judge session details. */
  judged_session_id?: string;
};

export interface WorkspaceSessionFilters {
  since?: string;
  until?: string;
  status?: WorkspaceSessionStatusFilter;
  agent_id?: string;
  kind?: WorkspaceSessionKind;
  task_run_id?: string;
  tags?: string[];
  limit?: number;
}

/** Response data from GET /api/workspaces/{ws}/sessions */
export interface WorkspaceSessionListData {
  sessions: WorkspaceSessionListItem[];
  total: number;
  limit: number;
  /** Sorted union over the full server-filtered range, not just this page. */
  score_dimensions: string[];
}

/** Response from GET /api/workspaces/{ws}/sessions */
export interface WorkspaceSessionListResponse {
  success: boolean;
  data: WorkspaceSessionListData;
  error?: string;
}

export interface TraceTaskRun {
  workspace_key: string;
  task_run_id: string;
  task_id: string;
  status: string;
  input_tokens?: number;
  output_tokens?: number;
  cache_read_tokens?: number;
  cache_write_tokens?: number;
  started_at?: string;
  finished_at?: string | null;
  created_at: string;
  updated_at: string;
}

/** Response data for GET /api/workspaces/{ws}/traces/runs/{taskRunId}. */
export interface WorkspaceTraceRunData {
  task_run_id: string;
  task_run?: TraceTaskRun;
  task_run_missing: boolean;
  task_id?: string;
  attempt_count: number;
  files_changed: number;
  total_tokens: number;
  duration_seconds: number;
  sessions: WorkspaceSessionListItem[];
}

export interface WorkspaceTraceRunResponse {
  success: boolean;
  data: WorkspaceTraceRunData;
  error?: string;
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
