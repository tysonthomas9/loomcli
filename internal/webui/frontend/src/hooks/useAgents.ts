/**
 * useAgents - React hook for fetching agent status from loom server.
 * Provides real-time agent status with automatic polling.
 */

import { useState, useEffect, useCallback, useRef } from 'react';
import { fetchStatus, fetchTasks } from '@/api';
import type {
  LoomAgentStatus,
  LoomTaskSummary,
  LoomTaskInfo,
  LoomTaskLists,
  LoomSyncInfo,
  LoomStats,
  LoomConnectionState,
} from '@/types';

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
  blocked: 0,
};

const DEFAULT_SYNC: LoomSyncInfo = {
  db_synced: true,
  db_last_sync: '',
  git_needs_push: 0,
  git_needs_pull: 0,
};

const DEFAULT_STATS: LoomStats = {
  open: 0,
  closed: 0,
  total: 0,
  completion: 0,
};

const DEFAULT_TASK_LISTS: LoomTaskLists = {
  needsPlanning: [],
  readyToImplement: [],
  needsReview: [],
  inProgress: [],
  blocked: [],
};

// Retry backoff configuration
const INITIAL_RETRY_DELAY = 5; // seconds
const MAX_RETRY_DELAY = 60; // seconds
const BACKOFF_MULTIPLIER = 2;

