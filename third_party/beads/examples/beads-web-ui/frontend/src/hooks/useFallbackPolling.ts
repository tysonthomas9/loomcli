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
  /** How long 'connected' state must persist before fully resetting degraded tracking (default: 5000ms) */
  recoveryGracePeriod?: number;
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
    recoveryGracePeriod = 5000,
  } = options;

  const [isActive, setIsActive] = useState(false);
  const [timeUntilActive, setTimeUntilActive] = useState<number | null>(null);

  // Refs for timers and callbacks
  const activationTimerRef = useRef<ReturnType<typeof setTimeout>>();
  const pollTimerRef = useRef<ReturnType<typeof setInterval>>();
  const countdownTimerRef = useRef<ReturnType<typeof setInterval>>();
  const recoveryTimerRef = useRef<ReturnType<typeof setTimeout>>();
  const onPollRef = useRef(onPoll);
  const mountedRef = useRef(true);
  const pollingInFlightRef = useRef(false);

  // Track activation start time for countdown calculation
  const activationStartRef = useRef<number | null>(null);
  // Track the first degraded-episode timestamp for accumulated degraded time
  const firstDegradedRef = useRef<number | null>(null);
  // Track whether polling was manually stopped (prevents re-activation while still reconnecting)
  const manuallyStopped = useRef(false);

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
    if (recoveryTimerRef.current !== undefined) {
      clearTimeout(recoveryTimerRef.current);
      recoveryTimerRef.current = undefined;
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
      firstDegradedRef.current = null;
      setIsActive(false);
      setTimeUntilActive(null);
      return;
    }

    if (wsState === 'connected') {
      // Reset manual stop flag — state change clears the stop
      manuallyStopped.current = false;
      // Cancel any pending activation/countdown timers but NOT the poll timer
      // (polling continues during grace period if already active)
      if (activationTimerRef.current !== undefined) {
        clearTimeout(activationTimerRef.current);
        activationTimerRef.current = undefined;
      }
      if (countdownTimerRef.current !== undefined) {
        clearInterval(countdownTimerRef.current);
        countdownTimerRef.current = undefined;
      }
      activationStartRef.current = null;
      setTimeUntilActive(null);

      // Start recovery grace timer — only fully reset after sustained connected state
      if (recoveryTimerRef.current !== undefined) {
        clearTimeout(recoveryTimerRef.current);
      }
      recoveryTimerRef.current = setTimeout(() => {
        if (!mountedRef.current) return;
        // Sustained recovery — fully reset degraded tracking
        firstDegradedRef.current = null;
        setIsActive(false);
        if (pollTimerRef.current !== undefined) {
          clearInterval(pollTimerRef.current);
          pollTimerRef.current = undefined;
        }
        recoveryTimerRef.current = undefined;
      }, recoveryGracePeriod);
    } else if (wsState === 'reconnecting') {
      // Cancel any pending recovery timer — connection dropped again
      if (recoveryTimerRef.current !== undefined) {
        clearTimeout(recoveryTimerRef.current);
        recoveryTimerRef.current = undefined;
      }

      // If already active or manually stopped, just keep current state (no-op)
      if (isActive || manuallyStopped.current) return;

      // Record the start of the degradation window if not already set
      if (firstDegradedRef.current === null) {
        firstDegradedRef.current = Date.now();
      }

      // Calculate remaining time based on total accumulated degraded time
      const totalDegraded = Date.now() - firstDegradedRef.current;
      const remaining = Math.max(0, activationThreshold - totalDegraded);

      if (remaining <= 0) {
        // Already past threshold — activate immediately
        if (countdownTimerRef.current !== undefined) {
          clearInterval(countdownTimerRef.current);
          countdownTimerRef.current = undefined;
        }
        setIsActive(true);
        setTimeUntilActive(null);
      } else {
        // Start countdown for remaining time
        activationStartRef.current = Date.now();
        setTimeUntilActive(remaining);

        // Clear any existing timers before setting new ones
        if (countdownTimerRef.current !== undefined) {
          clearInterval(countdownTimerRef.current);
        }
        if (activationTimerRef.current !== undefined) {
          clearTimeout(activationTimerRef.current);
        }

        // Update countdown every second
        countdownTimerRef.current = setInterval(() => {
          if (!mountedRef.current || firstDegradedRef.current === null) return;
          const elapsed = Date.now() - firstDegradedRef.current;
          const countdownRemaining = Math.max(0, activationThreshold - elapsed);
          setTimeUntilActive(countdownRemaining);
        }, 1000);

        // Set activation timer for remaining time
        activationTimerRef.current = setTimeout(() => {
          if (!mountedRef.current) return;
          if (countdownTimerRef.current !== undefined) {
            clearInterval(countdownTimerRef.current);
            countdownTimerRef.current = undefined;
          }
          setIsActive(true);
          setTimeUntilActive(null);
        }, remaining);
      }
    } else if (wsState === 'disconnected') {
      // Manual disconnect - stop everything
      manuallyStopped.current = false;
      clearTimers();
      firstDegradedRef.current = null;
      setIsActive(false);
      setTimeUntilActive(null);
    }
  }, [wsState, enabled, activationThreshold, recoveryGracePeriod, isActive, clearTimers]);

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
    firstDegradedRef.current = null;
    manuallyStopped.current = true;
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
