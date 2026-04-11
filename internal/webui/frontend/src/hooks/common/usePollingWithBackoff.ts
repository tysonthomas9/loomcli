/**
 * usePollingWithBackoff - Shared hook for retry scheduling with exponential backoff.
 * Manages retry timers, countdown state, and stale-banner state.
 * Consumers control the actual fetch logic and report success/failure.
 */

import { useState, useCallback, useEffect, useRef } from "react";

/** Options for the usePollingWithBackoff hook. */
export interface UsePollingWithBackoffOptions {
  /** Called when a retry fires. Consumer should call reportSuccess/reportFailure when done. */
  onRetry: () => void;
  /** Whether the hook is enabled (default: true) */
  enabled?: boolean;
  /** Initial retry delay in seconds (default: 5) */
  initialDelay?: number;
  /** Max retry delay in seconds (default: 60) */
  maxDelay?: number;
  /** Backoff multiplier (default: 2) */
  multiplier?: number;
  /** Delay before showing stale banner in ms (default: 5000). Set to 0 to disable. */
  staleBannerDelayMs?: number;
  /** Max consecutive failures at ceiling before declaring connection lost (default: 5). Set to 0 to disable. */
  maxFailuresAtCeiling?: number;
  /** Whether to re-poll on visibility change (default: true) */
  repollOnVisibilityChange?: boolean;
}

/** Result returned by the usePollingWithBackoff hook. */
export interface UsePollingWithBackoffResult {
  /** Call when a fetch/check succeeds — resets backoff, clears stale state */
  reportSuccess: () => void;
  /** Call when a fetch/check fails — starts/continues backoff retry cycle.
   *  Only schedules retry if wasEverConnected is true (or forceRetry option is passed). */
  reportFailure: (opts?: { forceRetry?: boolean }) => void;
  /** Seconds until next retry (0 if not waiting) */
  retryCountdown: number;
  /** True when disconnected longer than staleBannerDelayMs — data may be stale */
  showStaleBanner: boolean;
  /** True when reconnection failed after maxFailuresAtCeiling consecutive failures at max delay */
  connectionLost: boolean;
  /** Timestamp (ms) when disconnection started, null if connected */
  disconnectedSince: number | null;
  /** Whether we've ever had a successful report */
  wasEverConnected: boolean;
  /** Retry immediately, resetting backoff and failure counters */
  retryNow: () => void;
}

