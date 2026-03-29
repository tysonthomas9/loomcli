/**
 * React hook for managing issue state with real-time updates.
 * Composes useSSE + useMutationHandler + API fetch.
 *
 * This hook is the single source of truth for issue data across the application,
 * handling initial data fetching, real-time updates via SSE, and optimistic updates.
 */

import { useState, useEffect, useCallback, useMemo, useRef } from "react";

import type { ConnectionState, GraphFilter, MutationPayload } from "@/api";
import {
  getReadyIssues,
  getKanbanIssues,
  updateIssue as apiUpdateIssue,
  fetchGraphIssues,
} from "@/api";
import type { Issue, WorkFilter, Status } from "@/types";

import { useMutationHandler } from "./useMutationHandler";
import { useOptimisticUpdate } from "./useOptimisticUpdate";
import { useSSE } from "./useSSE";
import { useToast } from "./useToast";

// Threshold for triggering a full refetch after reconnection
// If we had this many consecutive reconnect attempts, assume we may have missed events
const TOO_FAR_BEHIND_THRESHOLD = 3;

// Max reconnect attempts before showing "Connection lost" with manual retry
const MAX_RECONNECT_ATTEMPTS = 10;

// Delay before showing stale data banner (ms)
const STALE_BANNER_DELAY_MS = 5000;

/**
 * Options for the useIssues hook.
 */
export interface UseIssuesOptions {
  /** Initial filter for fetching issues (default: all ready issues) */
  filter?: WorkFilter;
  /** Data source mode: 'ready' for ready issues, 'graph' for all issues with deps, 'kanban' for enriched kanban view */
  mode?: "ready" | "graph" | "kanban";
  /** Filter options when mode is 'graph' */
  graphFilter?: GraphFilter;
  /** Auto-fetch on mount (default: true) */
  autoFetch?: boolean;
  /** Auto-connect SSE (default: true) */
  autoConnect?: boolean;
  /** Subscribe to mutations on connect (default: true) */
  subscribeOnConnect?: boolean;
  /** Source repo filter — when set, refetch uses source_repos and SSE reconnects with updated URL */
  sourceRepos?: string[] | undefined;
  /** Workspace UUID — required for workspace-scoped API calls */
  workspaceId: string;
}

/**
 * Return type for the useIssues hook.
 */
export interface UseIssuesReturn {
  /** Issues as array for rendering */
  issues: Issue[];
  /** Issues as Map for O(1) lookups */
  issuesMap: Map<string, Issue>;
  /** Loading state for initial fetch */
  isLoading: boolean;
  /** Error from fetch or SSE */
  error: string | null;
  /** SSE connection state */
  connectionState: ConnectionState;
  /** Whether SSE is connected */
  isConnected: boolean;
  /** Current number of reconnection attempts */
  reconnectAttempts: number;
  /** Last event ID received from SSE (for debugging/monitoring) */
  lastEventId: number | undefined;
  /** Refetch issues from API */
  refetch: () => Promise<void>;
  /** Update an issue's status (optimistic + API call) */
  updateIssueStatus: (issueId: string, newStatus: Status) => Promise<void>;
  /** Get a single issue by ID */
  getIssue: (id: string) => Issue | undefined;
  /** Number of mutations processed */
  mutationCount: number;
  /** Immediately retry SSE connection */
  retryConnection: () => void;
  /** True when disconnected >5s — data may be stale */
  showStaleBanner: boolean;
  /** True when reconnection failed after max attempts */
  connectionLost: boolean;
  /** Timestamp (ms) when disconnection started, null if connected */
  disconnectedSince: number | null;
  /** Set of issue IDs currently in optimistic state (pending API confirmation) */
  pendingIds: Set<string>;
}

/**
 * React hook for managing issue state with real-time updates.
 *
 * @example
 * ```tsx
 * function IssueBoard() {
 *   const {
 *     issues,
 *     isLoading,
 *     error,
 *     connectionState,
 *     updateIssueStatus,
 *   } = useIssues()
 *
 *   if (isLoading) return <Spinner />
 *   if (error) return <ErrorDisplay error={error} />
 *
 *   return (
 *     <>
 *       <StatusBadge state={connectionState} />
 *       <KanbanBoard
 *         issues={issues}
 *         onStatusChange={updateIssueStatus}
 *       />
 *     </>
 *   )
 * }
 * ```
 */
