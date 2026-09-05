/**
 * Zustand vanilla store for agent state management.
 * Replaces useAgents hook (371 lines) with a single testable, framework-agnostic store.
 * Owns its own polling lifecycle (interval-based fetching, exponential backoff,
 * watchdog, visibility-change refetch) as vanilla JS — no React hooks needed.
 */

import { createStore, type StoreApi } from "zustand/vanilla";

import { fetchStatus } from "../api/agents";
import { resolveAgentByName } from "../types";
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
  workspaceId?: string;
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
  refreshForRecovery: (
    signal: AbortSignal,
    expectedWorkspaceId?: string,
  ) => Promise<void>;
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
  signal: AbortSignal,
): Promise<T> {
  let timeoutId: ReturnType<typeof setTimeout> | null = null;

  const timeoutPromise = new Promise<T>((_, reject) => {
    timeoutId = setTimeout(() => {
      reject(new Error(`${label} timeout`));
    }, timeoutMs);
  });

  let onAbort = (): void => {};
  const aborted = new Promise<never>((_, reject) => {
    onAbort = () => reject(signal.reason ?? new Error("Status fetch aborted"));
    signal.addEventListener("abort", onAbort, { once: true });
    if (signal.aborted) onAbort();
  });
  try {
    return await Promise.race([promise, timeoutPromise, aborted]);
  } finally {
    signal.removeEventListener("abort", onAbort);
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
  let activeWorkspaceId: string | undefined;
  let staleBannerTimeoutId: ReturnType<typeof setTimeout> | null = null;
  let fetchInProgress = false;
  let generation = 0;
  let activeRequest: AbortController | null = null;
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

  // A recovery fetch always supersedes older work. The status endpoint is
  // workspace-wide; callers abort the recovery signal if their repo scope changes.
  async function runFetch(
    set: (partial: Partial<AgentStore>) => void,
    get: () => AgentStore,
    externalSignal?: AbortSignal,
  ): Promise<void> {
    externalSignal?.throwIfAborted();
    activeRequest?.abort(new Error("Status fetch superseded"));
    const controller = new AbortController();
    activeRequest = controller;
    const onAbort = (): void => controller.abort(externalSignal?.reason);
    externalSignal?.addEventListener("abort", onAbort, { once: true });
    generation++;
    fetchInProgress = true;
    const fetchGeneration = generation;
    const workspaceId = activeWorkspaceId;
    const assertCurrent = (): void => {
      controller.signal.throwIfAborted();
      if (fetchGeneration !== generation)
        throw new Error("Status fetch superseded");
    };
    set({ isLoading: true });

    try {
      assertCurrent();
      const statusResult = await withTimeout(
        fetchStatus(workspaceId),
        FETCH_TIMEOUT_MS,
        "Status fetch",
        controller.signal,
      );

      assertCurrent();

      // Primary success
      const now = Date.now();
      set({
        agents: statusResult.agents,
        tasks: statusResult.tasks,
        taskLists: statusResult.taskLists,
        agentTasks: statusResult.agentTasks,
        sync: statusResult.sync,
        stats: statusResult.stats,
        isConnected: true,
        error: null,
        lastUpdated: now,
        isLoading: false,
      });

      assertCurrent();
      reportSuccess(set);
      assertCurrent();

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
      assertCurrent();
    } catch (err) {
      assertCurrent();

      // Primary failure
      const error = err instanceof Error ? err : new Error(String(err));
      set({
        error,
        isConnected: false,
        isLoading: false,
      });

      assertCurrent();
      reportFailure(set, get);
      assertCurrent();

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
      throw error;
    } finally {
      externalSignal?.removeEventListener("abort", onAbort);
      if (fetchGeneration === generation) {
        fetchInProgress = false;
        activeRequest = null;
        if (controller.signal.aborted) set({ isLoading: false });
      }
    }
  }

  // --- Store ---

  const store = createStore<AgentStore>((set, get) => ({
    ...INITIAL_STATE,

    async fetchData(): Promise<void> {
      if (fetchInProgress) return;
      // Legacy polling callers observe failures through store state.
      await runFetch(set, get).catch(() => {});
    },

    refreshForRecovery(
      signal: AbortSignal,
      expectedWorkspaceId?: string,
    ): Promise<void> {
      if (
        expectedWorkspaceId !== undefined &&
        expectedWorkspaceId !== activeWorkspaceId
      ) {
        return Promise.reject(new Error("Status recovery workspace mismatch"));
      }
      return runFetch(set, get, signal);
    },

    startPolling(options?: PollingOptions): void {
      const nextWorkspaceId = options?.workspaceId;
      const workspaceChanged = nextWorkspaceId !== activeWorkspaceId;

      // If already polling, stop first
      if (isPolling) {
        get().stopPolling();
      }

      if (workspaceChanged) {
        activeRequest?.abort(new Error("Status workspace changed"));
        generation++;
        fetchInProgress = false;
      }
      activeWorkspaceId = nextWorkspaceId;
      const interval = options?.pollInterval ?? 5000;
      isPolling = true;

      // Initial fetch
      void get().fetchData();

      // Setup polling with watchdog (only if interval > 0)
      if (interval > 0) {
        // No watchdog is needed to recover a stuck lock. withTimeout races
        // every request against FETCH_TIMEOUT_MS, so the call always settles
        // even when the underlying promise never does, and the finally block
        // releases the lock. A watchdog above that timeout could never fire,
        // and one below it would discard healthy slow requests.
        pollIntervalId = setInterval(() => {
          void get().fetchData();
        }, interval);
      }

      // Visibility change handler (guarded for non-browser environments)
      if (typeof document !== "undefined") {
        visibilityHandler = () => {
          if (document.visibilityState === "visible") {
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
      return resolveAgentByName(get().agents, name);
    },

    reset(): void {
      get().stopPolling();

      activeRequest?.abort(new Error("Status store reset"));
      generation++;
      fetchInProgress = false;
      currentRetryDelay = INITIAL_RETRY_DELAY_S;
      consecutiveFailuresAtCeiling = 0;
      isPolling = false;
      activeWorkspaceId = undefined;

      set({ ...INITIAL_STATE });
    },
  }));

  return store;
}
