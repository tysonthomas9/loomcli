/**
 * Agent-related types.
 */

/**
 * Agent state values.
 * Maps to Go types.AgentState.
 */
export type AgentState =
  | 'idle'
  | 'spawning'
  | 'running'
  | 'working'
  | 'stuck'
  | 'done'
  | 'stopped'
  | 'dead'
  | '';

/**
 * Agent state constants.
 */
export const StateIdle: AgentState = 'idle';
export const StateSpawning: AgentState = 'spawning';
export const StateRunning: AgentState = 'running';
export const StateWorking: AgentState = 'working';
export const StateStuck: AgentState = 'stuck';
export const StateDone: AgentState = 'done';
export const StateStopped: AgentState = 'stopped';
export const StateDead: AgentState = 'dead';

/**
 * Molecule type for swarm coordination.
 * Maps to Go types.MolType.
 */
export type MolType = 'swarm' | 'patrol' | 'work' | '';

/**
 * MolType constants.
 */
export const MolTypeSwarm: MolType = 'swarm';
export const MolTypePatrol: MolType = 'patrol';
export const MolTypeWork: MolType = 'work';

/**
 * Work type for assignment models.
 * Maps to Go types.WorkType.
 */
export type WorkType = 'mutex' | 'open_competition' | '';

/**
 * WorkType constants.
 */
export const WorkTypeMutex: WorkType = 'mutex';
export const WorkTypeOpenCompetition: WorkType = 'open_competition';

// ============================================================================
// Loom Server Types
// Types for agent status from loom server API.
// ============================================================================

/**
 * Connection state for the loom server.
 * Used to provide appropriate UI feedback for different connection scenarios.
 */
export type LoomConnectionState =
  | 'never_connected' // Initial state before first successful fetch
  | 'connected' // Healthy connection
  | 'disconnected' // Lost connection (may have cached data)
  | 'reconnecting'; // Actively trying to reconnect

/**
 * Agent status from the loom server.
 */
export interface LoomAgentStatus {
  /** Worktree name (e.g., "nova", "falcon") */
  name: string;
  /** Current git branch */
  branch: string;
  /** Status string (e.g., "ready", "working: bd-123 (5m)", "planning: bd-456 (2m)") */
  status: string;
  /** Commits ahead of integration branch */
  ahead: number;
  /** Commits behind integration branch */
  behind: number;
}

/**
 * Parsed agent status for display.
 */
export interface ParsedLoomStatus {
  /** The raw status type */
  type:
    | 'ready'
    | 'working'
    | 'planning'
    | 'done'
    | 'review'
    | 'idle'
    | 'error'
    | 'dirty'
    | 'changes';
  /** Task ID if working on a task */
  taskId?: string;
  /** Duration string (e.g., "5m", "2h30m") */
  duration?: string;
  /** Number of uncommitted changes */
  changeCount?: number;
}

/**
 * Response from GET /api/agents on loom server
 */
export interface LoomAgentsResponse {
  agents: LoomAgentStatus[] | null;
  timestamp: string;
}

/**
 * Task info from loom server.
 */
export interface LoomTaskInfo {
  id: string;
  title: string;
  priority: number;
  status: string;
}

/**
 * Task summary counts from loom server (API response format).
 * Note: API uses "backlog" field which is mapped to "blocked" for frontend use.
 */
export interface LoomTaskSummaryRaw {
  needs_planning: number;
  ready_to_implement: number;
  in_progress: number;
  need_review: number;
  backlog: number; // API field name for blocked tasks
}

/**
 * Task summary counts (frontend format).
 * Used after mapping from API response.
 */
export interface LoomTaskSummary {
  needs_planning: number;
  ready_to_implement: number;
  in_progress: number;
  need_review: number;
  blocked: number; // Mapped from API field "backlog"
}

/**
 * Sync status from loom server.
 */
export interface LoomSyncInfo {
  db_synced: boolean;
  db_last_sync: string;
  db_error?: string;
  git_needs_push: number;
  git_needs_pull: number;
}

/**
 * Statistics from loom server.
 */
export interface LoomStats {
  open: number;
  closed: number;
  total: number;
  completion: number;
}

/**
 * Full status response from GET /api/status on loom server.
 * Uses raw API types; callers should map to frontend types.
 */
export interface LoomStatusResponse {
  agents: LoomAgentStatus[] | null;
  tasks: LoomTaskSummaryRaw;
  in_progress_list: LoomTaskInfo[] | null;
  agent_tasks: Record<string, LoomTaskInfo> | null;
  stats: LoomStats;
  sync: LoomSyncInfo;
  timestamp: string;
}

/**
 * Task info keyed by agent name (from AgentTasks map).
 */
export type LoomAgentTasks = Record<string, LoomTaskInfo>;

/**
 * Response from GET /api/tasks on loom server.
 * Contains task lists organized by status category.
 */
export interface LoomTasksResponse {
  summary: LoomTaskSummary;
  needs_planning: LoomTaskInfo[] | null;
  ready_to_implement: LoomTaskInfo[] | null;
  needs_review: LoomTaskInfo[] | null;
  in_progress: LoomTaskInfo[] | null;
  backlog: LoomTaskInfo[] | null; // API sends "backlog" (contains blocked tasks)
  timestamp: string;
}

/**
 * Task lists organized by category for UI display.
 */
export interface LoomTaskLists {
  needsPlanning: LoomTaskInfo[];
  readyToImplement: LoomTaskInfo[];
  needsReview: LoomTaskInfo[];
  inProgress: LoomTaskInfo[];
  blocked: LoomTaskInfo[];
}

/**
 * Parse loom status string into structured data.
 * Examples:
 * - "ready" -> { type: "ready" }
 * - "working: bd-123 (5m)" -> { type: "working", taskId: "bd-123", duration: "5m" }
 * - "2 changes" -> { type: "changes", changeCount: 2 }
 */
export function parseLoomStatus(status: string): ParsedLoomStatus {
  // Check for "X changes" pattern
  const changesMatch = status.match(/^(\d+)\s+changes?$/);
  if (changesMatch && changesMatch[1] !== undefined) {
    return { type: 'changes', changeCount: parseInt(changesMatch[1], 10) };
  }

  // Check for "dirty"
  if (status === 'dirty') {
    return { type: 'dirty' };
  }

  // Check for "ready"
  if (status === 'ready') {
    return { type: 'ready' };
  }

  // Check for status with task ID and duration
  // Pattern: "working: bd-123 (5m)" or "planning: ... (2m)"
  const taskMatch = status.match(
    /^(working|planning|done|review|error|idle):\s*(.+?)?\s*\(([^)]+)\)$/
  );
  if (taskMatch && taskMatch[1] !== undefined && taskMatch[3] !== undefined) {
    const type = taskMatch[1] as ParsedLoomStatus['type'];
    const taskId = taskMatch[2]?.trim();
    const duration = taskMatch[3];
    const result: ParsedLoomStatus = { type, duration };
    if (taskId && taskId !== '...') {
      result.taskId = taskId;
    }
    return result;
  }

  // Fallback: just extract the type
  const typeMatch = status.match(/^(working|planning|done|review|error|idle)/);
  if (typeMatch && typeMatch[1] !== undefined) {
    return { type: typeMatch[1] as ParsedLoomStatus['type'] };
  }

  // Unknown status - treat as ready
  return { type: 'ready' };
}
