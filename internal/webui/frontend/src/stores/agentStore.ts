/**
 * Zustand vanilla store for agent state management.
 *
 * Lifecycle is SSE-signal-driven: `start(workspaceId, subscribeFn)` subscribes
 * to `agent_state_change` mutations and refetches agent data on each signal.
 * Status/tasks (no SSE equivalent) keep a 5s polling timer.
 *
 * Two disjoint fetch paths:
 *   - fetchAgentData → drives isConnected / backoff. Runs on cold start, on
 *     each SSE signal, and on a fallback 5s timer when SSE is wedged
 *     (reconnectAttempts >= MAX_RECONNECT_ATTEMPTS).
 *   - fetchMonitorData → status + tasks. Runs on cold start and every 5s.
 *     Failures log a warning only; never touches isConnected.
 *
 * `start()` returns an idempotent disposer. The caller (StoreWiring) returns
 * it from useEffect cleanup. `stop()` exists so `reset()` can tear down
 * without threading the disposer through.
 */

import { createStore, type StoreApi } from "zustand/vanilla";

import { fetchAgents, fetchStatus, fetchTasks } from "../api/agents";
import {
  DEFAULT_TASKS,
  DEFAULT_SYNC,
  DEFAULT_STATS,
  DEFAULT_TASK_LISTS,
} from "../api/agents/defaults";
import { MAX_RECONNECT_ATTEMPTS } from "./issueStoreHelpers";
import type { SubscribeFn } from "./issueStoreHelpers";
import type { MutationPayload } from "../types";
import type {
  LoomAgentStatus,
  LoomTaskSummary,
  LoomTaskInfo,
  LoomTaskLists,
  LoomSyncInfo,
  LoomStats,
  LoomConnectionState,
} from "../types";

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const INITIAL_RETRY_DELAY_S = 5;
const MAX_RETRY_DELAY_S = 60;
const BACKOFF_MULTIPLIER = 2;
const STALE_BANNER_DELAY_MS = 5_000;
const MAX_FAILURES_AT_CEILING = 5;
const MONITOR_POLL_INTERVAL_MS = 5_000;
const FALLBACK_POLL_INTERVAL_MS = 5_000;

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

export interface AgentStoreConfig {
  onToast?: (message: string) => void;
}

export interface AgentStoreState {
  // Data
  agents: LoomAgentStatus[];
  tasks: LoomTaskSummary;
  taskLists: LoomTaskLists;
  agentTasks: Record<string, LoomTaskInfo>;
  sync: LoomSyncInfo;
  stats: LoomStats;

  // Loading & errors
  isLoading: boolean;
  error: Error | null;

  // Connection
  isConnected: boolean;
  connectionState: LoomConnectionState;
  wasEverConnected: boolean;

  // Backoff
  retryCountdown: number;
  showStaleBanner: boolean;
  connectionLost: boolean;
  disconnectedSince: number | null;

  // Timestamps
  lastUpdated: number | null;
}

export interface AgentStoreActions {
  start: (workspaceId: string, subscribeFn: SubscribeFn) => () => void;
  stop: () => void;
  setReconnectAttempts: (attempts: number) => void;
  retryNow: () => void;
  getAgentByName: (name: string) => LoomAgentStatus | undefined;
  reset: () => void;
}

export type AgentStore = AgentStoreState & AgentStoreActions;

// ---------------------------------------------------------------------------
// Initial state
// ---------------------------------------------------------------------------

export const INITIAL_STATE: AgentStoreState = {
  agents: [],
  tasks: DEFAULT_TASKS,
  taskLists: DEFAULT_TASK_LISTS,
  agentTasks: {},
  sync: DEFAULT_SYNC,
  stats: DEFAULT_STATS,
  isLoading: false,
  error: null,
  isConnected: false,
  connectionState: "never_connected",
  wasEverConnected: false,
  retryCountdown: 0,
  showStaleBanner: false,
  connectionLost: false,
  disconnectedSince: null,
  lastUpdated: null,
};

// ---------------------------------------------------------------------------
// deriveConnectionState
// ---------------------------------------------------------------------------

