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
 * Based on generated MonitorAgentStatus, with workspace made optional for
 * unassigned agents and extended with state, cross_repo, path, worktree_path fields not in spec.
 */
export type LoomAgentStatus = Omit<
  components["schemas"]["MonitorAgentStatus"],
  "workspace"
> & {
  /** Workspace name from daemon config, when assigned */
  workspace?: string;
  /** Whether agent works across multiple repos */
  cross_repo?: boolean;
  /** Absolute path to the agent's working directory */
  path?: string;
  /** Absolute path to the agent's worktree */
  worktree_path?: string;
  /** Control-plane assignment state returned by the fleet-backed agents API */
  state?: string;
  /** Lead assignment delivery state returned by the fleet-backed agents API */
  delivery_state?: "pending" | "delivered" | "acknowledged" | string;
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

/**
 * Effective display status for an agent, preferring fleet-db's derived
 * live_status.
 *
 * On serve-only deployments the lock-derived `status` stays "idle"/"ready" even
 * while an agent is provably working, but fleet-db's live_status (carried on the
 * monitor response, computed there from the session+lease join) knows the truth.
 * When the raw status already encodes working/planning (daemon mode, often with a
 * duration we want to keep) it is returned verbatim. Otherwise, when live_status
 * is "working" *and* the lock-derived status is only idle-like (idle/ready/empty),
 * a "working: <task>" / "planning: <task>" string is synthesized so the existing
 * status parsing, labels, dot colors, and active-agent counters all reflect it
 * without re-deriving liveness. A more specific lock-derived status
 * (done/review/error/dirty/N changes) is NOT masked by live_status. Falls back to
 * the raw status.
 */
export function effectiveAgentStatus(agent: LoomAgentStatus): string {
  const raw = agent.status ?? "";
  if (raw.startsWith("working:") || raw.startsWith("planning:")) {
    return raw;
  }
  if (agent.live_status === "working" && isIdleLikeStatus(raw)) {
    const planning = agent.active_phase === "planning" || agent.role === "plan";
    const prefix = planning ? "planning" : "working";
    return agent.active_task_id ? `${prefix}: ${agent.active_task_id}` : prefix;
  }
  return raw;
}

/**
 * Whether a lock-derived status is "idle-like" — only idle/ready (and the
 * empty/unknown string, which parseLoomStatus maps to "ready"). These are the
 * statuses the fleet-db live_status override is allowed to replace; anything more
 * specific (done/review/error/dirty/N changes) is left untouched so a meaningful
 * badge is never masked by a (possibly stale) "working" liveness signal.
 */
function isIdleLikeStatus(raw: string): boolean {
  const { type } = parseLoomStatus(raw);
  return type === "idle" || type === "ready";
}

/**
 * Whether an agent counts as actively working/planning for the active-agent
 * counter. Shares effectiveAgentStatus + parseLoomStatus with the AgentCard badge
 * so the count and the badge can never disagree — including a bare "working" with
 * no task id, which the badge renders and this therefore also counts.
 */
export function isAgentActive(agent: LoomAgentStatus): boolean {
  const { type } = parseLoomStatus(effectiveAgentStatus(agent));
  return type === "working" || type === "planning";
}

/**
 * The agent claiming a task, by task id.
 *
 * CONTRACT: active_task_id outranks current_task_id for liveness. active_task_id
 * is fleet-db's session+lease-derived claim — set only when a FRESH lease + a
 * running session exist (expired/zombie leases are skipped server-side), so it
 * cannot be stale. current_task_id is LOCK-derived and can outlive the process
 * (the "immortal lock" pathology: a dead agent's .agent.lock persists on disk).
 * So when two DIFFERENT agents match — a live session-holder vs a stale
 * lock-holder — the live one must win. Two ordered finds (active first) make the
 * precedence deterministic regardless of array order; the original one-pass
 * `find(current || active)` let array order decide. The current_task_id fallback
 * covers the daemon path where fleet-db liveness wasn't computed (active absent).
 *
 * Single-claimant invariant: a loom task is claimed via a single lease/lock at a
 * time, so at most one agent should match. If two ever held active_task_id === T
 * (a lease-layer bug that shouldn't occur), the first by array order is returned;
 * that inconsistency belongs surfaced elsewhere, not silently tie-broken here.
 */
export function resolveAgentForTask(
  agents: readonly LoomAgentStatus[],
  taskId: string,
): LoomAgentStatus | undefined {
  return (
    agents.find((a) => a.active_task_id === taskId) ??
    agents.find((a) => a.current_task_id === taskId)
  );
}

/** The agent with the given name, or undefined. */
export function resolveAgentByName(
  agents: readonly LoomAgentStatus[],
  name: string,
): LoomAgentStatus | undefined {
  return agents.find((a) => a.name === name);
}
