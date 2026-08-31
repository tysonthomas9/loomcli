/**
 * Types, constants, and pure helper functions for the issue store.
 * Extracted from issueStore.ts to stay within LOC limits.
 */

import { produce } from "immer";

import type {
  ConnectionState,
  MutationPayload,
  MutationType,
} from "../api/common";
import type { GraphFilter } from "../api/issues";
import type { Issue, WorkFilter, Status } from "../types";
import { ApiError } from "@/types/common";
import {
  MutationCreate,
  MutationUpdate,
  MutationDelete,
  MutationStatus,
  MutationBonded,
  MutationRefresh,
} from "@/types/workspace";

/**
 * Extract a user-facing error message from a fetch failure.
 *
 * Prefers the server's original body.error text over the generic HTTP
 * status text baked into ApiError.message, so UI branches that key off
 * server-authored phrases (e.g. IssueViewGuard's "workspace is loading"
 * check that switches to the loading-spinner variant) actually receive
 * the text the server sent.
 */
export function extractErrorMessage(err: unknown): string {
  if (err instanceof ApiError) {
    const body = err.body;
    if (body && typeof body === "object") {
      const { error } = body as { error?: unknown };
      if (typeof error === "string" && error.length > 0) {
        return error;
      }
    }
    return err.message;
  }
  if (err instanceof Error) {
    return err.message;
  }
  return "Failed to fetch issues";
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

export const TOO_FAR_BEHIND_THRESHOLD = 3;
export const MAX_RECONNECT_ATTEMPTS = 10;
export const STALE_BANNER_DELAY_MS = 5_000;
export const AUTO_ROLLBACK_TIMEOUT_MS = 30_000;
export const REFRESH_DEBOUNCE_MS = 1_000;
export const MAX_PROJECTION_REFRESH_WAIT_MS = 5_000;
export const MAX_AUTO_RETRIES = 5;
export const RETRY_BASE_DELAY_MS = 1_000;
export const RETRY_MAX_DELAY_MS = 16_000;

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

export interface FetchIssuesParams {
  workspaceId: string;
  mode: "ready" | "graph" | "kanban";
  filter?: WorkFilter;
  graphFilter?: GraphFilter;
  sourceRepos?: string[];
  signal?: AbortSignal;
  /**
   * Signals that this fetch is an automatic retry — skip resetting retryCount
   * at the start of the call. Defaults to false for user-initiated or
   * view-switch fetches, which reset retry state.
   */
  isAutoRetry?: boolean;
}

export interface IssueStoreConfig {
  onToast?: (
    message: string,
    options?: { type?: string; duration?: number },
  ) => void;
  retryConnectionFn?: () => void;
}

export type SubscribeFn = (
  callback: (mutation: MutationPayload) => void,
  options?: { types?: MutationType[] },
) => () => void;

export interface IssueStoreState {
  issuesMap: Map<string, Issue>;
  isLoading: boolean;
  error: string | null;
  /** Current auto-retry attempt: 0 = no retries, 1 = first retry, etc. */
  retryCount: number;
  /** Timestamp (ms) when next auto-retry fires, or null if not retrying. */
  nextRetryAt: number | null;
  connectionState: ConnectionState;
  reconnectAttempts: number;
  lastEventId: number | undefined;
  showStaleBanner: boolean;
  connectionLost: boolean;
  disconnectedSince: number | null;
  pendingIds: Set<string>;
  mutationCount: number;
}

export interface IssueStoreActions {
  fetchIssues: (params: FetchIssuesParams) => Promise<void>;
  refetch: () => Promise<void>;
  connectToEvents: (subscribe: SubscribeFn) => () => void;
  applyMutation: (mutation: MutationPayload) => void;
  updateIssueStatus: (
    issueId: string,
    newStatus: Status,
    workspaceId: string,
  ) => Promise<void>;
  setConnectionState: (state: ConnectionState) => void;
  setReconnectAttempts: (attempts: number) => void;
  setLastEventId: (id: number | undefined) => void;
  retryConnection: () => void;
  getIssue: (id: string) => Issue | undefined;
  reset: () => void;
  configure: (config: IssueStoreConfig) => void;
}

export type IssueStore = IssueStoreState & IssueStoreActions;

export interface OptimisticEntry {
  snapshot: Issue;
  bufferedMutations: MutationPayload[];
  timeoutId: ReturnType<typeof setTimeout>;
}

// ---------------------------------------------------------------------------
// Initial state
// ---------------------------------------------------------------------------

export const INITIAL_STATE: IssueStoreState = {
  issuesMap: new Map(),
  isLoading: false,
  error: null,
  retryCount: 0,
  nextRetryAt: null,
  connectionState: "disconnected",
  reconnectAttempts: 0,
  lastEventId: undefined,
  showStaleBanner: false,
  connectionLost: false,
  disconnectedSince: null,
  pendingIds: new Set(),
  mutationCount: 0,
};

// ---------------------------------------------------------------------------
// Pure utility: issuesAreEqual
// ---------------------------------------------------------------------------

export function issuesAreEqual(a: Issue, b: Issue): boolean {
  return jsonishEqual(a, b);
}

/**
 * Keep a newer live issue while accepting the Kanban endpoint's authoritative
 * derived fields. SSE mutations do not carry these projection fields, so
 * retaining them from the live object can pin an issue in its former column.
 */
export function mergeKanbanProjection(current: Issue, fetched: Issue): Issue {
  const projectionsMatch =
    current.is_blocked === fetched.is_blocked &&
    current.is_ready === fetched.is_ready &&
    current.is_deferred === fetched.is_deferred &&
    current.blocked_by_count === fetched.blocked_by_count &&
    jsonishEqual(current.blocked_by, fetched.blocked_by) &&
    jsonishEqual(current.blocked_by_details, fetched.blocked_by_details);

  if (projectionsMatch) return current;

  const {
    is_blocked: _isBlocked,
    is_ready: _isReady,
    is_deferred: _isDeferred,
    blocked_by_count: _blockedByCount,
    blocked_by: _blockedBy,
    blocked_by_details: _blockedByDetails,
    ...currentWithoutProjection
  } = current;

  return { ...fetched, ...currentWithoutProjection };
}

function jsonishEqual(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (a === null || b === null) return a === b;
  if (typeof a !== "object" || typeof b !== "object") {
    return Object.is(a, b);
  }

  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b)) return false;
    if (a.length !== b.length) return false;
    for (let i = 0; i < a.length; i++) {
      if (!jsonishEqual(a[i], b[i])) return false;
    }
    return true;
  }

  const left = a as Record<string, unknown>;
  const right = b as Record<string, unknown>;
  const leftKeys = Object.keys(left).filter((key) => left[key] !== undefined);
  const rightKeys = Object.keys(right).filter(
    (key) => right[key] !== undefined,
  );
  if (leftKeys.length !== rightKeys.length) return false;

  for (const key of leftKeys) {
    if (!Object.prototype.hasOwnProperty.call(right, key)) return false;
    if (!jsonishEqual(left[key], right[key])) return false;
  }

  return true;
}