export function useIssues(options: UseIssuesOptions): UseIssuesReturn {
  const {
    filter,
    mode = "ready",
    graphFilter,
    autoFetch = true,
    autoConnect = true,
    sourceRepos,
    workspaceId,
    // Note: subscribeOnConnect is deprecated with SSE - connection equals subscription
  } = options;

  // Primary state: Map for O(1) lookups
  const [issuesMap, setIssuesMap] = useState<Map<string, Issue>>(new Map());
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Track fetch timestamp for subscription (catch-up on reconnect)
  const fetchTimestampRef = useRef<number>(0);
  const mountedRef = useRef(true);

  // Track fetch state for race condition prevention
  const fetchingRef = useRef(false);
  const deletedDuringFetchRef = useRef<Set<string>>(new Set());

  // Ref for refetch callback (needed because refetch is defined after useMutationHandler)
  const refetchRef = useRef<() => void>(() => {});

  // Mutation handler setup
  const { handleMutation, mutationCount } = useMutationHandler({
    issues: issuesMap,
    setIssues: setIssuesMap,
    onRefreshRequired: () => refetchRef.current(),
    onIssueDeleted: (issueId) => {
      // Track deletions during fetch to prevent re-adding from stale API response
      if (fetchingRef.current) {
        deletedDuringFetchRef.current.add(issueId);
      }
    },
    onMutationSkipped: (mutation, reason) => {
      // Debug logging for development
      if (process.env.NODE_ENV === "development") {
        console.debug(
          "[useIssues] Mutation skipped:",
          mutation.issue_id,
          reason,
        );
      }
    },
  });

  // Stale data banner state: shown when disconnected >5 seconds
  const [showStaleBanner, setShowStaleBanner] = useState(false);
  const [connectionLost, setConnectionLost] = useState(false);
  const [disconnectedSince, setDisconnectedSince] = useState<number | null>(
    null,
  );
  const staleBannerTimerRef = useRef<ReturnType<typeof setTimeout> | null>(
    null,
  );
  const mutationCountAtDisconnectRef = useRef<number>(0);

  // Toast for user notifications
  const { showToast } = useToast();

  // Optimistic update lifecycle management
  const { startOptimistic, pendingIds, filterMutation } = useOptimisticUpdate({
    setIssuesMap,
    handleMutation,
    showToast,
    mountedRef,
  });

  // Client-side mutation gate: discard SSE mutations for repos not in active filter
  // Also gates mutations for issues in optimistic state (buffers them for later replay)
  const sourceReposRef = useRef(sourceRepos);
  useEffect(() => {
    sourceReposRef.current = sourceRepos;
  }, [sourceRepos]);

  // Track active workspace for workspace-level SSE filtering
  const workspaceIdRef = useRef(workspaceId);
  useEffect(() => {
    workspaceIdRef.current = workspaceId;
  }, [workspaceId]);

  const gatedHandleMutation = useCallback(
    (mutation: MutationPayload) => {
      // Gate: buffer mutations for issues in optimistic state
      if (!filterMutation(mutation)) {
        return;
      }

      // Gate: only process mutations for the active workspace
      // Allow mutations without workspace_id (legacy/single-workspace mode)
      const activeWs = workspaceIdRef.current;
      if (
        activeWs &&
        mutation.workspace_id &&
        mutation.workspace_id !== activeWs
      ) {
        if (process.env.NODE_ENV === "development") {
          console.debug(
            "[useIssues] Gated mutation for different workspace:",
            mutation.workspace_id,
            "active:",
            activeWs,
            mutation.issue_id,
          );
        }
        return;
      }

      const activeRepos = sourceReposRef.current;
      // If no filter is active (empty or undefined), allow all mutations
      if (!activeRepos || activeRepos.length === 0) {
        handleMutation(mutation);
        return;
      }
      // Allow mutations without source_repo (legacy/unknown origin)
      if (!mutation.source_repo) {
        handleMutation(mutation);
        return;
      }
      // Gate: only process mutations for selected repos
      if (activeRepos.includes(mutation.source_repo)) {
        handleMutation(mutation);
      } else if (process.env.NODE_ENV === "development") {
        console.debug(
          "[useIssues] Gated mutation for unselected repo:",
          mutation.source_repo,
          mutation.issue_id,
        );
      }
    },
    [handleMutation, filterMutation],
  );

  // SSE setup - connection equals subscription (no separate subscribe message)
  // The 'since' parameter is passed on connect for catch-up events
  const {
    state: connectionState,
    isConnected,
    lastError: wsError,
    reconnectAttempts,
    lastEventId,
    retryNow,
  } = useSSE({
    workspaceId,
    autoConnect,
    since:
      fetchTimestampRef.current > 0 ? fetchTimestampRef.current : undefined,
    onMutation: gatedHandleMutation,
    sourceRepos,
  });

  // Track previous state for detecting reconnection
  const prevStateRef = useRef<ConnectionState>("disconnected");
  const maxReconnectAttemptsRef = useRef<number>(0);

  // Fetch issues from API
  const refetch = useCallback(
    async (signal?: AbortSignal) => {
      if (!mountedRef.current) return;

      setIsLoading(true);
      setError(null);
      fetchTimestampRef.current = Date.now();
      fetchingRef.current = true;
      deletedDuringFetchRef.current.clear();

      try {
        // Build effective filters with source_repos when active
        const effectiveFilter: WorkFilter | undefined = sourceRepos?.length
          ? { ...filter, source_repos: sourceRepos }
          : filter;
        const effectiveGraphFilter: GraphFilter | undefined =
          sourceRepos?.length
            ? { ...graphFilter, source_repos: sourceRepos }
            : graphFilter;

        const reqOpts = signal ? { signal } : undefined;
        let data: Issue[];
        if (mode === "kanban") {
          data = await getKanbanIssues(workspaceId, effectiveFilter, reqOpts);
        } else if (mode === "graph") {
          data = await fetchGraphIssues(
            workspaceId,
            effectiveGraphFilter,
            reqOpts,
          );
        } else {
          data = await getReadyIssues(workspaceId, effectiveFilter, reqOpts);
        }
        if (!mountedRef.current) return;

        // Capture deleted IDs before merge to avoid race condition with finally block clear()
        const deletedDuringFetch = new Set(deletedDuringFetchRef.current);

        // Merge API data with current state, preserving SSE mutations received during fetch
        setIssuesMap((currentMap) => {
          const mergedMap = new Map<string, Issue>();

          // Start with API data (authoritative for which issues should exist)
          for (const issue of data) {
            // Skip issues with empty IDs (defensive: backend should always provide IDs)
            if (!issue.id) {
              if (process.env.NODE_ENV === "development") {
                console.warn(
                  "[useIssues] Skipping API issue with empty id:",
                  issue.title,
                );
              }
              continue;
            }
            // Skip if deleted during fetch window by SSE mutation
            if (deletedDuringFetch.has(issue.id)) {
              continue;
            }
            mergedMap.set(issue.id, issue);
          }

          // Preserve fresher mutations from current state
          for (const [id, currentIssue] of currentMap) {
            const apiIssue = mergedMap.get(id);

            if (!apiIssue) {
              // Keep if created during fetch (SSE create mutation)
              const createdTime = Date.parse(currentIssue.created_at);
              if (
                !isNaN(createdTime) &&
                createdTime >= fetchTimestampRef.current
              ) {
                mergedMap.set(id, currentIssue);
              }
              continue;
            }

            // Keep current if newer (SSE mutation during fetch)
            const currentTime = Date.parse(currentIssue.updated_at);
            const apiTime = Date.parse(apiIssue.updated_at);
            if (
              !isNaN(currentTime) &&
              !isNaN(apiTime) &&
              currentTime > apiTime
            ) {
              mergedMap.set(id, currentIssue);
            }
          }

          return mergedMap;
        });
      } catch (err) {
        if (!mountedRef.current) return;
        // Suppress AbortError — expected during workspace switch or unmount
        if (err instanceof DOMException && err.name === "AbortError") return;
        const message =
          err instanceof Error ? err.message : "Failed to fetch issues";
        setError(message);
      } finally {
        if (mountedRef.current) {
          setIsLoading(false);
        }
        fetchingRef.current = false;
        deletedDuringFetchRef.current.clear();
      }
    },
    [filter, mode, graphFilter, sourceRepos, workspaceId],
  );

  // Keep refetchRef in sync with the latest refetch callback
  refetchRef.current = refetch;

  // Auto-fetch on mount (AbortController cancels in-flight fetch on unmount or dep change)
  useEffect(() => {
    if (!autoFetch) return;
    const controller = new AbortController();
    void refetch(controller.signal);
    return () => {
      controller.abort();
    };
  }, [autoFetch, refetch]);

  // Track max reconnect attempts to detect prolonged disconnection
  useEffect(() => {
    if (reconnectAttempts > maxReconnectAttemptsRef.current) {
      maxReconnectAttemptsRef.current = reconnectAttempts;
    }
    // Connection lost detection: exceeded max reconnect attempts
    if (reconnectAttempts >= MAX_RECONNECT_ATTEMPTS) {
      setConnectionLost(true);
    }
  }, [reconnectAttempts]);

  // Stale banner timer + too-far-behind detection + change count toast
  useEffect(() => {
    const prevState = prevStateRef.current;
    prevStateRef.current = connectionState;

    // Transition to reconnecting: start 5s timer for stale banner
    if (connectionState === "reconnecting" && prevState !== "reconnecting") {
      const now = Date.now();
      setDisconnectedSince(now);
      mutationCountAtDisconnectRef.current = mutationCount;

      // Clear any existing timer
      if (staleBannerTimerRef.current) {
        clearTimeout(staleBannerTimerRef.current);
      }
      staleBannerTimerRef.current = setTimeout(() => {
        setShowStaleBanner(true);
      }, STALE_BANNER_DELAY_MS);
    }

    // Transition to connected from reconnecting
    if (prevState === "reconnecting" && connectionState === "connected") {
      // Clear stale banner timer and state
      if (staleBannerTimerRef.current) {
        clearTimeout(staleBannerTimerRef.current);
        staleBannerTimerRef.current = null;
      }
      setShowStaleBanner(false);
      setConnectionLost(false);
      setDisconnectedSince(null);

      // If we had multiple reconnect attempts, assume we may have missed events
      if (maxReconnectAttemptsRef.current >= TOO_FAR_BEHIND_THRESHOLD) {
        if (process.env.NODE_ENV === "development") {
          console.debug(
            "[useIssues] Connection restored after",
            maxReconnectAttemptsRef.current,
            "attempts. Triggering full refetch.",
          );
        }
        showToast("Connection restored. Refreshing data...", {
          type: "info",
          duration: 3000,
        });
        void refetch();
      } else {
        // Show change count toast (only when not doing a full refetch)
        const changeCount =
          mutationCount - mutationCountAtDisconnectRef.current;
        if (changeCount > 0) {
          showToast(
            `Connection restored. ${changeCount} change${changeCount !== 1 ? "s" : ""} synced.`,
            { type: "info", duration: 3000 },
          );
        } else {
          showToast("Connection restored.", {
            type: "info",
            duration: 3000,
          });
        }
      }
      // Reset max attempts counter after successful reconnection
      maxReconnectAttemptsRef.current = 0;
    }

    // Transition to disconnected: clear timer
    if (connectionState === "disconnected" && prevState === "reconnecting") {
      if (staleBannerTimerRef.current) {
        clearTimeout(staleBannerTimerRef.current);
        staleBannerTimerRef.current = null;
      }
    }
  }, [connectionState, showToast, refetch, mutationCount]);

  // Cleanup on unmount
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      if (staleBannerTimerRef.current) {
        clearTimeout(staleBannerTimerRef.current);
      }
    };
  }, []);

  // Optimistic status update with SSE mutation buffering and rollback
  const updateIssueStatus = useCallback(
    async (issueId: string, newStatus: Status) => {
      const existingIssue = issuesMap.get(issueId);
      if (!existingIssue) {
        throw new Error(`Issue ${issueId} not found`);
      }

      // Start optimistic tracking (returns null if issue already has pending update)
      const handle = startOptimistic(issueId, existingIssue);
      if (!handle) {
        throw new Error(`Issue ${issueId} already has a pending update`);
      }

      // Optimistic update — apply new status immediately
      const optimisticIssue: Issue = {
        ...existingIssue,
        status: newStatus,
        updated_at: new Date().toISOString(),
      };
      const newMap = new Map(issuesMap);
      newMap.set(issueId, optimisticIssue);
      setIssuesMap(newMap);

      try {
        await apiUpdateIssue(workspaceId, issueId, { status: newStatus });
        // Confirm: clear optimistic state, flush buffered SSE mutations
        handle.confirm();
      } catch (err) {
        // Rollback: restore snapshot, flush buffered SSE mutations, show toast
        if (!mountedRef.current) {
          handle.rollback();
          return;
        }
        const message =
          err instanceof Error ? err.message : "Failed to update status";
        handle.rollback(message);
        throw err;
      }
    },
    [issuesMap, startOptimistic],
  );

  // Get single issue by ID
  const getIssue = useCallback((id: string) => issuesMap.get(id), [issuesMap]);

  // Derive array from Map (memoized)
  const issues = useMemo(() => Array.from(issuesMap.values()), [issuesMap]);

  // Combine errors (fetch error takes priority, then SSE error)
  const combinedError = error ?? wsError;

  return {
    issues,
    issuesMap,
    isLoading,
    error: combinedError,
    connectionState,
    isConnected,
    reconnectAttempts,
    lastEventId,
    refetch,
    updateIssueStatus,
    getIssue,
    mutationCount,
    retryConnection: retryNow,
    showStaleBanner,
    connectionLost,
    disconnectedSince,
    pendingIds,
  };
}
