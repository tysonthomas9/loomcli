/**
 * useAgents - React hook for fetching workspace-scoped agent status.
 * Provides real-time agent status with automatic polling.
 */

import { useState, useEffect, useCallback, useRef } from "react";

import { fetchWorkspaceAgents } from "@/api";
import type {
  LoomAgentStatus,
  LoomConnectionState,
} from "@/types";

/**
 * Options for the useAgents hook.
 */
export interface UseAgentsOptions {
  /** Poll interval in ms (default: 5000) */
  pollInterval?: number;
  /** Whether to fetch (default: true) */
  enabled?: boolean;
  /** Workspace ID — required for fetching agents */
  workspaceId?: string;
}

/**
 * Return type for the useAgents hook.
 */
export interface UseAgentsResult {
  /** Agent status data, empty array if not yet loaded or server unavailable */
  agents: LoomAgentStatus[];
  /** Whether a fetch is currently in progress */
  isLoading: boolean;
  /** Whether the server is available */
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

// Retry backoff configuration
const INITIAL_RETRY_DELAY = 5; // seconds
const MAX_RETRY_DELAY = 60; // seconds
const BACKOFF_MULTIPLIER = 2;
const LOOM_FETCH_TIMEOUT_MS = 10000;

// Stale banner delay (ms) — show banner when disconnected >5s
const STALE_BANNER_DELAY_MS = 5000;

// Max consecutive failures at MAX_RETRY_DELAY before declaring connection lost
const MAX_FAILURES_AT_CEILING = 5;

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
  const { pollInterval = 5000, enabled = true, workspaceId } = options ?? {};