export function usePollingWithBackoff(
  options: UsePollingWithBackoffOptions,
): UsePollingWithBackoffResult {
  const {
    enabled = true,
    initialDelay = 5,
    maxDelay = 60,
    multiplier = 2,
    staleBannerDelayMs = 5000,
    maxFailuresAtCeiling = 5,
    repollOnVisibilityChange = true,
  } = options;

  // State
  const [retryCountdown, setRetryCountdown] = useState(0);
  const [showStaleBanner, setShowStaleBanner] = useState(false);
  const [connectionLost, setConnectionLost] = useState(false);
  const [disconnectedSince, setDisconnectedSince] = useState<number | null>(
    null,
  );
  const [wasEverConnected, setWasEverConnected] = useState(false);

  // Refs
  const mountedRef = useRef(true);
  const onRetryRef = useRef(options.onRetry);
  const currentRetryDelayRef = useRef(initialDelay);
  const retryTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const retryIntervalRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const staleBannerTimerRef = useRef<ReturnType<typeof setTimeout> | null>(
    null,
  );
  const consecutiveFailuresAtCeilingRef = useRef(0);
  const disconnectedSinceRef = useRef<number | null>(null);
  const wasEverConnectedRef = useRef(false);

  // Keep onRetry ref current to avoid stale closures
  onRetryRef.current = options.onRetry;

  // Keep wasEverConnectedRef in sync
  wasEverConnectedRef.current = wasEverConnected;

  // Clear retry timers (used by reportSuccess, retryNow)
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

  // Schedule a retry with exponential backoff and countdown.
  // Clears any existing timers before scheduling new ones.
  const scheduleRetry = useCallback(() => {
    if (!mountedRef.current || !enabled) return;

    // Clear any existing timers before scheduling new ones
    if (retryTimeoutRef.current) {
      clearTimeout(retryTimeoutRef.current);
    }
    if (retryIntervalRef.current) {
      clearInterval(retryIntervalRef.current);
    }

    const delay = currentRetryDelayRef.current;
    setRetryCountdown(delay);

    // Countdown interval (tick every second)
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

    // Schedule actual retry
    const timeoutId = setTimeout(() => {
      if (!mountedRef.current) return;
      clearRetryTimers();
      // Increase backoff for next attempt
      currentRetryDelayRef.current = Math.min(
        currentRetryDelayRef.current * multiplier,
        maxDelay,
      );
      onRetryRef.current();
    }, delay * 1000);

    // Set refs immediately (same sync block) to prevent race conditions
    retryIntervalRef.current = intervalId;
    retryTimeoutRef.current = timeoutId;
  }, [enabled, multiplier, maxDelay, clearRetryTimers]);

  // Report a successful fetch/check
  const reportSuccess = useCallback(() => {
    if (!mountedRef.current) return;

    clearRetryTimers();
    currentRetryDelayRef.current = initialDelay;
    consecutiveFailuresAtCeilingRef.current = 0;

    // Clear stale banner timer
    if (staleBannerTimerRef.current) {
      clearTimeout(staleBannerTimerRef.current);
      staleBannerTimerRef.current = null;
    }

    setShowStaleBanner(false);
    setConnectionLost(false);
    disconnectedSinceRef.current = null;
    setDisconnectedSince(null);

    wasEverConnectedRef.current = true;
    setWasEverConnected(true);
  }, [clearRetryTimers, initialDelay]);

  // Report a failed fetch/check
  const reportFailure = useCallback(
    (opts?: { forceRetry?: boolean }) => {
      if (!mountedRef.current) return;

      const shouldRetry =
        wasEverConnectedRef.current || (opts?.forceRetry ?? false);

      // Start stale banner timer on first disconnect (only if staleBannerDelayMs > 0)
      if (
        staleBannerDelayMs > 0 &&
        wasEverConnectedRef.current &&
        disconnectedSinceRef.current === null &&
        !staleBannerTimerRef.current
      ) {
        const now = Date.now();
        disconnectedSinceRef.current = now;
        setDisconnectedSince(now);
        staleBannerTimerRef.current = setTimeout(() => {
          if (mountedRef.current) {
            setShowStaleBanner(true);
          }
        }, staleBannerDelayMs);
      }

      // Track consecutive failures at max delay
      if (
        maxFailuresAtCeiling > 0 &&
        currentRetryDelayRef.current >= maxDelay
      ) {
        consecutiveFailuresAtCeilingRef.current += 1;
        if (
          consecutiveFailuresAtCeilingRef.current >= maxFailuresAtCeiling &&
          mountedRef.current
        ) {
          setConnectionLost(true);
        }
      }

      // Schedule retry if appropriate
      if (shouldRetry) {
        scheduleRetry();
      }
    },
    [staleBannerDelayMs, maxFailuresAtCeiling, maxDelay, scheduleRetry],
  );

  // Retry immediately, resetting backoff
  const retryNow = useCallback(() => {
    clearRetryTimers();
    currentRetryDelayRef.current = initialDelay;
    consecutiveFailuresAtCeilingRef.current = 0;
    setConnectionLost(false);
    onRetryRef.current();
  }, [clearRetryTimers, initialDelay]);

  // Visibility change handler
  useEffect(() => {
    if (!repollOnVisibilityChange || !enabled) return;

    const handler = () => {
      if (
        document.visibilityState === "visible" &&
        disconnectedSinceRef.current !== null
      ) {
        retryNow();
      }
    };
    document.addEventListener("visibilitychange", handler);
    return () => document.removeEventListener("visibilitychange", handler);
  }, [repollOnVisibilityChange, enabled, retryNow]);

  // Cleanup on unmount
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
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
  }, []);

  return {
    reportSuccess,
    reportFailure,
    retryCountdown,
    showStaleBanner,
    connectionLost,
    disconnectedSince,
    wasEverConnected,
    retryNow,
  };
}
