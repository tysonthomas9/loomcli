/**
 * useWorkspaceRepos - React hook for workspace repository data.
 * Fetches workspace config on mount with auto-retry on failure.
 * Tracks connection state to distinguish never-connected vs lost-connection.
 */

import { useState, useCallback, useEffect, useRef } from "react";

import { fetchWorkspace } from "@/api/workspace";
import type { WorkspaceData, RepoInfo } from "@/api/workspace";
import { calculateBackoffDelay } from "@/utils/reconnectBackoff";

/** Connection state for workspace data fetching. */
export type WorkspaceConnectionState =
  | "loading"
  | "connected"
  | "error_never_connected"
  | "error_lost_connection";

export interface UseWorkspaceReposReturn {
  /** Full workspace data, null if not loaded */
  workspace: WorkspaceData | null;
  /** Convenience alias for workspace.repos (empty array if not loaded) */
  repos: RepoInfo[];
  /** Whether a fetch is in progress */
  isLoading: boolean;
  /** Error message from the last fetch */
  error: string | null;
  /** Re-fetch workspace data from the API */
  refetch: () => void;
  /** Current connection state */
  connectionState: WorkspaceConnectionState;
  /** Seconds until next auto-retry, null if not retrying */
  retryCountdown: number | null;
  /** Immediate retry, resets backoff */
  retryNow: () => void;
  /** True if at least one fetch succeeded */
  hasEverConnected: boolean;
}

const BACKOFF_CONFIG = {
  baseDelay: 5000,
  maxDelay: 60000,
  maxAttempts: 10,
  jitterFactor: 0.5,
};

/**
 * React hook for workspace repository data.
 * Fetches from GET /api/workspace on mount. Auto-retries with exponential
 * backoff on failure. Preserves stale data on lost-connection.
 */
export function useWorkspaceRepos(): UseWorkspaceReposReturn {
  const [workspace, setWorkspace] = useState<WorkspaceData | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [connectionState, setConnectionState] =
    useState<WorkspaceConnectionState>("loading");
  const [retryCountdown, setRetryCountdown] = useState<number | null>(null);
  const [hasEverConnected, setHasEverConnected] = useState(false);

  const mountedRef = useRef(true);
  const hasConnectedRef = useRef(false);
  const attemptRef = useRef(0);
  const retryTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const countdownTimerRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const nextRetryAtRef = useRef<number | null>(null);
  const fetchDataRef = useRef<() => Promise<void>>(async () => {});

  // Unmount guard — reset true on (re)mount, false on cleanup
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const clearTimers = useCallback(() => {
    if (retryTimerRef.current !== null) {
      clearTimeout(retryTimerRef.current);
      retryTimerRef.current = null;
    }
    if (countdownTimerRef.current !== null) {
      clearInterval(countdownTimerRef.current);
      countdownTimerRef.current = null;
    }
    nextRetryAtRef.current = null;
  }, []);

  const startCountdown = useCallback((nextRetryAt: number) => {
    nextRetryAtRef.current = nextRetryAt;

    // Set initial countdown
    const remaining = Math.max(0, Math.ceil((nextRetryAt - Date.now()) / 1000));
    setRetryCountdown(remaining);

    // Update countdown every second
    countdownTimerRef.current = setInterval(() => {
      const now = Date.now();
      const secsLeft = Math.max(0, Math.ceil((nextRetryAt - now) / 1000));
      setRetryCountdown(secsLeft);
      if (secsLeft <= 0 && countdownTimerRef.current !== null) {
        clearInterval(countdownTimerRef.current);
        countdownTimerRef.current = null;
      }
    }, 1000);
  }, []);

  // scheduleRetry reads fetchDataRef to avoid circular useCallback dependency
  const scheduleRetry = useCallback(() => {
    if (!mountedRef.current) return;
    if (attemptRef.current >= BACKOFF_CONFIG.maxAttempts) {
      // Max retries exhausted — stop auto-retrying
      setRetryCountdown(null);
      return;
    }

    const delay = calculateBackoffDelay(attemptRef.current, BACKOFF_CONFIG);
    const nextRetryAt = Date.now() + delay;

    startCountdown(nextRetryAt);

    retryTimerRef.current = setTimeout(() => {
      retryTimerRef.current = null;
      if (mountedRef.current) {
        fetchDataRef.current();
      }
    }, delay);
  }, [startCountdown]);

  const fetchData = useCallback(async () => {
    setIsLoading(true);
    setError(null);

    try {
      const data = await fetchWorkspace();
      if (mountedRef.current) {
        setWorkspace(data);
        hasConnectedRef.current = true;
        setHasEverConnected(true);
        attemptRef.current = 0;
        setConnectionState("connected");
        setRetryCountdown(null);
        clearTimers();
      }
    } catch (err) {
      if (mountedRef.current) {
        const message =
          err instanceof Error ? err.message : "Failed to load workspace data";
        setError(message);

        if (hasConnectedRef.current) {
          // Was connected before — keep stale workspace data
          setConnectionState("error_lost_connection");
        } else {
          setConnectionState("error_never_connected");
        }

        attemptRef.current++;
        scheduleRetry();
      }
    } finally {
      if (mountedRef.current) {
        setIsLoading(false);
      }
    }
  }, [clearTimers, scheduleRetry]);

  // Keep fetchDataRef in sync so scheduleRetry always calls latest version
  useEffect(() => {
    fetchDataRef.current = fetchData;
  });

  const retryNow = useCallback(() => {
    clearTimers();
    attemptRef.current = 0;
    setRetryCountdown(null);
    fetchData();
  }, [clearTimers, fetchData]);

  // Initial fetch
  useEffect(() => {
    fetchData();
  }, [fetchData]);

  // Cleanup all timers on unmount
  useEffect(() => {
    return () => {
      clearTimers();
    };
  }, [clearTimers]);

  return {
    workspace,
    repos: workspace?.repos ?? [],
    isLoading,
    error,
    refetch: fetchData,
    connectionState,
    retryCountdown,
    retryNow,
    hasEverConnected,
  };
}
