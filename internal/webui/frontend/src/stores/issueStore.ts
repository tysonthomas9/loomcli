/**
 * Zustand vanilla store for issue state management.
 * Replaces useState/useRef/useCallback composition from useIssues.ts with a single
 * testable, framework-agnostic store. Consumers access state via useStore(store, selector).
 */

import { createStore, type StoreApi } from "zustand/vanilla";

import type { ConnectionState, MutationPayload } from "../api/common";
import type { RequestOptions } from "../api/common";
import type { GraphFilter } from "../api/issues";
import {
  getReadyIssues,
  getKanbanIssues,
  updateIssue as apiUpdateIssue,
  fetchGraphIssues,
} from "../api/issues";
import type { Issue, WorkFilter, Status } from "../types";

import {
  TOO_FAR_BEHIND_THRESHOLD,
  MAX_RECONNECT_ATTEMPTS,
  STALE_BANNER_DELAY_MS,
  AUTO_ROLLBACK_TIMEOUT_MS,
  REFRESH_DEBOUNCE_MS,
  INITIAL_STATE,
  issuesAreEqual,
  processMutation as applyMutationPure,
} from "./issueStoreHelpers";
import type {
  IssueStore,
  IssueStoreConfig,
  FetchIssuesParams,
  SubscribeFn,
  OptimisticEntry,
} from "./issueStoreHelpers";

// Re-export public types and utilities
export { issuesAreEqual } from "./issueStoreHelpers";
export type {
  IssueStore,
  IssueStoreState,
  IssueStoreActions,
  IssueStoreConfig,
  FetchIssuesParams,
  SubscribeFn,
} from "./issueStoreHelpers";

