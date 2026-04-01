/**
 * useAgents - React hook for fetching agent status from loom server.
 * Provides real-time agent status with automatic polling.
 */

import { useState, useEffect, useCallback, useRef } from "react";

import { fetchAgents, fetchStatus, fetchTasks } from "@/api";
import type {
  LoomAgentStatus,
  LoomTaskSummary,
  LoomTaskInfo,
  LoomTaskLists,
  LoomSyncInfo,
  LoomStats,
  LoomConnectionState,
} from "@/types";

import { usePollingWithBackoff } from "./usePollingWithBackoff";

/**
 * Options for the useAgents hook.
 */
export interface UseAgentsOptions {
  /** Poll interval in ms (default: 5000) */
  pollInterval?: number;
  /** Whether to fetch (default: true) */
  enabled?: boolean;
}

/**
 * Return type for the useAgents hook.
 */
export interface UseAgentsResult {
  /** Agent status data, empty array if not yet loaded or server unavailable */
  agents: LoomAgentStatus[];
  /** Task queue summary counts */
  tasks: LoomTaskSummary;
  /** Task lists organized by category */
  taskLists: LoomTaskLists;
  /** Map of agent name to current task info (for task titles) */
  agentTasks: Record<string, LoomTaskInfo>;
  /** Sync status (DB and Git) */
  sync: LoomSyncInfo;
  /** Project statistics */
  stats: LoomStats;
  /** Whether a fetch is currently in progress */
  isLoading: boolean;
  /** Whether the loom server is available */
  isConnected: boolean;
  /** Connection state for detailed UI feedback */
  connectionState: LoomConnectionState;
  /** Whether we've ever successfully connected */
  wasEverConnected: boolean;
  /** Seconds until next auto-retry (0 if not waiting) */
  retryCountdown: number;
  /** Error from the last fetch attempt, null if successful */
  error: Error | null;
  /** Last successful update time */
  lastUpdated: Date | null;
  /** Function to manually trigger a refetch */
  refetch: () => Promise<void>;
  /** Function to retry immediately (skips countdown) */
  retryNow: () => void;
  /** True when disconnected >5s — data may be stale */
  showStaleBanner: boolean;
  /** True when reconnection failed after max retries */
  connectionLost: boolean;
  /** Timestamp (ms) when disconnection started, null if connected */
  disconnectedSince: number | null;
}

/**
 * React hook for fetching agent status from the loom server.
 *
 * @param options - Configuration options for the hook
 * @returns Object with agents data, loading/error states and refetch function
 *
 * @example
 * ```tsx
 * function AgentsSidebar() {
 *   const { agents, isLoading, isConnected, refetch } = useAgents({
 *     pollInterval: 5000, // Poll every 5 seconds
 *   });
 *
 *   if (!isConnected) {
 *     return <div>Loom server not available</div>;
 *   }
 *
 *   return (
 *     <div>
 *       {agents.map(agent => (
 *         <AgentCard key={agent.name} agent={agent} />
 *       ))}
 *     </div>
 *   );
 * }
 * ```
 */
