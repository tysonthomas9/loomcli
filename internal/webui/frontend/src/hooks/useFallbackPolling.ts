/**
 * Fallback polling hook for when SSE connection is unavailable.
 * Automatically activates polling after the connection has been
 * in 'reconnecting' state for a configurable threshold period.
 */

import { useState, useEffect, useRef, useCallback } from 'react';
import type { ConnectionState } from '../api/sse';

/**
 * Options for the useFallbackPolling hook.
 */
export interface UseFallbackPollingOptions {
  /** Current SSE connection state */
  wsState: ConnectionState;
  /** Callback invoked on each poll cycle */
  onPoll: () => void | Promise<void>;
  /** Time in 'reconnecting' state before activating polling (default: 30000ms) */
  activationThreshold?: number;
  /** Polling interval when active (default: 30000ms) */
  pollInterval?: number;
  /** Whether polling is enabled at all (default: true) */
  enabled?: boolean;
}

/**
 * Return type for the useFallbackPolling hook.
 */
export interface UseFallbackPollingReturn {
  /** Whether polling is currently active */
  isActive: boolean;
  /** Time until polling activates (ms), null if not in threshold countdown */
  timeUntilActive: number | null;
  /** Manually trigger a poll */
  pollNow: () => void;
  /** Force stop polling (until next activation) */
  stopPolling: () => void;
}

/**
 * React hook for fallback polling when SSE is unavailable.
 *
 * The hook monitors the connection state and automatically
 * activates polling when the connection has been in 'reconnecting' state
 * for longer than the activation threshold.
 *
 * @example
 * ```tsx
 * function MyComponent() {
 *   const sse = useSSE({ autoConnect: true })
 *   const polling = useFallbackPolling({
 *     wsState: sse.state,
 *     onPoll: () => fetchLatestIssues(),
 *   })
 *
 *   return (
 *     <div>
 *       {polling.isActive && <span>Using fallback polling...</span>}
 *     </div>
 *   )
 * }
 * ```
 */
export function useFallbackPolling(options: UseFallbackPollingOptions): UseFallbackPollingReturn {
  const {
    wsState,
    onPoll,
    activationThreshold = 30000,
    pollInterval = 30000,
    enabled = true,
  } = options;

  const [isActive, setIsActive] = useState(false);
  const [timeUntilActive, setTimeUntilActive] = useState<number | null>(null);

  // Refs for timers and callbacks
  const activationTimerRef = useRef<ReturnType<typeof setTimeout>>();
  const pollTimerRef = useRef<ReturnType<typeof setInterval>>();
  const countdownTimerRef = useRef<ReturnType<typeof setInterval>>();
  const onPollRef = useRef(onPoll);
  const mountedRef = useRef(true);
  const pollingInFlightRef = useRef(false);

  // Track activation start time for countdown calculation
  const activationStartRef = useRef<number | null>(null);

  // Update callback ref when it changes
  useEffect(() => {
    onPollRef.current = onPoll;
  }, [onPoll]);

  // Helper to clear all timers
  const clearTimers = useCallback(() => {
    if (activationTimerRef.current !== undefined) {
      clearTimeout(activationTimerRef.current);
      activationTimerRef.current = undefined;
    }
    if (pollTimerRef.current !== undefined) {
      clearInterval(pollTimerRef.current);
      pollTimerRef.current = undefined;
    }
    if (countdownTimerRef.current !== undefined) {
      clearInterval(countdownTimerRef.current);
      countdownTimerRef.current = undefined;
    }
    activationStartRef.current = null;
  }, []);

  // Execute a poll with error handling and in-flight tracking
  const executePoll = useCallback(async () => {
    // Prevent overlapping polls
    if (pollingInFlightRef.current) return;
    if (!mountedRef.current) return;

    pollingInFlightRef.current = true;
    try {
      await onPollRef.current();
    } catch (error) {
      // Log error but continue polling
      console.error('[useFallbackPolling] Poll error:', error);
    } finally {
      pollingInFlightRef.current = false;
    }
  }, []);

  // Monitor wsState and manage activation
  useEffect(() => {
    if (!enabled) {
      clearTimers();
      setIsActive(false);
      setTimeUntilActive(null);
      return;
    }

    if (wsState === 'connected') {
      // SSE recovered - stop polling
      clearTimers();
      setIsActive(false);
      setTimeUntilActive(null);
    } else if (wsState === 'reconnecting' && !isActive) {
      // Start countdown to activation
      activationStartRef.current = Date.now();
      setTimeUntilActive(activationThreshold);

      // Update countdown every second
      countdownTimerRef.current = setInterval(() => {
        if (!mountedRef.current || activationStartRef.current === null) return;
        const elapsed = Date.now() - activationStartRef.current;
        const remaining = Math.max(0, activationThreshold - elapsed);
        setTimeUntilActive(remaining);
      }, 1000);

      // Set activation timer
      activationTimerRef.current = setTimeout(() => {
        if (!mountedRef.current) return;
        // Clear countdown timer before activating
        if (countdownTimerRef.current !== undefined) {
          clearInterval(countdownTimerRef.current);
          countdownTimerRef.current = undefined;
        }
        setIsActive(true);
        setTimeUntilActive(null);
      }, activationThreshold);
    } else if (wsState === 'disconnected') {
      // Manual disconnect - stop polling
      clearTimers();
      setIsActive(false);
      setTimeUntilActive(null);
    }
  }, [wsState, enabled, activationThreshold, isActive, clearTimers]);

  // Manage polling interval when active
  useEffect(() => {
    if (!isActive) return;

    // Poll immediately on activation
    executePoll();

    // Then poll at interval
    pollTimerRef.current = setInterval(() => {
      if (!mountedRef.current) return;
      executePoll();
    }, pollInterval);

    return () => {
      if (pollTimerRef.current !== undefined) {
        clearInterval(pollTimerRef.current);
        pollTimerRef.current = undefined;
      }
    };
  }, [isActive, pollInterval, executePoll]);

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      mountedRef.current = false;
      clearTimers();
    };
  }, [clearTimers]);

  const pollNow = useCallback(() => {
    if (isActive) {
      executePoll();
    }
  }, [isActive, executePoll]);

  const stopPolling = useCallback(() => {
    clearTimers();
    setIsActive(false);
    setTimeUntilActive(null);
  }, [clearTimers]);

  return {
    isActive,
    timeUntilActive,
    pollNow,
    stopPolling,
  };
}