export function useAgents(options?: UseAgentsOptions): UseAgentsResult {
  const { pollInterval = 5000, enabled = true } = options ?? {};

  const [agents, setAgents] = useState<LoomAgentStatus[]>([]);
  const [tasks, setTasks] = useState<LoomTaskSummary>(DEFAULT_TASKS);
  const [taskLists, setTaskLists] = useState<LoomTaskLists>(DEFAULT_TASK_LISTS);
  const [agentTasks, setAgentTasks] = useState<Record<string, LoomTaskInfo>>({});
  const [sync, setSync] = useState<LoomSyncInfo>(DEFAULT_SYNC);
  const [stats, setStats] = useState<LoomStats>(DEFAULT_STATS);
  const [isLoading, setIsLoading] = useState<boolean>(false);
  const [isConnected, setIsConnected] = useState<boolean>(false);
  const [wasEverConnected, setWasEverConnected] = useState<boolean>(false);
  const [retryCountdown, setRetryCountdown] = useState<number>(0);
  const [error, setError] = useState<Error | null>(null);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);

  // Track if a fetch is in progress to prevent overlapping requests
  const fetchInProgressRef = useRef<boolean>(false);

  // Track if the component is mounted for cleanup
  const mountedRef = useRef<boolean>(true);

  // Track retry state
  const retryTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const retryIntervalRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const currentRetryDelayRef = useRef<number>(INITIAL_RETRY_DELAY);
  const wasEverConnectedRef = useRef<boolean>(false);

  // Keep ref in sync with state
  wasEverConnectedRef.current = wasEverConnected;

  // Compute connection state from other state values
  const connectionState: LoomConnectionState = (() => {
    if (isConnected) return 'connected';
    if (isLoading && !wasEverConnected) return 'never_connected';
    if (retryCountdown > 0) return 'reconnecting';
    if (!wasEverConnected) return 'never_connected';
    return 'disconnected';
  })();

  // Clear any pending retry timers
  const clearRetryTimers = useCallback(() => {
    if (retryTimeoutRef.current) {
      clearTimeout(retryTimeoutRef.current);
      retryTimeoutRef.current = null;
    }
    if (retryIntervalRef.current) {
      clearInterval(retryIntervalRef.current);
      retryIntervalRef.current = null;
    }
    setRetryCountdown(0);
  }, []);

  // Stable fetch function using useCallback
  // Note: scheduleRetry is defined after but uses fetchDataRef to avoid circular deps
  const fetchData = useCallback(async () => {
    // Skip if already fetching
    if (fetchInProgressRef.current) {
      return;
    }

    fetchInProgressRef.current = true;
    setIsLoading(true);
    clearRetryTimers();

    try {
      // Fetch status and task lists in parallel
      const [statusResult, tasksResult] = await Promise.all([fetchStatus(), fetchTasks()]);

      // Only update state if still mounted
      if (mountedRef.current) {
        setAgents(statusResult.agents);
        setTasks(statusResult.tasks);
        setTaskLists(tasksResult);
        setAgentTasks(statusResult.agentTasks);
        setSync(statusResult.sync);
        setStats(statusResult.stats);
        // Connected if we successfully got a response (no error thrown)
        setIsConnected(true);
        setWasEverConnected(true);
        // Reset retry delay on successful connection
        currentRetryDelayRef.current = INITIAL_RETRY_DELAY;
        setError(null);
        setLastUpdated(new Date());
      }
    } catch (err) {
      // Only update state if still mounted
      if (mountedRef.current) {
        setError(err instanceof Error ? err : new Error(String(err)));
        setIsConnected(false);
        // Keep stale data on error - retry scheduling handled by effect
      }
    } finally {
      if (mountedRef.current) {
        setIsLoading(false);
      }
      fetchInProgressRef.current = false;
    }
  }, [clearRetryTimers]);

  // Schedule retry when disconnected after being connected
  useEffect(() => {
    // Only schedule retry if we were connected and now have an error, and no retry is in progress
    if (!error || !wasEverConnected || isConnected || fetchInProgressRef.current) {
      return;
    }

    // Don't schedule if timers are already set (retryCountdown > 0 means we already scheduled)
    if (retryTimeoutRef.current || retryIntervalRef.current) {
      return;
    }

    const delay = currentRetryDelayRef.current;
    setRetryCountdown(delay);

    // Create both timers
    const intervalId = setInterval(() => {
      if (!mountedRef.current) {
        clearInterval(intervalId);
        return;
      }
      setRetryCountdown((prev) => {
        if (prev <= 1) {
          clearInterval(intervalId);
          if (retryIntervalRef.current === intervalId) {
            retryIntervalRef.current = null;
          }
          return 0;
        }
        return prev - 1;
      });
    }, 1000);
    const timeoutId = setTimeout(() => {
      if (!mountedRef.current) return;
      clearRetryTimers();
      // Increase delay for next retry (exponential backoff)
      currentRetryDelayRef.current = Math.min(
        currentRetryDelayRef.current * BACKOFF_MULTIPLIER,
        MAX_RETRY_DELAY
      );
      void fetchData();
    }, delay * 1000);

    // Set refs IMMEDIATELY after timer creation (same sync block)
    // Prevents race where second effect invocation passes the ref check
    // before first invocation has set its refs
    retryIntervalRef.current = intervalId;
    retryTimeoutRef.current = timeoutId;

    // Cleanup: clear timers if effect dependencies change or component unmounts
    return () => {
      clearInterval(intervalId);
      clearTimeout(timeoutId);
      if (retryIntervalRef.current === intervalId) {
        retryIntervalRef.current = null;
      }
      if (retryTimeoutRef.current === timeoutId) {
        retryTimeoutRef.current = null;
      }
    };
  }, [error, wasEverConnected, isConnected, clearRetryTimers, fetchData]);

  // Refetch function exposed to consumers
  const refetch = useCallback(async () => {
    await fetchData();
  }, [fetchData]);

  // Retry immediately (skips countdown)
  const retryNow = useCallback(() => {
    clearRetryTimers();
    // Reset retry delay when user manually retries
    currentRetryDelayRef.current = INITIAL_RETRY_DELAY;
    void fetchData();
  }, [clearRetryTimers, fetchData]);

  // Initial fetch and polling setup
  useEffect(() => {
    mountedRef.current = true;

    // Don't fetch if disabled
    if (!enabled) {
      return;
    }

    // Initial fetch
    void fetchData();

    // Setup polling
    let intervalId: ReturnType<typeof setInterval> | null = null;
    if (pollInterval > 0) {
      intervalId = setInterval(() => {
        void fetchData();
      }, pollInterval);
    }

    // Cleanup
    return () => {
      mountedRef.current = false;
      if (intervalId) {
        clearInterval(intervalId);
      }
      // Clear retry timers on unmount
      if (retryTimeoutRef.current) {
        clearTimeout(retryTimeoutRef.current);
      }
      if (retryIntervalRef.current) {
        clearInterval(retryIntervalRef.current);
      }
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
    wasEverConnected,
    retryCountdown,
    error,
    lastUpdated,
    refetch,
    retryNow,
  };
}