export function createIssueStore(
  initialConfig?: IssueStoreConfig,
): StoreApi<IssueStore> {
  // --- Closure state (not in Zustand — doesn't trigger re-renders) ---
  const optimisticEntries = new Map<string, OptimisticEntry>();
  let fetchTimestamp = 0;
  let isFetching = false;
  const deletedDuringFetch = new Set<string>();
  let refreshTimeout: ReturnType<typeof setTimeout> | null = null;
  let staleBannerTimeout: ReturnType<typeof setTimeout> | null = null;
  let activeWorkspaceId: string | null = null;
  let activeSourceRepos: string[] | null = null;
  let activeMode: "ready" | "graph" | "kanban" = "ready";
  let activeFilter: WorkFilter | undefined;
  let activeGraphFilter: GraphFilter | undefined;
  let mutationCountAtDisconnect = 0;
  let prevConnectionState: ConnectionState = "disconnected";
  let maxReconnectAttemptsTracked = 0;
  let eventUnsubscribe: (() => void) | null = null;

  let onToast = initialConfig?.onToast ?? null;
  let retryConnectionFn = initialConfig?.retryConnectionFn ?? null;

  /** Apply a mutation to the store, handling side effects from the pure result */
  function applyMutationToStore(
    mutation: MutationPayload,
    set: (partial: Partial<IssueStore>) => void,
    get: () => IssueStore,
  ): void {
    const result = applyMutationPure(get().issuesMap, mutation, isFetching);

    if (result.scheduleRefresh) {
      if (refreshTimeout) clearTimeout(refreshTimeout);
      refreshTimeout = setTimeout(() => {
        refreshTimeout = null;
        void get().refetch();
      }, REFRESH_DEBOUNCE_MS);
    }

    if (result.trackDeletion) {
      deletedDuringFetch.add(result.trackDeletion);
    }

    if (result.newMap) {
      set({ issuesMap: result.newMap });
    }

    if (result.incrementCount) {
      set({ mutationCount: get().mutationCount + 1 });
    }
  }

  /** Remove an optimistic entry and update pendingIds */
  function removeOptimisticEntry(
    issueId: string,
    get: () => IssueStore,
    set: (partial: Partial<IssueStore>) => void,
  ): void {
    const entry = optimisticEntries.get(issueId);
    if (entry) {
      clearTimeout(entry.timeoutId);
      optimisticEntries.delete(issueId);
      const newPending = new Set(get().pendingIds);
      newPending.delete(issueId);
      set({ pendingIds: newPending });
    }
  }

  const store = createStore<IssueStore>((set, get) => ({
    ...INITIAL_STATE,

    configure(config: IssueStoreConfig): void {
      if (config.onToast !== undefined) onToast = config.onToast ?? null;
      if (config.retryConnectionFn !== undefined)
        retryConnectionFn = config.retryConnectionFn ?? null;
    },

    async fetchIssues(params: FetchIssuesParams): Promise<void> {
      if (isFetching) return;

      const { workspaceId, mode, filter, graphFilter, sourceRepos, signal } =
        params;
      activeWorkspaceId = workspaceId;
      activeMode = mode;
      activeFilter = filter;
      activeGraphFilter = graphFilter;
      activeSourceRepos = sourceRepos ?? null;

      isFetching = true;
      fetchTimestamp = Date.now();
      deletedDuringFetch.clear();
      set({ isLoading: true, error: null });

      try {
        const effectiveFilter: WorkFilter | undefined = sourceRepos?.length
          ? { ...filter, source_repos: sourceRepos }
          : filter;
        const effectiveGraphFilter: GraphFilter | undefined =
          sourceRepos?.length
            ? { ...graphFilter, source_repos: sourceRepos }
            : graphFilter;

        const reqOpts: Pick<RequestOptions, "signal"> | undefined = signal
          ? { signal }
          : undefined;

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

        const deletedSnapshot = new Set(deletedDuringFetch);
        const currentMap = get().issuesMap;
        const mergedMap = new Map<string, Issue>();

        for (const issue of data) {
          if (!issue.id) continue;
          if (deletedSnapshot.has(issue.id)) continue;
          const currentIssue = currentMap.get(issue.id);
          if (currentIssue && issuesAreEqual(currentIssue, issue)) {
            mergedMap.set(issue.id, currentIssue);
          } else {
            mergedMap.set(issue.id, issue);
          }
        }

        for (const [id, currentIssue] of currentMap) {
          const apiIssue = mergedMap.get(id);
          if (!apiIssue) {
            const createdTime = Date.parse(currentIssue.created_at);
            if (!isNaN(createdTime) && createdTime >= fetchTimestamp) {
              mergedMap.set(id, currentIssue);
            }
            continue;
          }
          const currentTime = Date.parse(currentIssue.updated_at);
          const apiTime = Date.parse(apiIssue.updated_at);
          if (!isNaN(currentTime) && !isNaN(apiTime) && currentTime > apiTime) {
            mergedMap.set(id, currentIssue);
          }
        }

        set({ issuesMap: mergedMap, isLoading: false });
      } catch (err) {
        if (err instanceof DOMException && err.name === "AbortError") {
          set({ isLoading: false });
          return;
        }
        const message =
          err instanceof Error ? err.message : "Failed to fetch issues";
        set({ error: message, isLoading: false });
      } finally {
        isFetching = false;
        deletedDuringFetch.clear();
      }
    },

    async refetch(): Promise<void> {
      if (!activeWorkspaceId) return;
      const params: FetchIssuesParams = {
        workspaceId: activeWorkspaceId,
        mode: activeMode,
      };
      if (activeFilter !== undefined) params.filter = activeFilter;
      if (activeGraphFilter !== undefined)
        params.graphFilter = activeGraphFilter;
      if (activeSourceRepos) params.sourceRepos = activeSourceRepos;
      await get().fetchIssues(params);
    },

    connectToEvents(subscribeFn: SubscribeFn): () => void {
      if (eventUnsubscribe) {
        return eventUnsubscribe;
      }

      const unsubscribe = subscribeFn((mutation: MutationPayload) => {
        get().applyMutation(mutation);
      });

      eventUnsubscribe = () => {
        unsubscribe();
        eventUnsubscribe = null;
      };

      return eventUnsubscribe;
    },

    applyMutation(mutation: MutationPayload): void {
      const { issue_id } = mutation;
      // Optimistic gate: buffer mutations for pending issues
      if (issue_id && optimisticEntries.has(issue_id)) {
        optimisticEntries.get(issue_id)!.bufferedMutations.push(mutation);
        return;
      }

      // Workspace gate
      if (
        activeWorkspaceId &&
        mutation.workspace_id &&
        mutation.workspace_id !== activeWorkspaceId
      ) {
        return;
      }

      // Source repo gate
      if (activeSourceRepos && activeSourceRepos.length > 0) {
        if (
          mutation.source_repo &&
          !activeSourceRepos.includes(mutation.source_repo)
        ) {
          return;
        }
      }

      applyMutationToStore(mutation, set, get);
    },

    async updateIssueStatus(
      issueId: string,
      newStatus: Status,
      workspaceId: string,
    ): Promise<void> {
      const existingIssue = get().issuesMap.get(issueId);
      if (!existingIssue) {
        throw new Error(`Issue ${issueId} not found`);
      }

      if (optimisticEntries.has(issueId)) {
        throw new Error(`Issue ${issueId} already has a pending update`);
      }

      // Auto-rollback timeout
      const timeoutId = setTimeout(() => {
        const entry = optimisticEntries.get(issueId);
        if (!entry) return;

        const currentMap = get().issuesMap;
        const newMap = new Map(currentMap);
        newMap.set(issueId, entry.snapshot);
        set({ issuesMap: newMap });

        for (const m of entry.bufferedMutations) {
          applyMutationToStore(m, set, get);
        }

        optimisticEntries.delete(issueId);
        const newPending = new Set(get().pendingIds);
        newPending.delete(issueId);
        set({ pendingIds: newPending });

        onToast?.("Update timed out — changes reverted", { type: "error" });
      }, AUTO_ROLLBACK_TIMEOUT_MS);

      const entry: OptimisticEntry = {
        snapshot: existingIssue,
        bufferedMutations: [],
        timeoutId,
      };
      optimisticEntries.set(issueId, entry);
      const newPending = new Set(get().pendingIds);
      newPending.add(issueId);
      set({ pendingIds: newPending });

      // Optimistic update
      const optimisticIssue: Issue = {
        ...existingIssue,
        status: newStatus,
        updated_at: new Date().toISOString(),
      };
      const currentMap = get().issuesMap;
      const newMap = new Map(currentMap);
      newMap.set(issueId, optimisticIssue);
      set({ issuesMap: newMap });

      try {
        await apiUpdateIssue(workspaceId, issueId, { status: newStatus });
        // Flush buffered mutations then clean up
        const confirmedEntry = optimisticEntries.get(issueId);
        if (confirmedEntry) {
          for (const m of confirmedEntry.bufferedMutations) {
            applyMutationToStore(m, set, get);
          }
        }
        removeOptimisticEntry(issueId, get, set);
      } catch (err) {
        const currentEntry = optimisticEntries.get(issueId);
        if (currentEntry) {
          const rollbackMap = new Map(get().issuesMap);
          rollbackMap.set(issueId, currentEntry.snapshot);
          set({ issuesMap: rollbackMap });

          for (const m of currentEntry.bufferedMutations) {
            applyMutationToStore(m, set, get);
          }

          const message =
            err instanceof Error ? err.message : "Failed to update status";
          onToast?.(message, { type: "error" });
        }
        removeOptimisticEntry(issueId, get, set);
        throw err;
      }
    },

    setConnectionState(newState: ConnectionState): void {
      const prev = prevConnectionState;
      prevConnectionState = newState;
      set({ connectionState: newState });

      if (newState === "reconnecting" && prev !== "reconnecting") {
        const now = Date.now();
        set({ disconnectedSince: now });
        mutationCountAtDisconnect = get().mutationCount;

        if (staleBannerTimeout) clearTimeout(staleBannerTimeout);
        staleBannerTimeout = setTimeout(() => {
          set({ showStaleBanner: true });
        }, STALE_BANNER_DELAY_MS);
      }

      if (prev === "reconnecting" && newState === "connected") {
        if (staleBannerTimeout) {
          clearTimeout(staleBannerTimeout);
          staleBannerTimeout = null;
        }
        set({
          showStaleBanner: false,
          connectionLost: false,
          disconnectedSince: null,
        });

        if (maxReconnectAttemptsTracked >= TOO_FAR_BEHIND_THRESHOLD) {
          onToast?.("Connection restored. Refreshing data...", {
            type: "info",
            duration: 3000,
          });
          void get().refetch();
        } else {
          const changeCount = get().mutationCount - mutationCountAtDisconnect;
          if (changeCount > 0) {
            onToast?.(
              `Connection restored. ${changeCount} change${changeCount !== 1 ? "s" : ""} synced.`,
              { type: "info", duration: 3000 },
            );
          } else {
            onToast?.("Connection restored.", { type: "info", duration: 3000 });
          }
        }
        maxReconnectAttemptsTracked = 0;
      }

      if (newState === "disconnected" && prev === "reconnecting") {
        if (staleBannerTimeout) {
          clearTimeout(staleBannerTimeout);
          staleBannerTimeout = null;
        }
      }
    },

    setReconnectAttempts(attempts: number): void {
      if (attempts > maxReconnectAttemptsTracked) {
        maxReconnectAttemptsTracked = attempts;
      }
      set({ reconnectAttempts: attempts });
      if (attempts >= MAX_RECONNECT_ATTEMPTS) {
        set({ connectionLost: true });
      }
    },

    setLastEventId(id: number | undefined): void {
      set({ lastEventId: id });
    },

    retryConnection(): void {
      retryConnectionFn?.();
    },

    getIssue(id: string): Issue | undefined {
      return get().issuesMap.get(id);
    },

    reset(): void {
      for (const entry of optimisticEntries.values()) {
        clearTimeout(entry.timeoutId);
      }
      optimisticEntries.clear();

      if (refreshTimeout) {
        clearTimeout(refreshTimeout);
        refreshTimeout = null;
      }
      if (staleBannerTimeout) {
        clearTimeout(staleBannerTimeout);
        staleBannerTimeout = null;
      }

      // Note: eventUnsubscribe is NOT called here. The SSE subscription
      // lifecycle is managed by StoreWiring's useEffect, not by reset().
      // Calling eventUnsubscribe() here would break SSE after workspace changes.

      fetchTimestamp = 0;
      isFetching = false;
      deletedDuringFetch.clear();
      activeWorkspaceId = null;
      activeSourceRepos = null;
      activeMode = "ready";
      activeFilter = undefined;
      activeGraphFilter = undefined;
      mutationCountAtDisconnect = 0;
      prevConnectionState = "disconnected";
      maxReconnectAttemptsTracked = 0;

      set({ ...INITIAL_STATE, pendingIds: new Set(), issuesMap: new Map() });
    },
  }));

  return store;
}
