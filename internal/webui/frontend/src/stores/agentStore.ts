/**
 * Zustand vanilla store for agent state management.
 * Replaces useAgents hook (371 lines) with a single testable, framework-agnostic store.
 * Owns its own polling lifecycle (interval-based fetching, exponential backoff,
 * watchdog, visibility-change refetch) as vanilla JS — no React hooks needed.
 */

import { createStore, type StoreApi } from "zustand/vanilla";

import { fetchAgents, fetchStatus, fetchTasks } from "../api/agents";
import {
  DEFAULT_TASKS,
  DEFAULT_SYNC,
  DEFAULT_STATS,
  DEFAULT_TASK_LISTS,
} from "../api/agents/defaults";
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
const FETCH_TIMEOUT_MS = 15_000;
const WATCHDOG_TIMEOUT_MS = 20_000;

// ---------------------------------------------------------------------------
// Default values (moved from useAgents.ts)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

export interface AgentStoreConfig {
  onToast?: (message: string) => void;
}

export interface PollingOptions {
  pollInterval?: number; // ms, default 5000
  // workspaceId is required for workspace-scoped fetches. Empty or unknown
  // values result in empty responses rather than cross-workspace data leaks.
  workspaceId: string;
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
  fetchData: () => Promise<void>;
  startPolling: (options?: PollingOptions) => void;
  stopPolling: () => void;
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
// withTimeout utility (same logic as useAgents.ts)
// ---------------------------------------------------------------------------

async function withTimeout<T>(
  promise: Promise<T>,
  timeoutMs: number,
  label: string,
): Promise<T> {
  let timeoutId: ReturnType<typeof setTimeout> | null = null;

  const timeoutPromise = new Promise<T>((_, reject) => {
    timeoutId = setTimeout(() => {
      reject(new Error(`${label} timeout`));
    }, timeoutMs);
  });

  try {
    return await Promise.race([promise, timeoutPromise]);
  } finally {
    if (timeoutId !== null) {
      clearTimeout(timeoutId);
    }
  }
}

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
  let pollIntervalId: ReturnType<typeof setInterval> | null = null;
  let retryTimeoutId: ReturnType<typeof setTimeout> | null = null;
  let countdownIntervalId: ReturnType<typeof setInterval> | null = null;
  let staleBannerTimeoutId: ReturnType<typeof setTimeout> | null = null;
  let fetchInProgress = false;
  let fetchStartTime = 0;
  let currentRetryDelay = INITIAL_RETRY_DELAY_S;
  let consecutiveFailuresAtCeiling = 0;
  let visibilityHandler: (() => void) | null = null;
  let isPolling = false;
  // Active workspace ID set by startPolling. fetchData reads it per call so
  // callers don't pass it on every invocation, and so retryNow/visibility
  // refetches use the same workspace as the polling loop.
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
      // Increase backoff for next attempt
      currentRetryDelay = Math.min(
        currentRetryDelay * BACKOFF_MULTIPLIER,
        MAX_RETRY_DELAY_S,
      );
      void get().fetchData();
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

  // --- Store ---

