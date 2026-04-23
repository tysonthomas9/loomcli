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
  /**
   * Monotonic counter incremented on every reset(). Consumers that drive
   * fetches (e.g. App.tsx's fetchIssues effect) include this in their effect
   * deps so a reset that lands after the initial fetch (React fires child
   * effects before parent effects) triggers a follow-up fetch — without this,
   * the parent's reset aborts the child's fetch and the store is stuck in
   * INITIAL_STATE forever.
   */
  resetGeneration: number;
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
  resetGeneration: 0,
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
  if (a.id !== b.id) return false;
  if (a.updated_at !== b.updated_at) return false;
  if (a.title !== b.title) return false;
  if (a.status !== b.status) return false;
  if (a.priority !== b.priority) return false;
  if (a.assignee !== b.assignee) return false;
  if (a.issue_type !== b.issue_type) return false;
  if (a.owner !== b.owner) return false;
  const aLabels = a.labels;
  const bLabels = b.labels;
  if (aLabels !== bLabels) {
    if (!aLabels || !bLabels) return false;
    if (aLabels.length !== bLabels.length) return false;
    for (let i = 0; i < aLabels.length; i++) {
      if (aLabels[i] !== bLabels[i]) return false;
    }
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

/**
 * Embed the backend's lightweight issue object into an existing Issue, merging
 * the mutation timestamp and preserving fields the lightweight payload omits
 * (repo is a frontend alias for source_repo that the backend doesn't carry on
 * a lightweight serialization). Returns the merged issue for wholesale
 * replacement.
 */
function mergeEmbeddedIssue(
  existing: Issue | undefined,
  embedded: Issue,
  mutation: MutationPayload,
): Issue {
  const merged: Issue = {
    ...(existing ?? ({} as Issue)),
    ...embedded,
    // id and updated_at come from the mutation envelope, which is guaranteed
    // to carry both. Guards against a partially-populated embedded payload
    // leaving the merged issue without an id (the store entry would
    // otherwise be unreachable by getIssue).
    id: mutation.issue_id || embedded.id,
    updated_at: mutation.timestamp,
  };
  if (mutation.source_repo != null && !merged.repo) {
    merged.repo = mutation.source_repo;
  }
  return merged;
}

function createIssueFromMutation(mutation: MutationPayload): Issue {
  if (mutation.issue) {
    return mergeEmbeddedIssue(undefined, mutation.issue, mutation);
  }
  const now = mutation.timestamp;
  const issue: Issue = {
    id: mutation.issue_id,
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

/**
 * Apply any issue-level mutation (update, status, bonded, etc.) to an existing
 * issue. Prefers the embedded `issue` payload for wholesale replacement — the
 * single code path that fixes the drift where applyStatusToIssue silently
 * dropped non-status fields like priority and assignee. Falls back to
 * per-field apply for backwards compatibility with older daemons.
 */
function applyMutationToIssue(issue: Issue, mutation: MutationPayload): Issue {
  if (mutation.issue) {
    return mergeEmbeddedIssue(issue, mutation.issue, mutation);
  }
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
      const updated = applyMutationToIssue(existing, mutation);
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
    case MutationStatus:
      updated = applyMutationToIssue(existing, mutation);
      break;
    case MutationBonded:
      updated = produce(existing, (draft) => {
        draft.updated_at = mutation.timestamp;
      });
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