// ---------------------------------------------------------------------------
// Pure mutation helpers (ported from useMutationHandler.ts)
// ---------------------------------------------------------------------------

export function isStaleMutation(
  mutation: MutationPayload,
  issue: Issue,
): boolean {
  const mutationTime = Date.parse(mutation.timestamp);
  const issueTime = Date.parse(issue.updated_at);
  if (isNaN(mutationTime) || isNaN(issueTime)) return true;
  return mutationTime < issueTime;
}

function createIssueFromMutation(mutation: MutationPayload): Issue {
  const now = mutation.timestamp;
  const issueId = mutation.issue_id ?? "";
  const issue: Issue = {
    id: issueId,
    title: mutation.title ?? "Untitled",
    priority: 2,
    created_at: now,
    updated_at: now,
  };
  if (mutation.assignee != null) issue.assignee = mutation.assignee;
  if (mutation.new_status != null) issue.status = mutation.new_status as Status;
  if (mutation.source_repo != null) issue.repo = mutation.source_repo;
  return issue;
}

function applyUpdateToIssue(issue: Issue, mutation: MutationPayload): Issue {
  return produce(issue, (draft) => {
    draft.updated_at = mutation.timestamp;
    if (mutation.title != null) draft.title = mutation.title;
    if (mutation.assignee != null) draft.assignee = mutation.assignee;
    if (mutation.new_status != null)
      draft.status = mutation.new_status as Status;
    if (
      mutation.priority != null &&
      mutation.priority >= 0 &&
      mutation.priority <= 4
    ) {
      draft.priority = mutation.priority as typeof draft.priority;
    }
    if (mutation.source_repo != null && !draft.repo)
      draft.repo = mutation.source_repo;
  });
}

function applyStatusToIssue(issue: Issue, mutation: MutationPayload): Issue {
  return produce(issue, (draft) => {
    draft.updated_at = mutation.timestamp;
    if (mutation.new_status != null)
      draft.status = mutation.new_status as Status;
  });
}

function applyBondedToIssue(issue: Issue, mutation: MutationPayload): Issue {
  return produce(issue, (draft) => {
    draft.updated_at = mutation.timestamp;
  });
}

