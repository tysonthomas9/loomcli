/**
 * Zustand vanilla store for agent state management.
 * Replaces useAgents hook (371 lines) with a single testable, framework-agnostic store.
 * Owns its own polling lifecycle (interval-based fetching, exponential backoff,
 * watchdog, visibility-change refetch) as vanilla JS — no React hooks needed.
 */

import { createStore, type StoreApi } from "zustand/vanilla";

import { fetchAgents, fetchStatus, fetchTasks } from "../api/agents";
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

const DEFAULT_TASKS: LoomTaskSummary = {
  needs_planning: 0,
  ready_to_implement: 0,
  in_progress: 0,
  need_review: 0,
  backlog: 0,
  epics: 0,
};

const DEFAULT_SYNC: LoomSyncInfo = {
  db_synced: true,
  db_last_sync: "",
  git_needs_push: 0,
  git_needs_pull: 0,
};

const DEFAULT_STATS: LoomStats = {
  open: 0,
  closed: 0,
  total: 0,
  completion: 0,
  remaining: 0,
  in_progress: 0,
  review: 0,
  blocked: 0,
};

const DEFAULT_TASK_LISTS: LoomTaskLists = {
  needsPlanning: [],
  readyToImplement: [],
  needsReview: [],
  inProgress: [],
  backlog: [],
  done: [],
};

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

export interface AgentStoreConfig {
  onToast?: (message: string) => void;
}

export interface PollingOptions {
  pollInterval?: number; // ms, default 5000
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

      try {
        const agentsResult = await withTimeout(
          fetchAgents(),
          FETCH_TIMEOUT_MS,
          "Agent fetch",
        );

        // Primary success
        const now = Date.now();
        set({
          agents: agentsResult,
          isConnected: true,
          error: null,
          lastUpdated: now,
          isLoading: false,
        });

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
              fetchStatus(),
              FETCH_TIMEOUT_MS,
              "Status fetch",
            );
            set({
              tasks: statusResult.tasks,
              agentTasks: statusResult.agentTasks,
              sync: statusResult.sync,
              stats: statusResult.stats,
            });
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
              fetchTasks(),
              FETCH_TIMEOUT_MS,
              "Tasks fetch",
            );
            set({ taskLists: tasksResult });
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

      const interval = options?.pollInterval ?? 5000;
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
