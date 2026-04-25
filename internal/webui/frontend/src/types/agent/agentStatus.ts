/**
 * Workspace-scoped agent status types and LoomAgentStatus adapter.
 *
 * AgentStatusEntry / WorkspaceAgentStatusResponse alias the generated OpenAPI
 * schemas (matches the LoomCommitDetail / LoomFileChange aliasing pattern in
 * agent.ts). mapToLoomAgentStatus bridges the new daemon-enriched endpoint to
 * the existing LoomAgentStatus shape consumed by ~30 components.
 */

import type { components } from "@/types/generated/openapi";
import type { LoomAgentStatus } from "./agent";

/** Single agent entry from GET /api/workspaces/{ws}/agents/status. */
export type AgentStatusEntry = components["schemas"]["AgentStatusEntry"];

/** Top-level data envelope from the agent status endpoint. */
export type WorkspaceAgentStatusResponse =
  components["schemas"]["WorkspaceAgentStatusResponse"];

/**
 * Map an AgentStatusEntry to LoomAgentStatus.
 *
 * The endpoint computes the monitor-format status string server-side, so the
 * adapter passes it through unchanged for parseLoomStatus on the consumer side.
 * Workspace name comes from the response envelope (not the per-entry field) to
 * keep the source-of-truth single.
 *
 * Null timestamps in the wire format are dropped rather than passed through:
 * exactOptionalPropertyTypes treats a missing property and an explicit
 * undefined/null differently, and LoomAgentStatus uses optional, not nullable.
 */
export function mapToLoomAgentStatus(
  entry: AgentStatusEntry,
  workspaceName: string,
): LoomAgentStatus {
  return {
    name: entry.worktree,
    branch: entry.branch,
    status: entry.status,
    ahead: entry.ahead,
    behind: entry.behind,
    workspace: workspaceName,
    daemon_managed: true,
    cross_repo: entry.cross_repo,
    worktree_path: entry.worktree_path,
    path: entry.worktree_path,
    pid: entry.pid,
    supervisor_status: entry.supervisor_status,
    restart_count: entry.restart_count,
    yield_requested: entry.yield_requested,
    changes_count: entry.changes,
    ...(entry.role !== undefined ? { role: entry.role } : {}),
    ...(entry.repo !== undefined ? { repo: entry.repo } : {}),
    ...(entry.last_error_class !== undefined
      ? { last_error_class: entry.last_error_class }
      : {}),
    ...(entry.backoff_until != null
      ? { backoff_until: entry.backoff_until }
      : {}),
    ...(entry.stop_reason !== undefined
      ? { stop_reason: entry.stop_reason }
      : {}),
    ...(entry.task_id !== undefined ? { task_id: entry.task_id } : {}),
    ...(entry.epic_id !== undefined ? { epic_id: entry.epic_id } : {}),
    ...(entry.current_backend !== undefined
      ? { current_backend: entry.current_backend }
      : {}),
    ...(entry.remote_branch !== undefined
      ? { remote_branch: entry.remote_branch }
      : {}),
    ...(entry.yield_reason !== undefined
      ? { yield_reason: entry.yield_reason }
      : {}),
    ...(entry.yield_requested_at != null
      ? { yield_requested_at: entry.yield_requested_at }
      : {}),
    ...(entry.error !== undefined ? { collection_error: entry.error } : {}),
  };
}

/**
 * Convert a full status response to LoomAgentStatus[].
 * Uses the envelope's workspace_name as the authoritative workspace label.
 */
export function mapAgentStatusResponse(
  response: WorkspaceAgentStatusResponse,
): LoomAgentStatus[] {
  return response.agents.map((entry) =>
    mapToLoomAgentStatus(entry, response.workspace_name),
  );
}