  const store = createStore<AgentStore>((set, get) => ({
    ...INITIAL_STATE,

    async fetchData(): Promise<void> {
      if (fetchInProgress) return;

      fetchInProgress = true;
      fetchStartTime = Date.now();
      set({ isLoading: true });

      // Pin wsID so awaits in this cycle can't splice data across a
      // workspace switch.
      const wsID = activeWorkspaceID;

      try {
        const agentsResult = await withTimeout(
          fetchAgents(wsID),
          FETCH_TIMEOUT_MS,
          "Agent fetch",
        );
        if (wsID !== activeWorkspaceID) return;

        // Primary success
        const now = Date.now();
        set(
          skipIfEqual(
            {
              agents: agentsResult,
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

        // Derive connection state
        const state = get();
        set({
          connectionState: deriveConnectionState(
            true,
            false,
            state.wasEverConnected,
            state.retryCountdown,
          ),
        });

        // Fire secondary fetches (fire-and-forget)
        void (async () => {
          try {
            const statusResult = await withTimeout(
              fetchStatus(wsID),
              FETCH_TIMEOUT_MS,
              "Status fetch",
            );
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
          } catch (statusError) {
            console.warn(
              "Loom status fetch failed:",
              statusError instanceof Error
                ? statusError.message
                : String(statusError),
            );
          }
        })();

        void (async () => {
          try {
            const tasksResult = await withTimeout(
              fetchTasks(wsID),
              FETCH_TIMEOUT_MS,
              "Tasks fetch",
            );
            if (wsID !== activeWorkspaceID) return;
            set(
              skipIfEqual({ taskLists: tasksResult }, get(), [
                "taskLists",
              ] as const),
            );
          } catch (taskError) {
            console.warn(
              "Loom tasks fetch failed:",
              taskError instanceof Error
                ? taskError.message
                : String(taskError),
            );
          }
        })();
      } catch (err) {
        // Primary failure
        const error = err instanceof Error ? err : new Error(String(err));
        set({
          error,
          isConnected: false,
          isLoading: false,
        });

        reportFailure(set, get);

        // Derive connection state
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
        fetchInProgress = false;
      }
    },

    startPolling(options?: PollingOptions): void {
      // If already polling, stop first
      if (isPolling) {
        get().stopPolling();
      }

      // Empty workspaceId would cause every poll tick to fire a doomed
      // /api/workspaces//monitor/agents request — the backend rejects it and
      // the store would churn state on each tick. The caller hasn't resolved
      // a workspace yet; wait for the next startPolling call with a real ID.
      const wsID = options?.workspaceId ?? "";
      if (wsID === "") {
        return;
      }

      const interval = options?.pollInterval ?? 5000;

      // Reset backoff so a new workspace doesn't inherit stale retry delay
      // from a previous workspace's failure streak.
      if (wsID !== activeWorkspaceID) {
        currentRetryDelay = INITIAL_RETRY_DELAY_S;
        consecutiveFailuresAtCeiling = 0;
      }

      activeWorkspaceID = wsID;
      isPolling = true;

      // Initial fetch
      void get().fetchData();

      // Setup polling with watchdog (only if interval > 0)
      if (interval > 0) {
        pollIntervalId = setInterval(() => {
          // Watchdog: if previous fetch has been running past the timeout
          if (
            fetchInProgress &&
            Date.now() - fetchStartTime > WATCHDOG_TIMEOUT_MS
          ) {
            console.warn(
              "Agent fetch watchdog: force-resetting stale fetch lock",
            );
            fetchInProgress = false;
          }
          void get().fetchData();
        }, interval);
      }

      // Visibility change handler (guarded for non-browser environments)
      if (typeof document !== "undefined") {
        visibilityHandler = () => {
          if (document.visibilityState === "visible") {
            // Force-reset in case previous fetch was orphaned during background
            fetchInProgress = false;
            void get().fetchData();
          }
        };
        document.addEventListener("visibilitychange", visibilityHandler);
      }
    },

    stopPolling(): void {
      if (pollIntervalId) {
        clearInterval(pollIntervalId);
        pollIntervalId = null;
      }

      clearRetryTimers();
      clearStaleBannerTimer();

      if (visibilityHandler && typeof document !== "undefined") {
        document.removeEventListener("visibilitychange", visibilityHandler);
        visibilityHandler = null;
      }

      isPolling = false;

      // Reset retry state and re-derive connectionState
      const state = get();
      set({
        retryCountdown: 0,
        connectionState: deriveConnectionState(
          state.isConnected,
          false,
          state.wasEverConnected,
          0,
        ),
      });
    },

    retryNow(): void {
      clearRetryTimers();
      currentRetryDelay = INITIAL_RETRY_DELAY_S;
      consecutiveFailuresAtCeiling = 0;
      set({ connectionLost: false, retryCountdown: 0 });
      void get().fetchData();
    },

    getAgentByName(name: string): LoomAgentStatus | undefined {
      return get().agents.find((a) => a.name === name);
    },

    reset(): void {
      get().stopPolling();

      fetchInProgress = false;
      fetchStartTime = 0;
      currentRetryDelay = INITIAL_RETRY_DELAY_S;
      consecutiveFailuresAtCeiling = 0;
      isPolling = false;

      set({ ...INITIAL_STATE });
    },
  }));

  return store;
}
