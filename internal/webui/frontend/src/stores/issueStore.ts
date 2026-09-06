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
import { ApiError } from "../types/common";
import {
  calculateBackoffDelay,
  type ReconnectConfig,
} from "../utils/reconnectBackoff";

import {
  TOO_FAR_BEHIND_THRESHOLD,
  MAX_RECONNECT_ATTEMPTS,
  STALE_BANNER_DELAY_MS,
  AUTO_ROLLBACK_TIMEOUT_MS,
  REFRESH_DEBOUNCE_MS,
  MAX_PROJECTION_REFRESH_WAIT_MS,
  MAX_AUTO_RETRIES,
  RETRY_BASE_DELAY_MS,
  RETRY_MAX_DELAY_MS,
  INITIAL_STATE,
  issuesAreEqual,
  mergeKanbanProjection,
  extractErrorMessage,
  issueMutationAppliesToLocalIssue,
  issueMutationInvalidatesProjection,
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

/**
 * Whether a failed fetch is worth retrying automatically. Network failures
 * and 5xx are; 4xx are not, except the two transient ones.
 */
export function isRetryableError(err: unknown): boolean {
  if (!(err instanceof ApiError)) return true;
  if (err.status === 408 || err.status === 429) return true;
  return err.status < 400 || err.status >= 500;
}

export function createIssueStore(
  initialConfig?: IssueStoreConfig,
): StoreApi<IssueStore> {
  // --- Closure state (not in Zustand — doesn't trigger re-renders) ---
  const optimisticEntries = new Map<string, OptimisticEntry>();
  let scopeEpoch = 0;
  let activeScopeKey: string | null = null;
  let recoveryRevision = 0;
  let commandRevision = 0;
  const unresolvedCommands = new Map<string, Set<object>>();
  let fetchGeneration = 0;
  let fetchTimestamp = 0;
  /**
   * Controller for the in-flight fetchIssues call. New calls abort it and
   * install their own. Used to distinguish "currently fetching" from "stale
   * fetch completing late" — replaces the previous `isFetching` boolean,
   * which could silently drop retry attempts.
   */
  let activeController: AbortController | null = null;
  let activeRecovery: { scope: string; promise: Promise<void> } | null = null;
  function fetchScope(params: FetchIssuesParams): string {
    return JSON.stringify([
      params.workspaceId,
      params.mode,
      params.filter ?? null,
      params.graphFilter ?? null,
      [...(params.sourceRepos ?? [])].sort(),
    ]);
  }

  const deletedDuringFetch = new Set<string>();
  let refreshTimeout: ReturnType<typeof setTimeout> | null = null;
  let projectionRefreshPendingSince: number | null = null;
  let staleBannerTimeout: ReturnType<typeof setTimeout> | null = null;
  /** Pending auto-retry timer; cleared on new fetch, reset, or success. */
  let retryTimeout: ReturnType<typeof setTimeout> | null = null;
  let activeWorkspaceId: string | null = null;
  let activeSourceRepos: string[] | null = null;
  let activeMode: "ready" | "graph" | "kanban" = "ready";
  let activeFilter: WorkFilter | undefined;
  let activeGraphFilter: GraphFilter | undefined;
  let mutationCountAtDisconnect = 0;
  let prevConnectionState: ConnectionState = "disconnected";
  let reconnectRecoveryPending = false;
  let maxReconnectAttemptsTracked = 0;
  let eventUnsubscribe: (() => void) | null = null;

  let onToast = initialConfig?.onToast ?? null;
  let retryConnectionFn = initialConfig?.retryConnectionFn ?? null;

  function scheduleProjectionRefresh(get: () => IssueStore): void {
    const now = Date.now();
    if (projectionRefreshPendingSince === null) {
      projectionRefreshPendingSince = now;
    }

    const maxWaitRemaining = Math.max(
      0,
      MAX_PROJECTION_REFRESH_WAIT_MS - (now - projectionRefreshPendingSince),
    );

    if (refreshTimeout) clearTimeout(refreshTimeout);
    refreshTimeout = setTimeout(
      () => {
        refreshTimeout = null;
        projectionRefreshPendingSince = null;
        void get().refetch();
      },
      Math.min(REFRESH_DEBOUNCE_MS, maxWaitRemaining),
    );
  }

  /** Apply a mutation to the store, handling side effects from the pure result */
  function applyMutationToStore(
    mutation: MutationPayload,
    set: (partial: Partial<IssueStore>) => void,
    get: () => IssueStore,
  ): void {
    const mutationEpoch = scopeEpoch;
    const result = issueMutationAppliesToLocalIssue(mutation)
      ? applyMutationPure(get().issuesMap, mutation, activeController !== null)
      : {
          newMap: null,
          incrementCount: false,
          trackDeletion: null,
          scheduleRefresh: false,
        };

    if (
      result.scheduleRefresh ||
      issueMutationInvalidatesProjection(mutation)
    ) {
      scheduleProjectionRefresh(get);
    }

    if (result.trackDeletion) {
      deletedDuringFetch.add(result.trackDeletion);
    }

    if (result.newMap) {
      set({ issuesMap: result.newMap });
    }

    if (scopeEpoch !== mutationEpoch) return;

    if (result.incrementCount) {
      set({ mutationCount: get().mutationCount + 1 });
    }
  }

  const retryBackoffConfig: ReconnectConfig = {
    baseDelay: RETRY_BASE_DELAY_MS,
    maxDelay: RETRY_MAX_DELAY_MS,
    maxAttempts: MAX_AUTO_RETRIES,
    jitterFactor: 0,
  };

  /** Retire UI ownership without pretending outstanding server work settled. */
  function retireScope(): void {
    scopeEpoch++;
    recoveryRevision++;
    for (const entry of optimisticEntries.values())
      clearTimeout(entry.timeoutId);
    optimisticEntries.clear();
    if (refreshTimeout !== null) clearTimeout(refreshTimeout);
    refreshTimeout = null;
    projectionRefreshPendingSince = null;
    if (retryTimeout !== null) clearTimeout(retryTimeout);
    retryTimeout = null;
  }

  function removeOptimisticEntry(
    issueId: string,
    expected: OptimisticEntry,
    get: () => IssueStore,
    set: (partial: Partial<IssueStore>) => void,
  ): void {
    if (optimisticEntries.get(issueId) !== expected) return;
    clearTimeout(expected.timeoutId);
    optimisticEntries.delete(issueId);
    const pendingIds = new Set(get().pendingIds);
    pendingIds.delete(issueId);
    set({ pendingIds });
  }

  async function runFetchIssues(
    params: FetchIssuesParams,
    recovery = false,
  ): Promise<void> {
    params = {
      ...params,
      ...(params.filter ? { filter: structuredClone(params.filter) } : {}),
      ...(params.graphFilter
        ? { graphFilter: structuredClone(params.graphFilter) }
        : {}),
      ...(params.sourceRepos ? { sourceRepos: [...params.sourceRepos] } : {}),
    };
    const set = store.setState;
    const get = store.getState;
    const {
      workspaceId,
      mode,
      filter,
      graphFilter,
      sourceRepos,
      signal,
      isAutoRetry = false,
    } = params;
    if (signal?.aborted) {
      if (recovery) throw new DOMException("Recovery aborted", "AbortError");
      return;
    }
    const nextScope = fetchScope(params);
    const generation = ++fetchGeneration;
    const scopeChanged = nextScope !== activeScopeKey;
    if (scopeChanged) {
      retireScope();
      activeScopeKey = nextScope;
    }
    activeWorkspaceId = workspaceId;
    activeMode = mode;
    activeFilter = filter;
    activeGraphFilter = graphFilter;
    activeSourceRepos = sourceRepos ?? null;

    const readScopeEpoch = scopeEpoch;
    const readCommandRevision = commandRevision;
    if (scopeChanged) {
      set({ issuesMap: new Map(), pendingIds: new Set() });
      if (scopeEpoch !== readScopeEpoch || generation !== fetchGeneration) {
        if (recovery) throw new Error("Issue recovery scope changed");
        return;
      }
    }

    // Cancel any in-flight fetch so the new call always proceeds.
    // This replaces the previous `if (isFetching) return` guard, which
    // silently dropped manual retries and view-switch fetches.
    const previousController = activeController;
    const internalController = new AbortController();
    activeController = internalController;
    previousController?.abort();
    if (
      activeController !== internalController ||
      scopeEpoch !== readScopeEpoch ||
      generation !== fetchGeneration
    ) {
      if (recovery)
        throw new DOMException(
          "Recovery superseded while starting",
          "AbortError",
        );
      return;
    }

    // Cancel any pending auto-retry timer. If this call IS an auto-retry,
    // preserve retryCount (the retry logic already incremented it when
    // scheduling). Otherwise, reset retry state — user-initiated or
    // view-switch fetches start a fresh retry window.
    if (retryTimeout !== null) {
      clearTimeout(retryTimeout);
      retryTimeout = null;
    }
    if (!isAutoRetry) {
      set({
        isLoading: true,
        error: null,
        retryCount: 0,
        nextRetryAt: null,
      });
    } else {
      set({ isLoading: true, error: null, nextRetryAt: null });
    }

    if (
      activeController !== internalController ||
      scopeEpoch !== readScopeEpoch ||
      generation !== fetchGeneration
    ) {
      if (recovery)
        throw new DOMException(
          "Recovery superseded while starting",
          "AbortError",
        );
      return;
    }

    fetchTimestamp = Date.now();
    deletedDuringFetch.clear();

    // Merge the internal controller with any external signal — either can
    // abort the fetch. External signals come from useEffect cleanup on
    // unmount/dependency change.
    const mergedSignal: AbortSignal = signal
      ? AbortSignal.any([internalController.signal, signal])
      : internalController.signal;

    try {
      const effectiveFilter: WorkFilter | undefined = sourceRepos?.length
        ? { ...filter, source_repos: sourceRepos }
        : filter;
      const effectiveGraphFilter: GraphFilter | undefined = sourceRepos?.length
        ? { ...graphFilter, source_repos: sourceRepos }
        : graphFilter;

      const reqOpts: Pick<RequestOptions, "signal"> = {
        signal: mergedSignal,
      };

      const awaitResponse = async (
        request: Promise<Issue[]>,
      ): Promise<Issue[]> => {
        if (!recovery) return request;
        let onAbort: () => void = () => {};
        const aborted = new Promise<never>((_, reject) => {
          onAbort = () =>
            reject(new DOMException("Recovery aborted", "AbortError"));
          mergedSignal.addEventListener("abort", onAbort, { once: true });
          if (mergedSignal.aborted) onAbort();
        });
        try {
          return await Promise.race([request, aborted]);
        } finally {
          mergedSignal.removeEventListener("abort", onAbort);
        }
      };
      let data: Issue[];
      if (mode === "kanban") {
        data = await awaitResponse(
          getKanbanIssues(workspaceId, effectiveFilter, reqOpts),
        );
      } else if (mode === "graph") {
        data = await awaitResponse(
          fetchGraphIssues(workspaceId, effectiveGraphFilter, reqOpts),
        );
      } else {
        data = await awaitResponse(
          getReadyIssues(workspaceId, effectiveFilter, reqOpts),
        );
      }

      if (
        activeController !== internalController ||
        mergedSignal.aborted ||
        scopeEpoch !== readScopeEpoch ||
        generation !== fetchGeneration
      ) {
        if (recovery)
          throw new DOMException(
            "Recovery superseded or aborted",
            "AbortError",
          );
        return;
      }

      if (
        recovery &&
        (unresolvedCommands.get(workspaceId)?.size ||
          commandRevision !== readCommandRevision ||
          scopeEpoch !== readScopeEpoch)
      ) {
        throw new Error(
          "Issue recovery cannot complete while commands are unresolved or changed",
        );
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
          mergedMap.set(
            id,
            mode === "kanban"
              ? mergeKanbanProjection(currentIssue, apiIssue)
              : currentIssue,
          );
        }
      }

      // Success is the only point that can clear stale snapshot state.
      set({
        issuesMap: mergedMap,
        showStaleBanner: false,
        connectionLost: false,
        disconnectedSince: null,
        isLoading: false,
        error: null,
        retryCount: 0,
        nextRetryAt: null,
      });
      if (
        recovery &&
        (activeController !== internalController ||
          mergedSignal.aborted ||
          scopeEpoch !== readScopeEpoch ||
          generation !== fetchGeneration ||
          commandRevision !== readCommandRevision ||
          !!unresolvedCommands.get(workspaceId)?.size)
      ) {
        throw new DOMException(
          "Recovery superseded during commit",
          "AbortError",
        );
      }
    } catch (err) {
      if (recovery) {
        if (activeController === internalController) {
          set({
            isLoading: false,
            error: mergedSignal.aborted ? null : extractErrorMessage(err),
          });
        }
        throw err;
      }
      if (err instanceof DOMException && err.name === "AbortError") {
        // Aborted fetch. If a newer call superseded us, leave isLoading
        // alone — the newer call has already set it true and is in flight.
        // If we're still the active controller, the abort came from the
        // external signal (e.g., useEffect cleanup on unmount); clear
        // isLoading so the UI doesn't stay in a spinner forever. Either
        // way, do NOT schedule an auto-retry for user-driven aborts.
        if (activeController === internalController) {
          set({ isLoading: false });
        }
        return;
      }
      // A non-abort error from a superseded fetch must not mutate the
      // newer fetch's state or schedule a retry. Parallels the
      // AbortError and `finally` branches.
      if (activeController !== internalController) {
        return;
      }
      // Prefer the server's original error text (from ApiError.body.error)
      // over the generic HTTP status text baked into ApiError.message. This
      // lets IssueViewGuard distinguish "workspace is loading" (daemon
      // starting, 503+kind=starting) from other 503s like "daemon
      // unavailable" and route to the loading-variant UX accordingly.
      const message = extractErrorMessage(err);

      // A client error (4xx) is deterministic — the same request will fail
      // the same way — so retrying only hammers the server and hides the
      // real message behind a countdown. 408 and 429 are the transient
      // exceptions. Surface the error and stop.
      if (!isRetryableError(err)) {
        set({
          error: message,
          isLoading: false,
          retryCount: MAX_AUTO_RETRIES,
          nextRetryAt: null,
        });
        return;
      }

      // Schedule exponential-backoff auto-retry if we haven't exhausted
      // the budget. The retry calls fetchIssues({ isAutoRetry: true }),
      // which preserves retryCount for subsequent attempts.
      const currentRetryCount = get().retryCount;
      if (currentRetryCount < MAX_AUTO_RETRIES) {
        const nextAttempt = currentRetryCount + 1;
        const delay = calculateBackoffDelay(
          currentRetryCount,
          retryBackoffConfig,
        );
        set({
          error: message,
          isLoading: false,
          retryCount: nextAttempt,
          nextRetryAt: Date.now() + delay,
        });
        if (
          activeController !== internalController ||
          scopeEpoch !== readScopeEpoch ||
          generation !== fetchGeneration
        )
          return;
        // Strip the external signal before retrying: by the time this
        // timer fires, the caller's AbortController (e.g. the one from
        // App.tsx's useEffect) may have been aborted by a cleanup (view
        // switch, StrictMode double-invoke). Reusing that aborted signal
        // would abort the retry before it starts, silently eating the
        // recovery attempt. The internal controller still provides
        // cancellation for the retry itself.
        const { signal: _externalSignal, ...retryParams } = params;
        void _externalSignal;
        retryTimeout = setTimeout(() => {
          if (scopeEpoch !== readScopeEpoch || generation !== fetchGeneration)
            return;
          retryTimeout = null;
          void get().fetchIssues({
            ...retryParams,
            isAutoRetry: true,
          });
        }, delay);
      } else {
        // Exhausted retries — leave error displayed, stop retrying.
        set({
          error: message,
          isLoading: false,
          nextRetryAt: null,
        });
      }
    } finally {
      // Only clear activeController if it's still ours. A newer call
      // may have replaced it; clearing blindly would erase the new call's
      // controller and re-enable concurrent fetches.
      if (activeController === internalController) {
        activeController = null;
        deletedDuringFetch.clear();
      }
    }
  }

  const store = createStore<IssueStore>((set, get) => ({
    ...INITIAL_STATE,

    configure(config: IssueStoreConfig): void {
      if (config.onToast !== undefined) onToast = config.onToast ?? null;
      if (config.retryConnectionFn !== undefined)
        retryConnectionFn = config.retryConnectionFn ?? null;
    },

    fetchIssues(params: FetchIssuesParams): Promise<void> {
      if (activeRecovery?.scope === fetchScope(params)) {
        // Legacy refresh scheduling may join this post-fence request, but its
        // swallowing contract must not turn a failure into recovery success.
        return activeRecovery.promise.catch(() => {});
      }
      activeRecovery = null;
      return runFetchIssues(params);
    },

    getRecoveryRevision(): number {
      return recoveryRevision;
    },

    async refreshForRecovery(
      signal: AbortSignal,
      expectedWorkspaceId?: string,
    ): Promise<void> {
      if (
        !activeWorkspaceId ||
        (expectedWorkspaceId !== undefined &&
          expectedWorkspaceId !== activeWorkspaceId)
      ) {
        throw new Error(
          "Issue recovery scope is not configured or workspace changed",
        );
      }
      const workspaceId = activeWorkspaceId;
      if (unresolvedCommands.get(workspaceId)?.size)
        throw new Error("Issue recovery has unresolved commands");
      const params: FetchIssuesParams = {
        workspaceId,
        mode: activeMode,
        signal,
      };
      if (activeFilter !== undefined) params.filter = activeFilter;
      if (activeGraphFilter !== undefined)
        params.graphFilter = activeGraphFilter;
      if (activeSourceRepos) params.sourceRepos = [...activeSourceRepos];
      let resolve!: () => void;
      let reject!: (error: unknown) => void;
      const promise = new Promise<void>((yes, no) => {
        resolve = yes;
        reject = no;
      });
      const recovery = { scope: fetchScope(params), promise };
      activeRecovery = recovery;
      void runFetchIssues(params, true).then(resolve, reject);
      try {
        await promise;
      } finally {
        if (activeRecovery === recovery) activeRecovery = null;
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
      if (eventUnsubscribe) return eventUnsubscribe;
      let active = true;
      let sourceUnsubscribe: (() => void) | undefined;
      const cleanup = () => {
        if (!active) return;
        active = false;
        if (eventUnsubscribe === cleanup) eventUnsubscribe = null;
        sourceUnsubscribe?.();
      };
      eventUnsubscribe = cleanup;
      try {
        const unsubscribe = subscribeFn((mutation: MutationPayload) => {
          if (active && eventUnsubscribe === cleanup)
            get().applyMutation(mutation);
        });
        sourceUnsubscribe = unsubscribe;
        if (!active) unsubscribe();
      } catch (error) {
        cleanup();
        throw error;
      }
      return cleanup;
    },

    applyMutation(mutation: MutationPayload): void {
      const { issue_id } = mutation;
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

      if (
        issueMutationAppliesToLocalIssue(mutation) ||
        issueMutationInvalidatesProjection(mutation)
      )
        recoveryRevision++;
      // Optimistic gate: buffer mutations for pending issues
      if (issue_id && optimisticEntries.has(issue_id)) {
        optimisticEntries.get(issue_id)!.bufferedMutations.push(mutation);
        return;
      }

      applyMutationToStore(mutation, set, get);
    },

    async updateIssueStatus(
      issueId: string,
      newStatus: Status,
      workspaceId: string,
    ): Promise<void> {
      if (workspaceId !== activeWorkspaceId)
        throw new Error("Issue update workspace is not active");
      const existingIssue = get().issuesMap.get(issueId);
      if (!existingIssue) throw new Error(`Issue ${issueId} not found`);
      if (optimisticEntries.has(issueId))
        throw new Error(`Issue ${issueId} already has a pending update`);
      const epoch = scopeEpoch;
      const token = {};
      const pending = unresolvedCommands.get(workspaceId) ?? new Set<object>();
      pending.add(token);
      unresolvedCommands.set(workspaceId, pending);
      commandRevision++;
      recoveryRevision++;
      const owns = () =>
        scopeEpoch === epoch &&
        activeWorkspaceId === workspaceId &&
        optimisticEntries.get(issueId) === entry;
      const finish = (rollback: boolean, message?: string) => {
        if (!owns()) return;
        if (rollback) {
          const issuesMap = new Map(get().issuesMap);
          issuesMap.set(issueId, entry.snapshot);
          set({ issuesMap });
        }
        for (const mutation of entry.bufferedMutations) {
          if (!owns()) return;
          applyMutationToStore(mutation, set, get);
        }
        if (!owns()) return;
        const revisionBeforeCallbacks = commandRevision;
        removeOptimisticEntry(issueId, entry, get, set);
        if (scopeEpoch !== epoch || commandRevision !== revisionBeforeCallbacks)
          return;
        if (message) onToast?.(message, { type: "error" });
        else scheduleProjectionRefresh(get);
      };
      const timeoutId = setTimeout(
        () => finish(true, "Update timed out — changes reverted"),
        AUTO_ROLLBACK_TIMEOUT_MS,
      );
      const entry: OptimisticEntry = {
        snapshot: existingIssue,
        bufferedMutations: [],
        timeoutId,
      };
      optimisticEntries.set(issueId, entry);
      try {
        const pendingIds = new Set(get().pendingIds);
        pendingIds.add(issueId);
        const issuesMap = new Map(get().issuesMap);
        issuesMap.set(issueId, {
          ...existingIssue,
          status: newStatus,
          updated_at: new Date().toISOString(),
        });
        set({ issuesMap, pendingIds });
        await apiUpdateIssue(workspaceId, issueId, { status: newStatus });
        finish(false);
      } catch (error) {
        finish(
          true,
          error instanceof Error ? error.message : "Failed to update status",
        );
        throw error;
      } finally {
        pending.delete(token);
        if (
          pending.size === 0 &&
          unresolvedCommands.get(workspaceId) === pending
        )
          unresolvedCommands.delete(workspaceId);
        recoveryRevision++;
      }
    },

    setConnectionState(newState: ConnectionState): void {
      const prev = prevConnectionState;
      prevConnectionState = newState;
      set({ connectionState: newState });

      if (newState === "reconnecting" && prev !== "reconnecting") {
        const now = Date.now();
        reconnectRecoveryPending = true;
        set({ disconnectedSince: now });
        mutationCountAtDisconnect = get().mutationCount;

        if (staleBannerTimeout) clearTimeout(staleBannerTimeout);
        staleBannerTimeout = setTimeout(() => {
          set({ showStaleBanner: true });
        }, STALE_BANNER_DELAY_MS);
      }

      if (newState === "connected" && reconnectRecoveryPending) {
        if (staleBannerTimeout) {
          clearTimeout(staleBannerTimeout);
          staleBannerTimeout = null;
        }

        if (maxReconnectAttemptsTracked >= TOO_FAR_BEHIND_THRESHOLD) {
          onToast?.("Connection restored. Refreshing data...", {
            type: "info",
            duration: 3000,
          });
          void get().refetch();
        } else {
          const changeCount = get().mutationCount - mutationCountAtDisconnect;
          if (changeCount > 0) {
            void get().refetch();
            onToast?.(
              `Connection restored. ${changeCount} change${changeCount !== 1 ? "s" : ""} synced.`,
              { type: "info", duration: 3000 },
            );
          } else {
            void get().refetch();
            onToast?.("Connection restored.", { type: "info", duration: 3000 });
          }
        }
        maxReconnectAttemptsTracked = 0;
        reconnectRecoveryPending = false;
      }

      if (newState === "disconnected" && reconnectRecoveryPending) {
        if (staleBannerTimeout) {
          clearTimeout(staleBannerTimeout);
          staleBannerTimeout = null;
        }
        reconnectRecoveryPending = false;
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
      activeRecovery = null;
      activeScopeKey = null;
      retireScope();

      if (refreshTimeout) {
        clearTimeout(refreshTimeout);
        refreshTimeout = null;
      }
      projectionRefreshPendingSince = null;
      if (staleBannerTimeout) {
        clearTimeout(staleBannerTimeout);
        staleBannerTimeout = null;
      }
      if (retryTimeout) {
        clearTimeout(retryTimeout);
        retryTimeout = null;
      }
      if (activeController) {
        activeController.abort();
        activeController = null;
      }

      // Note: eventUnsubscribe is NOT called here. The SSE subscription
      // lifecycle is managed by StoreWiring's useEffect, not by reset().
      // Calling eventUnsubscribe() here would break SSE after workspace changes.

      fetchTimestamp = 0;
      deletedDuringFetch.clear();
      activeWorkspaceId = null;
      activeSourceRepos = null;
      activeMode = "ready";
      activeFilter = undefined;
      activeGraphFilter = undefined;
      mutationCountAtDisconnect = 0;
      prevConnectionState = "disconnected";
      reconnectRecoveryPending = false;
      maxReconnectAttemptsTracked = 0;

      set({ ...INITIAL_STATE, pendingIds: new Set(), issuesMap: new Map() });
    },
  }));

  return store;
}
