/**
 * Zero-value defaults for the Loom monitor API response shapes. Used both
 * by the store (initial state) and by the API client (short-circuit return
 * when no active workspace is resolved yet).
 */

import type {
  LoomTaskSummary,
  LoomSyncInfo,
  LoomStats,
  LoomTaskLists,
} from "@/types";
import type { UsageResponse } from "@/types";

export const DEFAULT_TASKS: LoomTaskSummary = {
  needs_planning: 0,
  ready_to_implement: 0,
  in_progress: 0,
  need_review: 0,
  backlog: 0,
  epics: 0,
};

export const DEFAULT_SYNC: LoomSyncInfo = {
  db_synced: true,
  db_last_sync: "",
  git_needs_push: 0,
  git_needs_pull: 0,
};

export const DEFAULT_STATS: LoomStats = {
  open: 0,
  closed: 0,
  total: 0,
  completion: 0,
  remaining: 0,
  in_progress: 0,
  review: 0,
  blocked: 0,
};

export const DEFAULT_TASK_LISTS: LoomTaskLists = {
  needsPlanning: [],
  readyToImplement: [],
  needsReview: [],
  inProgress: [],
  backlog: [],
  done: [],
};

export const DEFAULT_USAGE: UsageResponse = {
  total_input_tokens: 0,
  total_output_tokens: 0,
  total_cache_read_tokens: 0,
  total_cache_write_tokens: 0,
  total_cost: 0,
  session_count: 0,
  by_agent: [],
  by_backend: [],
  daily_costs: [],
  sessions: [],
  timestamp: "",
} as unknown as UsageResponse;
