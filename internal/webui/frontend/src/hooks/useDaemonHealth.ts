/**
 * useDaemonHealth - React hook for daemon availability monitoring.
 * Polls GET /api/health with exponential backoff and provides daemon
 * availability state to the app. Follows the same backoff pattern as useAgents.
 */

import { useState, useCallback, useEffect, useRef } from "react";

import { checkDaemonHealth } from "@/api/health";

// ============= Constants =============

/** Initial retry delay in seconds. */
const INITIAL_RETRY_DELAY = 5;
/** Maximum retry delay in seconds (cap). */
const MAX_RETRY_DELAY = 60;
/** Backoff multiplier for exponential increase. */
const BACKOFF_MULTIPLIER = 2;
/**
 * Debounce before showing overlay (ms). Daemon must be unreachable for
 * this long before the overlay appears. Prevents flash on brief blips.
 * Not applied in never-connected state (show immediately).
 */
const UNAVAILABLE_DEBOUNCE_MS = 2000;

// ============= Types =============

/** Connection mode distinguishing first-time vs reconnection scenarios. */
export type DaemonConnectionMode =
  | "never_connected"
  | "connected"
  | "lost_connection"
  | "reconnecting";

/** Return type for the useDaemonHealth hook. */
export interface UseDaemonHealthReturn {
  /** Whether the daemon is currently available. */
  isDaemonAvailable: boolean;
  /** Whether a health check is in progress. */
  isChecking: boolean;
  /** Whether the daemon was ever successfully connected. */
  wasEverConnected: boolean;
  /** Current connection mode for overlay messaging. */
  connectionMode: DaemonConnectionMode;
  /** Seconds until next retry (0 if not waiting). */
  retryCountdown: number;
  /** Last error message from health check, if any. */
  lastError: string | null;
  /** Trigger an immediate retry, resetting backoff. */
  retryNow: () => void;
}

// ============= Hook =============