// Default values for initial state
const DEFAULT_TASKS: LoomTaskSummary = {
  needs_planning: 0,
  ready_to_implement: 0,
  in_progress: 0,
  need_review: 0,
  backlog: 0,
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

const LOOM_FETCH_TIMEOUT_MS = 15000;

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

export function useAgents(options?: UseAgentsOptions): UseAgentsResult {
  const { pollInterval = 5000, enabled = true } = options ?? {};

  const [agents, setAgents] = useState<LoomAgentStatus[]>([]);
  const [tasks, setTasks] = useState<LoomTaskSummary>(DEFAULT_TASKS);
  const [taskLists, setTaskLists] = useState<LoomTaskLists>(DEFAULT_TASK_LISTS);
  const [agentTasks, setAgentTasks] = useState<Record<string, LoomTaskInfo>>(
    {},
  );
  const [sync, setSync] = useState<LoomSyncInfo>(DEFAULT_SYNC);
  const [stats, setStats] = useState<LoomStats>(DEFAULT_STATS);
  const [isLoading, setIsLoading] = useState<boolean>(false);
  const [isConnected, setIsConnected] = useState<boolean>(false);
  const [error, setError] = useState<Error | null>(null);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);

  // Track if a fetch is in progress to prevent overlapping requests
  const fetchInProgressRef = useRef<boolean>(false);

  // Track fetch start time for watchdog
  const fetchStartTimeRef = useRef<number>(0);

  // Track if the component is mounted for cleanup
  const mountedRef = useRef<boolean>(true);

  // Backoff hook — delegates retry scheduling, countdown, and stale-banner state
  const backoff = usePollingWithBackoff({
    onRetry: () => {
      void fetchData();
    },
    enabled,
    staleBannerDelayMs: 5000,
    maxFailuresAtCeiling: 5,
    repollOnVisibilityChange: false, // useAgents manages its own visibility handler
  });

  // Store backoff functions in refs to avoid fetchData dependency changes
  const reportSuccessRef = useRef(backoff.reportSuccess);
  const reportFailureRef = useRef(backoff.reportFailure);
  reportSuccessRef.current = backoff.reportSuccess;
  reportFailureRef.current = backoff.reportFailure;

  // Stable fetch function using useCallback
  const fetchData = useCallback(async () => {
    // Skip if already fetching
    if (fetchInProgressRef.current) {
      return;
    }

    fetchInProgressRef.current = true;
    fetchStartTimeRef.current = Date.now();
    setIsLoading(true);

    try {
      const agentsResult = await withTimeout(
        fetchAgents(),
        LOOM_FETCH_TIMEOUT_MS,
        "Loom agents fetch",
      );

      // Only update state if still mounted
      if (mountedRef.current) {
        setAgents(agentsResult);
        setIsConnected(true);
        setError(null);
        setLastUpdated(new Date());
        reportSuccessRef.current();
      }

      void (async () => {
        try {
          const statusResult = await withTimeout(
            fetchStatus(),
            LOOM_FETCH_TIMEOUT_MS,
            "Loom status fetch",
          );
          if (mountedRef.current) {
            setTasks(statusResult.tasks);
            setAgentTasks(statusResult.agentTasks);
            setSync(statusResult.sync);
            setStats(statusResult.stats);
          }
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
            LOOM_FETCH_TIMEOUT_MS,
            "Loom tasks fetch",
          );
          if (mountedRef.current) {
            setTaskLists(tasksResult);
          }
        } catch (taskError) {
          console.warn(
            "Loom tasks fetch failed:",
            taskError instanceof Error ? taskError.message : String(taskError),
          );
        }
      })();
    } catch (err) {
      // Only update state if still mounted
      if (mountedRef.current) {
        setError(err instanceof Error ? err : new Error(String(err)));
        setIsConnected(false);
        reportFailureRef.current();
      }
    } finally {
      if (mountedRef.current) {
        setIsLoading(false);
      }
      fetchInProgressRef.current = false;
    }
  }, []);

  // Compute connection state from other state values
  const connectionState: LoomConnectionState = (() => {
    if (isConnected) return "connected";
    if (isLoading && !backoff.wasEverConnected) return "never_connected";
    if (backoff.retryCountdown > 0) return "reconnecting";
    if (!backoff.wasEverConnected) return "never_connected";
    return "disconnected";
  })();

  // Refetch function exposed to consumers
  const refetch = useCallback(async () => {
    await fetchData();
  }, [fetchData]);

  // Retry immediately (skips countdown) — delegates to backoff hook
  const retryNow = useCallback(() => {
    backoff.retryNow();
  }, [backoff.retryNow]);

  // Initial fetch and polling setup
  useEffect(() => {
    mountedRef.current = true;

    // Don't fetch if disabled
    if (!enabled) {
      return;
    }

    // Initial fetch
    void fetchData();

    // Setup polling with watchdog
    let intervalId: ReturnType<typeof setInterval> | null = null;
    if (pollInterval > 0) {
      intervalId = setInterval(() => {
        // Watchdog: if previous fetch has been running for >20s (past the 15s timeout),
        // force-reset the lock to unblock polling
        const fetchAge = Date.now() - fetchStartTimeRef.current;
        if (fetchInProgressRef.current && fetchAge > 20000) {
          console.warn("Loom fetch watchdog: force-resetting stale fetch lock");
          fetchInProgressRef.current = false;
        }
        void fetchData();
      }, pollInterval);
    }

    // Visibility change handler: refetch immediately when tab becomes visible
    const handleVisibilityChange = () => {
      if (document.visibilityState === "visible") {
        // Force-reset in case previous fetch was orphaned during background
        fetchInProgressRef.current = false;
        void fetchData();
      }
    };
    document.addEventListener("visibilitychange", handleVisibilityChange);

    // Cleanup
    return () => {
      mountedRef.current = false;
      if (intervalId) {
        clearInterval(intervalId);
      }
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, [enabled, pollInterval, fetchData]);

  return {
    agents,
    tasks,
    taskLists,
    agentTasks,
    sync,
    stats,
    isLoading,
    isConnected,
    connectionState,
    wasEverConnected: backoff.wasEverConnected,
    retryCountdown: backoff.retryCountdown,
    error,
    lastUpdated,
    refetch,
    retryNow,
    showStaleBanner: backoff.showStaleBanner,
    connectionLost: backoff.connectionLost,
    disconnectedSince: backoff.disconnectedSince,
  };
}