function deriveConnectionState(
  isConnected: boolean,
  isLoading: boolean,
  wasEverConnected: boolean,
  retryCountdown: number,
): LoomConnectionState {
  if (isConnected) return "connected";
  if (isLoading && !wasEverConnected) return "never_connected";
  if (retryCountdown > 0) return "reconnecting";
  if (!wasEverConnected) return "never_connected";
  return "disconnected";
}

// ---------------------------------------------------------------------------
// Factory
// ---------------------------------------------------------------------------

export function createAgentStore(
  _initialConfig?: AgentStoreConfig,
): StoreApi<AgentStore> {
  // --- Closure state (not in Zustand — doesn't trigger re-renders) ---
  let activeDisposer: (() => void) | null = null;
  let sseUnsubscribe: (() => void) | null = null;
  let agentFetchController: AbortController | null = null;
  let monitorFetchController: AbortController | null = null;
  let monitorIntervalId: ReturnType<typeof setInterval> | null = null;
  let fallbackIntervalId: ReturnType<typeof setInterval> | null = null;
  let fallbackPollingActive = false;
  let coldStartInFlight = false;
  let pendingSignalDuringColdStart = false;
  let visibilityHandler: (() => void) | null = null;

  // Monotonic generation counter — bumped on every start() invocation.
  // The cold-start `.finally` callback captures the generation it was scheduled
  // under and bails if a newer start() has already begun. Prevents a stale
  // (aborted) cold-start finally from clobbering the new start's
  // `coldStartInFlight` flag during a rapid workspace switch.
  let startGeneration = 0;

  let retryTimeoutId: ReturnType<typeof setTimeout> | null = null;
  let countdownIntervalId: ReturnType<typeof setInterval> | null = null;
  let staleBannerTimeoutId: ReturnType<typeof setTimeout> | null = null;
  let currentRetryDelay = INITIAL_RETRY_DELAY_S;
  let consecutiveFailuresAtCeiling = 0;

  // Active workspace ID set by start(). fetchAgentData / fetchMonitorData
  // read it per call so the disposer can guard against late writes after a
  // workspace switch.
  let activeWorkspaceID = "";

  // Drops entries from `patch` whose serialized value equals the current
  // store value, so polling ticks that return identical data don't fire a
  // new reference to subscribers (zustand's default equality is Object.is).
  // JSON.stringify is fine here: payloads are API responses without
  // undefineds, functions, or circular refs.
  function skipIfEqual<K extends keyof AgentStore>(
    patch: Partial<AgentStore>,
    current: AgentStore,
    keys: readonly K[],
  ): Partial<AgentStore> {
    const out: Partial<AgentStore> = { ...patch };
    for (const k of keys) {
      if (k in out && JSON.stringify(out[k]) === JSON.stringify(current[k])) {
        delete out[k];
      }
    }
    return out;
  }

  // --- Internal timer helpers ---

  function clearRetryTimers(): void {
    if (retryTimeoutId) {
      clearTimeout(retryTimeoutId);
      retryTimeoutId = null;
    }
    if (countdownIntervalId) {
      clearInterval(countdownIntervalId);
      countdownIntervalId = null;
    }
  }

  function clearStaleBannerTimer(): void {
    if (staleBannerTimeoutId) {
      clearTimeout(staleBannerTimeoutId);
      staleBannerTimeoutId = null;
    }
  }

  // --- Backoff engine ---

  function reportSuccess(set: (partial: Partial<AgentStore>) => void): void {
    clearRetryTimers();
    currentRetryDelay = INITIAL_RETRY_DELAY_S;
    consecutiveFailuresAtCeiling = 0;
    clearStaleBannerTimer();

    set({
      showStaleBanner: false,
      connectionLost: false,
      disconnectedSince: null,
      wasEverConnected: true,
      retryCountdown: 0,
    });
  }

  function scheduleRetry(
    set: (partial: Partial<AgentStore>) => void,
    get: () => AgentStore,
  ): void {
    clearRetryTimers();

    const delay = currentRetryDelay;
    set({ retryCountdown: delay });

    // Countdown interval (tick every second)
    countdownIntervalId = setInterval(() => {
      const current = get().retryCountdown;
      if (current <= 1) {
        if (countdownIntervalId) {
          clearInterval(countdownIntervalId);
          countdownIntervalId = null;
        }
        set({ retryCountdown: 0 });
        return;
      }
      set({ retryCountdown: current - 1 });
    }, 1000);

    // Schedule actual retry
    retryTimeoutId = setTimeout(() => {
      clearRetryTimers();
      set({ retryCountdown: 0 });
      currentRetryDelay = Math.min(
        currentRetryDelay * BACKOFF_MULTIPLIER,
        MAX_RETRY_DELAY_S,
      );
      if (activeWorkspaceID) void fetchAgentData(activeWorkspaceID);
    }, delay * 1000);
  }

  function reportFailure(
    set: (partial: Partial<AgentStore>) => void,
    get: () => AgentStore,
  ): void {
    const state = get();

    // Start stale banner timer on first disconnect
    if (
      state.wasEverConnected &&
      state.disconnectedSince === null &&
      !staleBannerTimeoutId
    ) {
      const now = Date.now();
      set({ disconnectedSince: now });
      staleBannerTimeoutId = setTimeout(() => {
        staleBannerTimeoutId = null;
        set({ showStaleBanner: true });
      }, STALE_BANNER_DELAY_MS);
    }

    // Track consecutive failures at max delay
    if (currentRetryDelay >= MAX_RETRY_DELAY_S) {
      consecutiveFailuresAtCeiling += 1;
      if (consecutiveFailuresAtCeiling >= MAX_FAILURES_AT_CEILING) {
        set({ connectionLost: true });
      }
    }

    // Only schedule retry if we've connected before
    if (state.wasEverConnected) {
      scheduleRetry(set, get);
    }
  }

  // ---------------------------------------------------------------------
  // Fetch paths
  // ---------------------------------------------------------------------

  // Hoisted refs to set/get filled in by createStore below — needed so the
  // top-level fetchAgentData/fetchMonitorData helpers can update store state
  // without being re-defined inside the actions block on every call.
  let storeSet: ((partial: Partial<AgentStore>) => void) | null = null;
  let storeGet: (() => AgentStore) | null = null;

  async function fetchAgentData(wsID: string): Promise<void> {
    if (!storeSet || !storeGet) return;
    const set = storeSet;
    const get = storeGet;

    // Abort any in-flight agent fetch so the new call always proceeds.
    if (agentFetchController) agentFetchController.abort();
    const controller = new AbortController();
    agentFetchController = controller;

    set({ isLoading: true });
    try {
      const agents = await fetchAgents(wsID, { signal: controller.signal });
      // Pin workspace switch + supersession safety
      if (agentFetchController !== controller) return;
      if (wsID !== activeWorkspaceID) return;

      const now = Date.now();
      set(
        skipIfEqual(
          {
            agents,
            isConnected: true,
            error: null,
            lastUpdated: now,
            isLoading: false,
          },
          get(),
          ["agents"] as const,
        ),
      );
      reportSuccess(set);
      const state = get();
      set({
        connectionState: deriveConnectionState(
          true,
          false,
          state.wasEverConnected,
          state.retryCountdown,
        ),
      });
    } catch (err) {
      if (err instanceof DOMException && err.name === "AbortError") return;
      if (agentFetchController !== controller) return;
      const error = err instanceof Error ? err : new Error(String(err));
      set({ error, isConnected: false, isLoading: false });
      reportFailure(set, get);
      const state = get();
      set({
        connectionState: deriveConnectionState(
          false,
          false,
          state.wasEverConnected,
          state.retryCountdown,
        ),
      });
    } finally {
      if (agentFetchController === controller) agentFetchController = null;
    }
  }

  async function fetchMonitorData(wsID: string): Promise<void> {
    if (!storeSet || !storeGet) return;
    const set = storeSet;
    const get = storeGet;

    if (monitorFetchController) monitorFetchController.abort();
    const controller = new AbortController();
    monitorFetchController = controller;

    try {
      try {
        const statusResult = await fetchStatus(wsID, {
          signal: controller.signal,
        });
        if (monitorFetchController !== controller) return;
        if (wsID !== activeWorkspaceID) return;
        set(
          skipIfEqual(
            {
              tasks: statusResult.tasks,
              agentTasks: statusResult.agentTasks,
              sync: statusResult.sync,
              stats: statusResult.stats,
            },
            get(),
            ["tasks", "agentTasks", "sync", "stats"] as const,
          ),
        );
      } catch (err) {
        if (!(err instanceof DOMException && err.name === "AbortError")) {
          console.warn(
            "Loom status fetch failed:",
            err instanceof Error ? err.message : String(err),
          );
        }
      }

      try {
        const tasksResult = await fetchTasks(wsID, {
          signal: controller.signal,
        });
        if (monitorFetchController !== controller) return;
        if (wsID !== activeWorkspaceID) return;
        set(
          skipIfEqual({ taskLists: tasksResult }, get(), [
            "taskLists",
          ] as const),
        );
      } catch (err) {
        if (!(err instanceof DOMException && err.name === "AbortError")) {
          console.warn(
            "Loom tasks fetch failed:",
            err instanceof Error ? err.message : String(err),
          );
        }
      }
    } finally {
      if (monitorFetchController === controller) monitorFetchController = null;
    }
  }

  // ---------------------------------------------------------------------
  // Internal teardown — runs as the disposer body. Idempotent.
  // ---------------------------------------------------------------------

  function tearDown(): void {
    if (sseUnsubscribe) {
      try {
        sseUnsubscribe();
      } catch (err) {
        console.warn("[agentStore] SSE unsubscribe threw:", err);
      }
      sseUnsubscribe = null;
    }
    if (monitorIntervalId) {
      clearInterval(monitorIntervalId);
      monitorIntervalId = null;
    }
    if (fallbackIntervalId) {
      clearInterval(fallbackIntervalId);
      fallbackIntervalId = null;
    }
    if (agentFetchController) {
      agentFetchController.abort();
      agentFetchController = null;
    }
    if (monitorFetchController) {
      monitorFetchController.abort();
      monitorFetchController = null;
    }
    if (visibilityHandler && typeof document !== "undefined") {
      document.removeEventListener("visibilitychange", visibilityHandler);
      visibilityHandler = null;
    }

    clearRetryTimers();
    clearStaleBannerTimer();

    fallbackPollingActive = false;
    coldStartInFlight = false;
    pendingSignalDuringColdStart = false;

    if (storeSet && storeGet) {
      const state = storeGet();
      storeSet({
        retryCountdown: 0,
        connectionState: deriveConnectionState(
          state.isConnected,
          false,
          state.wasEverConnected,
          0,
        ),
      });
    }
  }

  // ---------------------------------------------------------------------
  // Store
  // ---------------------------------------------------------------------

  const store = createStore<AgentStore>((set, get) => {
    storeSet = set;
    storeGet = get;

    return {
      ...INITIAL_STATE,

      start(workspaceId: string, subscribeFn: SubscribeFn): () => void {
        // Empty workspaceId: no subscription, no fetch, no timer.
        if (workspaceId === "") {
          if (
            typeof process !== "undefined" &&
            process.env?.NODE_ENV !== "production"
          ) {
            console.warn(
              "[agentStore] start() called with empty workspaceId — no-op",
            );
          }
          return () => {};
        }

        // Re-entry: tear down the prior subscription so we don't leak
        // SSE listeners across rapid workspace switches.
        if (activeDisposer) {
          activeDisposer();
        }

        // Reset backoff on workspace change so a new workspace doesn't
        // inherit a stale retry delay from a previous workspace's failure
        // streak.
        if (workspaceId !== activeWorkspaceID) {
          currentRetryDelay = INITIAL_RETRY_DELAY_S;
          consecutiveFailuresAtCeiling = 0;
        }
        activeWorkspaceID = workspaceId;

        // 1. Subscribe BEFORE the cold-start fetch — guarantees no SSE
        //    signal is dropped while the initial fetch is in flight.
        const myGeneration = ++startGeneration;
        coldStartInFlight = true;
        pendingSignalDuringColdStart = false;
        sseUnsubscribe = subscribeFn(
          (mutation: MutationPayload) => {
            // Workspace gate (belt-and-suspenders on top of EventProvider's
            // per-workspace SSE client; defends against stale callbacks
            // during a workspace-switch race).
            if (
              mutation.workspace_id &&
              mutation.workspace_id !== activeWorkspaceID
            ) {
              return;
            }
            if (coldStartInFlight) {
              pendingSignalDuringColdStart = true;
              return;
            }
            if (activeWorkspaceID) void fetchAgentData(activeWorkspaceID);
          },
          { types: ["agent_state_change"] },
        );

        // 2. Cold-start fetch. Bind the finally to this specific promise
        //    AND this start() generation: a stale finally from a prior
        //    start() (whose fetch was aborted by tearDown) must not clobber
        //    the new start's `coldStartInFlight` flag.
        const coldStartPromise = fetchAgentData(workspaceId);
        coldStartPromise.finally(() => {
          if (myGeneration !== startGeneration) return;
          coldStartInFlight = false;
          if (pendingSignalDuringColdStart) {
            pendingSignalDuringColdStart = false;
            if (activeWorkspaceID) void fetchAgentData(activeWorkspaceID);
          }
        });
        void fetchMonitorData(workspaceId);

        // 3. Monitor poll timer (always on; status/tasks have no SSE).
        monitorIntervalId = setInterval(() => {
          if (activeWorkspaceID) void fetchMonitorData(activeWorkspaceID);
        }, MONITOR_POLL_INTERVAL_MS);

        // 4. Visibility handler — refetch both paths on tab focus.
        if (typeof document !== "undefined") {
          visibilityHandler = () => {
            if (document.visibilityState === "visible") {
              if (activeWorkspaceID) {
                void fetchAgentData(activeWorkspaceID);
                void fetchMonitorData(activeWorkspaceID);
              }
            }
          };
          document.addEventListener("visibilitychange", visibilityHandler);
        }

        // 5. Idempotent disposer.
        const disposer = (): void => {
          if (activeDisposer !== disposer) return;
          activeDisposer = null;
          tearDown();
        };
        activeDisposer = disposer;
        return disposer;
      },

      stop(): void {
        if (activeDisposer) activeDisposer();
      },

      setReconnectAttempts(attempts: number): void {
        // No active session: ignore. Without this guard, fallbackIntervalId
        // would be set up with no disposer to clean it up — leaks the timer
        // until process exit. This also avoids spawning a fallback timer
        // before start() has run (e.g. if EventProvider's reconnectAttempts
        // hits the threshold before workspaceId resolves).
        if (!activeWorkspaceID) return;

        const shouldFallback = attempts >= MAX_RECONNECT_ATTEMPTS;
        if (shouldFallback && !fallbackPollingActive) {
          fallbackPollingActive = true;
          void fetchAgentData(activeWorkspaceID);
          fallbackIntervalId = setInterval(() => {
            if (activeWorkspaceID) void fetchAgentData(activeWorkspaceID);
          }, FALLBACK_POLL_INTERVAL_MS);
        } else if (!shouldFallback && fallbackPollingActive) {
          fallbackPollingActive = false;
          if (fallbackIntervalId) {
            clearInterval(fallbackIntervalId);
            fallbackIntervalId = null;
          }
        }

        if (attempts >= MAX_RECONNECT_ATTEMPTS) {
          set({ connectionLost: true });
        }
      },

      retryNow(): void {
        clearRetryTimers();
        currentRetryDelay = INITIAL_RETRY_DELAY_S;
        consecutiveFailuresAtCeiling = 0;
        set({ connectionLost: false, retryCountdown: 0 });
        if (activeWorkspaceID) void fetchAgentData(activeWorkspaceID);
      },

      getAgentByName(name: string): LoomAgentStatus | undefined {
        return get().agents.find((a) => a.name === name);
      },

      reset(): void {
        get().stop();

        currentRetryDelay = INITIAL_RETRY_DELAY_S;
        consecutiveFailuresAtCeiling = 0;
        activeWorkspaceID = "";

        set({ ...INITIAL_STATE });
      },
    };
  });

  return store;
}