export function useDaemonHealth(): UseDaemonHealthReturn {
  const [isDaemonAvailable, setIsDaemonAvailable] = useState(true);
  const [isChecking, setIsChecking] = useState(false);
  const [wasEverConnected, setWasEverConnected] = useState(false);
  const [connectionMode, setConnectionMode] =
    useState<DaemonConnectionMode>("never_connected");
  const [retryCountdown, setRetryCountdown] = useState(0);
  const [lastError, setLastError] = useState<string | null>(null);

  const mountedRef = useRef(true);
  const currentRetryDelayRef = useRef(INITIAL_RETRY_DELAY);
  const countdownIntervalRef = useRef<ReturnType<typeof setInterval> | null>(
    null,
  );
  const retryTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  /** Timestamp (ms) when daemon first became unavailable (pre-debounce). */
  const unavailableSinceRef = useRef<number | null>(null);
  /** Whether the initial health check has completed. */
  const initialCheckDoneRef = useRef(false);
  /** Ref to avoid stale closure for wasEverConnected. */
  const wasEverConnectedRef = useRef(false);

  // Cleanup on unmount
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      if (countdownIntervalRef.current !== null) {
        clearInterval(countdownIntervalRef.current);
      }
      if (retryTimeoutRef.current !== null) {
        clearTimeout(retryTimeoutRef.current);
      }
    };
  }, []);

  /** Clear countdown interval. */
  const clearCountdown = useCallback(() => {
    if (countdownIntervalRef.current !== null) {
      clearInterval(countdownIntervalRef.current);
      countdownIntervalRef.current = null;
    }
  }, []);

  /** Clear retry timeout. */
  const clearRetryTimeout = useCallback(() => {
    if (retryTimeoutRef.current !== null) {
      clearTimeout(retryTimeoutRef.current);
      retryTimeoutRef.current = null;
    }
  }, []);

  /** Schedule a retry with exponential backoff and countdown. */
  const scheduleRetry = useCallback(
    (showCountdown: boolean) => {
      if (!mountedRef.current) return;

      clearCountdown();
      clearRetryTimeout();

      const delay = currentRetryDelayRef.current;

      // Only update countdown state when overlay is visible
      if (showCountdown) {
        setRetryCountdown(delay);

        // Countdown interval (tick every second)
        countdownIntervalRef.current = setInterval(() => {
          if (!mountedRef.current) {
            clearCountdown();
            return;
          }
          setRetryCountdown((prev) => {
            if (prev <= 1) {
              clearCountdown();
              return 0;
            }
            return prev - 1;
          });
        }, 1000);
      }

      // Schedule actual retry
      retryTimeoutRef.current = setTimeout(() => {
        if (!mountedRef.current) return;
        clearCountdown();
        // Increase backoff for next attempt
        currentRetryDelayRef.current = Math.min(
          currentRetryDelayRef.current * BACKOFF_MULTIPLIER,
          MAX_RETRY_DELAY,
        );
        checkHealth();
      }, delay * 1000);
    },
    // checkHealth is defined below but stable via refs — safe to reference
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [clearCountdown, clearRetryTimeout],
  );

  /** Handle daemon unavailable state with debounce and backoff. */
  const handleUnavailable = useCallback(
    (errorMessage: string) => {
      if (!mountedRef.current) return;

      setLastError(errorMessage);

      const now = Date.now();
      const isFirstCheck = !initialCheckDoneRef.current;
      initialCheckDoneRef.current = true;

      // Track when unavailability started
      if (unavailableSinceRef.current === null) {
        unavailableSinceRef.current = now;
      }

      const elapsed = now - unavailableSinceRef.current;

      // Apply debounce (skip for never-connected / first check)
      if (!isFirstCheck && elapsed < UNAVAILABLE_DEBOUNCE_MS) {
        // Not long enough — schedule retry without showing overlay or countdown
        scheduleRetry(false);
        return;
      }

      // Show overlay
      setIsDaemonAvailable(false);
      setConnectionMode(
        wasEverConnectedRef.current ? "reconnecting" : "never_connected",
      );

      scheduleRetry(true);
    },
    [scheduleRetry],
  );

  /** Perform a health check. */
  const checkHealth = useCallback(async () => {
    if (!mountedRef.current) return;
    setIsChecking(true);

    try {
      const response = await checkDaemonHealth();

      if (!mountedRef.current) return;

      if (response.status === "ok" || response.daemon.connected) {
        // Daemon is available
        setIsDaemonAvailable(true);
        wasEverConnectedRef.current = true;
        setWasEverConnected(true);
        setConnectionMode("connected");
        setLastError(null);
        setRetryCountdown(0);
        currentRetryDelayRef.current = INITIAL_RETRY_DELAY;
        unavailableSinceRef.current = null;
        clearCountdown();
        clearRetryTimeout();
        initialCheckDoneRef.current = true;
      } else {
        // Daemon responded but degraded/unhealthy
        handleUnavailable(response.daemon.error ?? "Daemon is degraded");
      }
    } catch (err) {
      if (!mountedRef.current) return;
      const message =
        err instanceof Error ? err.message : "Failed to reach daemon";
      handleUnavailable(message);
    } finally {
      if (mountedRef.current) {
        setIsChecking(false);
      }
    }
  }, [clearCountdown, clearRetryTimeout, handleUnavailable]);

  /** Immediate retry, resetting backoff. */
  const retryNow = useCallback(() => {
    currentRetryDelayRef.current = INITIAL_RETRY_DELAY;
    setRetryCountdown(0);
    clearCountdown();
    clearRetryTimeout();
    checkHealth();
  }, [clearCountdown, clearRetryTimeout, checkHealth]);

  // Initial health check on mount
  useEffect(() => {
    checkHealth();
  }, [checkHealth]);

  // Listen for daemon-unavailable events from fetchApi
  useEffect(() => {
    const handler = () => {
      // Only trigger if currently marked available — avoids redundant checks
      if (isDaemonAvailable && !isChecking) {
        checkHealth();
      }
    };
    window.addEventListener("daemon-unavailable", handler);
    return () => window.removeEventListener("daemon-unavailable", handler);
  }, [isDaemonAvailable, isChecking, checkHealth]);

  // Re-poll immediately when tab becomes visible
  useEffect(() => {
    const handler = () => {
      if (document.visibilityState === "visible" && !isDaemonAvailable) {
        retryNow();
      }
    };
    document.addEventListener("visibilitychange", handler);
    return () => document.removeEventListener("visibilitychange", handler);
  }, [isDaemonAvailable, retryNow]);

  return {
    isDaemonAvailable,
    isChecking,
    wasEverConnected,
    connectionMode,
    retryCountdown,
    lastError,
    retryNow,
  };
}
