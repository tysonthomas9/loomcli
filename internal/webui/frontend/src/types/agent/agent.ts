/**
 * Agent-related types.
 * Monitor/loom server types aliased from generated OpenAPI schemas where possible.
 * LoomCommitDetail → MonitorCommitDetail, LoomFileChange → MonitorFileChange,
 * LoomTaskInfo → MonitorTaskInfo, LoomStats → MonitorStats,
 * WorktreeSyncDetail → MonitorWorktreeSyncDetail, LoomSyncInfo → MonitorSyncInfo.
 * LoomAgentStatus extends MonitorAgentStatus with cross_repo, path, worktree_path fields not in spec.
 * LoomTaskSummary extends MonitorTaskSummary with epics field from spec.
 * LoomStatusResponse, LoomAgentsResponse, LoomTasksResponse aliased from generated schemas.
 * Runtime constants, ParsedLoomStatus, LoomConnectionState, parseLoomStatus kept hand-written.
 */

import type { components } from "@/types/generated/openapi";

/**
 * Agent state values.
 * Maps to Go types.AgentState.
 */
export type AgentState =
  | "idle"
  | "spawning"
  | "running"
  | "working"
  | "stuck"
  | "done"
  | "stopped"
  | "dead"
  | "";

/**
 * Agent state constants.
 */
export const StateIdle: AgentState = "idle";
export const StateSpawning: AgentState = "spawning";
export const StateRunning: AgentState = "running";
export const StateWorking: AgentState = "working";
export const StateStuck: AgentState = "stuck";
export const StateDone: AgentState = "done";
export const StateStopped: AgentState = "stopped";
export const StateDead: AgentState = "dead";

/**
 * Molecule type for swarm coordination.
 * Maps to Go types.MolType.
 */
export type MolType = "swarm" | "patrol" | "work" | "";

/**
 * MolType constants.
 */
export const MolTypeSwarm: MolType = "swarm";
export const MolTypePatrol: MolType = "patrol";
export const MolTypeWork: MolType = "work";

/**
 * Work type for assignment models.
 * Maps to Go types.WorkType.
 */
export type WorkType = "mutex" | "open_competition" | "";

/**
 * WorkType constants.
 */
export const WorkTypeMutex: WorkType = "mutex";
export const WorkTypeOpenCompetition: WorkType = "open_competition";

// ============================================================================
// Loom Server Types
// Types for agent status from loom server API.
// ============================================================================

/**
 * Connection state for the loom server.
 * Used to provide appropriate UI feedback for different connection scenarios.
 */
export type LoomConnectionState =
  | "never_connected" // Initial state before first successful fetch
  | "connected" // Healthy connection
  | "disconnected" // Lost connection (may have cached data)
  | "reconnecting"; // Actively trying to reconnect

/**
 * Commit detail from the loom server.
 * Aliased from generated MonitorCommitDetail schema.
 */
export type LoomCommitDetail = components["schemas"]["MonitorCommitDetail"];

/**
 * File change from git status.
 * Aliased from generated MonitorFileChange schema.
 */
export type LoomFileChange = components["schemas"]["MonitorFileChange"];

/**
 * Agent status from the loom server.
 * Based on generated MonitorAgentStatus, with workspace made optional (legacy/single-repo mode
 * sends empty string or omits it) and extended with cross_repo, path, worktree_path fields not in spec.
 */
export type LoomAgentStatus = Omit<
  components["schemas"]["MonitorAgentStatus"],
  "workspace"
> & {
  /** Workspace name from daemon config (empty string in legacy/single-repo mode) */
  workspace?: string;
  /** Whether agent works across multiple repos */
  cross_repo?: boolean;
  /** Absolute path to the agent's working directory */
  path?: string;
  /** Absolute path to the agent's worktree */
  worktree_path?: string;
};

/**
 * Parsed agent status for display.
 */
export interface ParsedLoomStatus {
  /** The raw status type */
  type:
    | "ready"
    | "working"
    | "planning"
    | "done"
    | "review"
    | "idle"
    | "error"
    | "dirty"
    | "changes";
  /** Task ID if working on a task */
  taskId?: string;
  /** Duration string (e.g., "5m", "2h30m") */
  duration?: string;
  /** Number of uncommitted changes */
  changeCount?: number;
}