  const [agents, setAgents] = useState<LoomAgentStatus[]>([]);
  const [isLoading, setIsLoading] = useState<boolean>(false);
  const [isConnected, setIsConnected] = useState<boolean>(false);
  const [wasEverConnected, setWasEverConnected] = useState<boolean>(false);
  const [retryCountdown, setRetryCountdown] = useState<number>(0);
  const [error, setError] = useState<Error | null>(null);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);

  // Stale data banner state
  const [showStaleBanner, setShowStaleBanner] = useState(false);
  const [connectionLost, setConnectionLost] = useState(false);
  const [disconnectedSince, setDisconnectedSince] = useState<number | null>(
    null,
  );
  const staleBannerTimerRef = useRef<ReturnType<typeof setTimeout> | null>(
    null,
  );
  const consecutiveFailuresAtCeilingRef = useRef(0);
  const disconnectedSinceRef = useRef<number | null>(null);

  // Stable ref for workspaceId so fetchData callback doesn't need it as a dep
  const workspaceIdRef = useRef(workspaceId);
  workspaceIdRef.current = workspaceId;

  // Clear stale agents immediately when workspace changes.
  useEffect(() => {
    setAgents([]);
  }, [workspaceId]);

  // Track if a fetch is in progress to prevent overlapping requests
  const fetchInProgressRef = useRef<boolean>(false);

  // Track fetch start time for watchdog
  const fetchStartTimeRef = useRef<number>(0);

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
    if (isConnected) return "connected";
    if (isLoading && !wasEverConnected) return "never_connected";
    if (retryCountdown > 0) return "reconnecting";
    if (!wasEverConnected) return "never_connected";
    return "disconnected";
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
  const fetchData = useCallback(async () => {
    // Skip if already fetching
    if (fetchInProgressRef.current) {
      return;
    }

    const wsId = workspaceIdRef.current;
    if (!wsId) {
      // No workspace — can't fetch workspace-scoped agents
      return;
    }

    fetchInProgressRef.current = true;
    fetchStartTimeRef.current = Date.now();
    setIsLoading(true);
    clearRetryTimers();

    try {
      const wsAgents = await withTimeout(
        fetchWorkspaceAgents(wsId),
        LOOM_FETCH_TIMEOUT_MS,
        "Workspace agents fetch",
      );

      // Guard: discard if workspace changed during the fetch (TOCTOU fix).
      if (mountedRef.current && workspaceIdRef.current === wsId) {
        setAgents(wsAgents);
        setIsConnected(true);
        setWasEverConnected(true);
        currentRetryDelayRef.current = INITIAL_RETRY_DELAY;
        consecutiveFailuresAtCeilingRef.current = 0;
        setError(null);
        setLastUpdated(new Date());
        // Clear stale banner on successful connection
        if (staleBannerTimerRef.current) {
          clearTimeout(staleBannerTimerRef.current);
          staleBannerTimerRef.current = null;
        }
        setShowStaleBanner(false);
        setConnectionLost(false);
        disconnectedSinceRef.current = null;
        setDisconnectedSince(null);
      }
    } catch (err) {
      // Only update state if still mounted
      if (mountedRef.current) {
        setError(err instanceof Error ? err : new Error(String(err)));
        setIsConnected(false);
        // Start stale banner timer on first disconnect
        if (
          wasEverConnectedRef.current &&
          disconnectedSinceRef.current === null &&
          !staleBannerTimerRef.current
        ) {
          const now = Date.now();
          disconnectedSinceRef.current = now;
          setDisconnectedSince(now);
          staleBannerTimerRef.current = setTimeout(() => {
            setShowStaleBanner(true);
          }, STALE_BANNER_DELAY_MS);
        }
        // Track consecutive failures at max delay to detect connection lost
        if (currentRetryDelayRef.current >= MAX_RETRY_DELAY) {
          consecutiveFailuresAtCeilingRef.current += 1;
          if (
            consecutiveFailuresAtCeilingRef.current >= MAX_FAILURES_AT_CEILING
          ) {
            setConnectionLost(true);
          }
        }
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
    if (
      !error ||
      !wasEverConnected ||
      isConnected ||
      fetchInProgressRef.current
    ) {
      return;
    }

    if (retryTimeoutRef.current || retryIntervalRef.current) {
      return;
    }

    const delay = currentRetryDelayRef.current;
    setRetryCountdown(delay);

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
      currentRetryDelayRef.current = Math.min(
        currentRetryDelayRef.current * BACKOFF_MULTIPLIER,
        MAX_RETRY_DELAY,
      );
      void fetchData();
    }, delay * 1000);

    retryIntervalRef.current = intervalId;
    retryTimeoutRef.current = timeoutId;

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
    currentRetryDelayRef.current = INITIAL_RETRY_DELAY;
    consecutiveFailuresAtCeilingRef.current = 0;
    setConnectionLost(false);
    void fetchData();
  }, [clearRetryTimers, fetchData]);

  // Initial fetch and polling setup
  useEffect(() => {
    mountedRef.current = true;

    if (!enabled) {
      return;
    }

    void fetchData();

    let intervalId: ReturnType<typeof setInterval> | null = null;
    if (pollInterval > 0) {
      intervalId = setInterval(() => {
        const fetchAge = Date.now() - fetchStartTimeRef.current;
        if (fetchInProgressRef.current && fetchAge > 20000) {
          console.warn("Loom fetch watchdog: force-resetting stale fetch lock");
          fetchInProgressRef.current = false;
        }
        void fetchData();
      }, pollInterval);
    }

    const handleVisibilityChange = () => {
      if (document.visibilityState === "visible") {
        fetchInProgressRef.current = false;
        void fetchData();
      }
    };
    document.addEventListener("visibilitychange", handleVisibilityChange);

    return () => {
      mountedRef.current = false;
      if (intervalId) {
        clearInterval(intervalId);
      }
      document.removeEventListener("visibilitychange", handleVisibilityChange);
      if (retryTimeoutRef.current) {
        clearTimeout(retryTimeoutRef.current);
      }
      if (retryIntervalRef.current) {
        clearInterval(retryIntervalRef.current);
      }
      if (staleBannerTimerRef.current) {
        clearTimeout(staleBannerTimerRef.current);
      }
    };
  }, [enabled, pollInterval, fetchData]);

  return {
    agents,
    isLoading,
    isConnected,
    connectionState,
    wasEverConnected,
    retryCountdown,
    error,
    lastUpdated,
    refetch,
    retryNow,
    showStaleBanner,
    connectionLost,
    disconnectedSince,
  };
}
