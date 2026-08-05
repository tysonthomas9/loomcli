/**
 * useWorkspaceHealth - React hook for workspace service availability monitoring.
 * Polls GET /api/health with exponential backoff and provides workspace service
 * availability state to the app. Uses the shared usePollingWithBackoff
 * hook for retry scheduling.
 */

import { useState, useCallback, useEffect, useRef } from "react";

import { onWorkspaceUnavailable } from "@/api/common";
import { checkWorkspaceHealth } from "@/api/common";

import { usePollingWithBackoff } from "@/hooks/common";

// ============= Constants =============

/**
 * Debounce before showing overlay (ms). Workspace service must be unreachable for
 * this long before the overlay appears. Prevents flash on brief blips.
 * Not applied in never-connected state (show immediately).
 */
const UNAVAILABLE_DEBOUNCE_MS = 2000;

// ============= Types =============

/** Connection mode distinguishing first-time vs reconnection scenarios. */
export type WorkspaceConnectionMode =
  | "never_connected"
  | "connected"
  | "lost_connection"
  | "reconnecting"
  | "starting";

/** Return type for the useWorkspaceHealth hook. */
export interface UseWorkspaceHealthReturn {
  /** Whether the workspace service is currently available. */
  isWorkspaceAvailable: boolean;
  /** Whether a health check is in progress. */
  isChecking: boolean;
  /** Whether the workspace service was ever successfully connected. */
  wasEverConnected: boolean;
  /** Current connection mode for overlay messaging. */
  connectionMode: WorkspaceConnectionMode;
  /** Seconds until next retry (0 if not waiting). */
  retryCountdown: number;
  /** Last error message from health check, if any. */
  lastError: string | null;
  /** Trigger an immediate retry, resetting backoff. */
  retryNow: () => void;
}

// ============= Hook =============

export function useWorkspaceHealth(): UseWorkspaceHealthReturn {
  const [isWorkspaceAvailable, setIsWorkspaceAvailable] = useState(true);
  const [isChecking, setIsChecking] = useState(false);
  const [connectionMode, setConnectionMode] =
    useState<WorkspaceConnectionMode>("never_connected");
  const [lastError, setLastError] = useState<string | null>(null);

  const mountedRef = useRef(true);
  /** Timestamp (ms) when workspace service first became unavailable (pre-debounce). */
  const unavailableSinceRef = useRef<number | null>(null);
  /** Whether the initial health check has completed. */
  const initialCheckDoneRef = useRef(false);
  /** Ref to avoid stale closure for wasEverConnected. */
  const wasEverConnectedRef = useRef(false);

  // Backoff hook — handles retry scheduling and countdown.
  // onRetry references checkHealth (defined below) via closure.
  // This is safe because onRetry is stored in a ref inside the shared hook
  // and is never called synchronously during render.
  const backoff = usePollingWithBackoff({
    onRetry: () => {
      void checkHealth();
    },
    staleBannerDelayMs: 0, // Workspace service uses its own overlay, not stale banner
    maxFailuresAtCeiling: 0, // Disable — workspace service doesn't track connectionLost
    repollOnVisibilityChange: false, // Workspace service handles visibility itself
  });

  // Keep wasEverConnectedRef in sync with backoff state
  wasEverConnectedRef.current = backoff.wasEverConnected;

  // Store backoff functions in refs to avoid unstable dependency chains
  const reportSuccessRef = useRef(backoff.reportSuccess);
  const reportFailureRef = useRef(backoff.reportFailure);
  reportSuccessRef.current = backoff.reportSuccess;
  reportFailureRef.current = backoff.reportFailure;

  /** Handle workspace service unavailable state with debounce and backoff. */
  const handleUnavailable = useCallback((errorMessage: string) => {
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
      // Not long enough — schedule retry without showing overlay
      reportFailureRef.current({ forceRetry: true });
      return;
    }

    // Show overlay
    setIsWorkspaceAvailable(false);
    setConnectionMode(
      wasEverConnectedRef.current ? "reconnecting" : "never_connected",
    );

    reportFailureRef.current({ forceRetry: true });
  }, []);

  /** Perform a health check. */
  const checkHealth = useCallback(async () => {
    if (!mountedRef.current) return;
    setIsChecking(true);

    try {
      const response = await checkWorkspaceHealth();

      if (!mountedRef.current) return;

      if (response.status === "ok") {
        // Workspace service is available
        setIsWorkspaceAvailable(true);
        wasEverConnectedRef.current = true;
        setConnectionMode("connected");
        setLastError(null);
        unavailableSinceRef.current = null;
        initialCheckDoneRef.current = true;
        reportSuccessRef.current();
      } else if (response.status === "starting") {
        // Workspace service is starting up (hydrating) — show loading state, not error
        setIsWorkspaceAvailable(false);
        setConnectionMode("starting");
        setLastError(response.error ?? "Workspace service is starting up");
        initialCheckDoneRef.current = true;
        reportFailureRef.current({ forceRetry: true });
      } else {
        // Workspace service responded but degraded/unhealthy
        handleUnavailable(response.error ?? "Workspace service is degraded");
      }
    } catch (err) {
      if (!mountedRef.current) return;
      const message =
        err instanceof Error
          ? err.message
          : "Failed to reach workspace service";
      handleUnavailable(message);
    } finally {
      if (mountedRef.current) {
        setIsChecking(false);
      }
    }
  }, [handleUnavailable]);

  /** Immediate retry, resetting backoff. */
  const retryNow = useCallback(() => {
    backoff.retryNow();
  }, [backoff.retryNow]);

  // Cleanup on unmount
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  // Initial health check on mount
  useEffect(() => {
    checkHealth();
  }, [checkHealth]);

  // Listen for workspace service-unavailable notifications from fetchApi
  useEffect(() => {
    const handler = () => {
      // Only trigger if currently marked available — avoids redundant checks
      if (isWorkspaceAvailable && !isChecking) {
        checkHealth();
      }
    };
    return onWorkspaceUnavailable(handler);
  }, [isWorkspaceAvailable, isChecking, checkHealth]);

  // Re-poll immediately when tab becomes visible
  useEffect(() => {
    const handler = () => {
      if (document.visibilityState === "visible" && !isWorkspaceAvailable) {
        retryNow();
      }
    };
    document.addEventListener("visibilitychange", handler);
    return () => document.removeEventListener("visibilitychange", handler);
  }, [isWorkspaceAvailable, retryNow]);

  return {
    isWorkspaceAvailable,
    isChecking,
    wasEverConnected: backoff.wasEverConnected,
    connectionMode,
    retryCountdown: backoff.retryCountdown,
    lastError,
    retryNow,
  };
}