const ISSUE_PROJECTION_ENTITY_TYPES = new Set([
  "issue",
  "dependency",
  "dep",
  "comment",
  "label",
]);

// ---------------------------------------------------------------------------
// processMutation: pure function returning a result instead of calling set()
// ---------------------------------------------------------------------------

export interface MutationResult {
  /** New issues map, or null if no change */
  newMap: Map<string, Issue> | null;
  /** Whether mutation count should be incremented */
  incrementCount: boolean;
  /** Issue ID to track as deleted during fetch, or null */
  trackDeletion: string | null;
  /** Whether a debounced refetch should be scheduled */
  scheduleRefresh: boolean;
}

export function issueMutationInvalidatesProjection(
  mutation: MutationPayload,
): boolean {
  if (mutation.entity_type != null && mutation.entity_type !== "") {
    return ISSUE_PROJECTION_ENTITY_TYPES.has(mutation.entity_type);
  }

  return (
    mutation.type === MutationRefresh ||
    (typeof mutation.issue_id === "string" && mutation.issue_id.length > 0)
  );
}

export function issueMutationAppliesToLocalIssue(
  mutation: MutationPayload,
): boolean {
  return mutation.entity_type == null || mutation.entity_type === "issue";
}

/**
 * Process a single mutation against an issue map.
 * Returns the result without side effects.
 */
export function processMutation(
  issuesMap: Map<string, Issue>,
  mutation: MutationPayload,
  isFetchingFlag: boolean,
): MutationResult {
  const { issue_id, type } = mutation;

  // Refresh handling
  if (type === MutationRefresh) {
    return {
      newMap: null,
      incrementCount: true,
      trackDeletion: null,
      scheduleRefresh: true,
    };
  }

  // Empty issue_id guard
  if (!issue_id) {
    return {
      newMap: null,
      incrementCount: false,
      trackDeletion: null,
      scheduleRefresh: false,
    };
  }

  // Create mutation
  if (type === MutationCreate) {
    const existing = issuesMap.get(issue_id);
    if (existing) {
      if (isStaleMutation(mutation, existing)) {
        return {
          newMap: null,
          incrementCount: false,
          trackDeletion: null,
          scheduleRefresh: false,
        };
      }
      const updated = applyUpdateToIssue(existing, mutation);
      const newMap = new Map(issuesMap);
      newMap.set(issue_id, updated);
      return {
        newMap,
        incrementCount: true,
        trackDeletion: null,
        scheduleRefresh: false,
      };
    }
    const newIssue = createIssueFromMutation(mutation);
    const newMap = new Map(issuesMap);
    newMap.set(issue_id, newIssue);
    return {
      newMap,
      incrementCount: true,
      trackDeletion: null,
      scheduleRefresh: false,
    };
  }

  // Delete mutation
  if (type === MutationDelete) {
    const existing = issuesMap.get(issue_id);
    if (!existing) {
      return {
        newMap: null,
        incrementCount: false,
        trackDeletion: null,
        scheduleRefresh: false,
      };
    }
    if (isStaleMutation(mutation, existing)) {
      return {
        newMap: null,
        incrementCount: false,
        trackDeletion: null,
        scheduleRefresh: false,
      };
    }
    const newMap = new Map(issuesMap);
    newMap.delete(issue_id);
    return {
      newMap,
      incrementCount: true,
      trackDeletion: isFetchingFlag ? issue_id : null,
      scheduleRefresh: false,
    };
  }

  // For all other mutations, issue must exist
  const existing = issuesMap.get(issue_id);
  if (!existing) {
    return {
      newMap: null,
      incrementCount: false,
      trackDeletion: null,
      scheduleRefresh: false,
    };
  }
  if (isStaleMutation(mutation, existing)) {
    return {
      newMap: null,
      incrementCount: false,
      trackDeletion: null,
      scheduleRefresh: false,
    };
  }

  let updated: Issue;
  switch (type) {
    case MutationUpdate:
      updated = applyUpdateToIssue(existing, mutation);
      break;
    case MutationStatus:
      updated = applyStatusToIssue(existing, mutation);
      break;
    case MutationBonded:
      updated = applyBondedToIssue(existing, mutation);
      break;
    default:
      updated = produce(existing, (draft) => {
        draft.updated_at = mutation.timestamp;
      });
      break;
  }

  const newMap = new Map(issuesMap);
  newMap.set(issue_id, updated);
  return {
    newMap,
    incrementCount: true,
    trackDeletion: null,
    scheduleRefresh: false,
  };
}
