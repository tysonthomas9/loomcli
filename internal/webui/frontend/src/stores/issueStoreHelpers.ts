/**
 * Types, constants, and pure helper functions for the issue store.
 * Extracted from issueStore.ts to stay within LOC limits.
 */

import { produce } from "immer";

import type {
  ConnectionState,
  MutationPayload,
  MutationType,
} from "../api/sse";
import type { GraphFilter } from "../api/issues";
import type { Issue, WorkFilter, Status } from "../types";
import {
  MutationCreate,
  MutationUpdate,
  MutationDelete,
  MutationStatus,
  MutationBonded,
  MutationRefresh,
} from "../types/mutation";

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

export const TOO_FAR_BEHIND_THRESHOLD = 3;
export const MAX_RECONNECT_ATTEMPTS = 10;
export const STALE_BANNER_DELAY_MS = 5_000;
export const AUTO_ROLLBACK_TIMEOUT_MS = 30_000;
export const REFRESH_DEBOUNCE_MS = 1_000;

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

function createIssueFromMutation(mutation: MutationPayload): Issue {
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