/**
 * Response from GET /api/agents on loom server.
 * Aliased from generated MonitorAgentsResponse schema.
 * Drift: generated has workspace field and non-null agents; hand-written had nullable agents.
 */
export type LoomAgentsResponse = components["schemas"]["MonitorAgentsResponse"];

/**
 * Task info from loom server.
 * Aliased from generated MonitorTaskInfo schema.
 */
export type LoomTaskInfo = components["schemas"]["MonitorTaskInfo"];

/**
 * Task summary counts from loom server.
 * Aliased from generated MonitorTaskSummary schema.
 * Drift: generated has additional epics field.
 */
export type LoomTaskSummary = components["schemas"]["MonitorTaskSummary"];

/**
 * Per-worktree sync detail (commits ahead or behind).
 * Aliased from generated MonitorWorktreeSyncDetail schema.
 */
export type WorktreeSyncDetail =
  components["schemas"]["MonitorWorktreeSyncDetail"];

/**
 * Sync status from loom server.
 * Aliased from generated MonitorSyncInfo schema.
 */
export type LoomSyncInfo = components["schemas"]["MonitorSyncInfo"];

/**
 * Statistics from loom server.
 * Aliased from generated MonitorStats schema.
 */
export type LoomStats = components["schemas"]["MonitorStats"];

/**
 * Full status response from GET /api/status on loom server.
 * Aliased from generated MonitorStatusResponse schema.
 * Drift: generated has workspace field not in hand-written type.
 */
export type LoomStatusResponse = components["schemas"]["MonitorStatusResponse"];

/**
 * Task info keyed by agent name (from AgentTasks map).
 */
export type LoomAgentTasks = Record<string, LoomTaskInfo>;

/**
 * Response from GET /api/tasks on loom server.
 * Aliased from generated MonitorTasksResponse schema.
 */
export type LoomTasksResponse = components["schemas"]["MonitorTasksResponse"];

/**
 * Task lists organized by category for UI display.
 */
export interface LoomTaskLists {
  needsPlanning: LoomTaskInfo[];
  readyToImplement: LoomTaskInfo[];
  needsReview: LoomTaskInfo[];
  inProgress: LoomTaskInfo[];
  backlog: LoomTaskInfo[];
  done: LoomTaskInfo[];
}

/**
 * Parse loom status string into structured data.
 * Examples:
 * - "ready" -> { type: "ready" }
 * - "working: loomcli-123 (5m)" -> { type: "working", taskId: "loomcli-123", duration: "5m" }
 * - "2 changes" -> { type: "changes", changeCount: 2 }
 */
export function parseLoomStatus(status: string): ParsedLoomStatus {
  // Check for "X changes" pattern
  const changesMatch = status.match(/^(\d+)\s+changes?$/);
  if (changesMatch && changesMatch[1] !== undefined) {
    return { type: "changes", changeCount: parseInt(changesMatch[1], 10) };
  }

  // Check for "dirty"
  if (status === "dirty") {
    return { type: "dirty" };
  }

  // Check for "ready"
  if (status === "ready") {
    return { type: "ready" };
  }

  // Check for status with task ID and duration
  // Pattern: "working: loomcli-123 (5m)" or "planning: ... (2m)"
  const taskMatch = status.match(
    /^(working|planning|done|review|error|idle):\s*(.+?)?\s*\(([^)]+)\)$/,
  );
  if (taskMatch && taskMatch[1] !== undefined && taskMatch[3] !== undefined) {
    const type = taskMatch[1] as ParsedLoomStatus["type"];
    const taskId = taskMatch[2]?.trim();
    const duration = taskMatch[3];
    const result: ParsedLoomStatus = { type, duration };
    if (taskId && taskId !== "...") {
      result.taskId = taskId;
    }
    return result;
  }

  // Fallback: just extract the type
  const typeMatch = status.match(/^(working|planning|done|review|error|idle)/);
  if (typeMatch && typeMatch[1] !== undefined) {
    return { type: typeMatch[1] as ParsedLoomStatus["type"] };
  }

  // Unknown status - treat as ready
  return { type: "ready" };
}
