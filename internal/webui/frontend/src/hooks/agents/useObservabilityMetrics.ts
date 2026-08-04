/**
 * useObservabilityMetrics - React hook for fetching observability metrics.
 * Polls GET /api/observability/metrics at a configurable interval (default 30s).
 */

import { useState, useEffect, useCallback, useRef } from "react";

import { fetchObservabilityMetrics } from "@/api";
import type { MetricsSnapshot } from "@/types";

export interface UseObservabilityMetricsOptions {
  /** Poll interval in ms (default: 30000) */
  pollInterval?: number;
  /** Whether to fetch (default: true) */
  enabled?: boolean;
}

export interface UseObservabilityMetricsResult {
  metrics: MetricsSnapshot | null;
  isLoading: boolean;
  error: Error | null;
  isConnected: boolean;
  lastUpdated: Date | null;
  refetch: () => Promise<void>;
}

export function useObservabilityMetrics(
  options?: UseObservabilityMetricsOptions,
): UseObservabilityMetricsResult {
  const { pollInterval = 30000, enabled = true } = options ?? {};

  const [metrics, setMetrics] = useState<MetricsSnapshot | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const [isConnected, setIsConnected] = useState(false);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);

  const fetchInProgressRef = useRef(false);
  const mountedRef = useRef(true);

  const fetchData = useCallback(async () => {
    if (fetchInProgressRef.current) return;

    fetchInProgressRef.current = true;
    setIsLoading(true);

    try {
      const result = await fetchObservabilityMetrics();

      if (mountedRef.current) {
        setMetrics(result);
        setIsConnected(true);
        setError(null);
        setLastUpdated(new Date());
      }
    } catch (err) {
      if (mountedRef.current) {
        setError(err instanceof Error ? err : new Error(String(err)));
        setIsConnected(false);
      }
    } finally {
      if (mountedRef.current) {
        setIsLoading(false);
      }
      fetchInProgressRef.current = false;
    }
  }, []);

  const refetch = useCallback(async () => {
    await fetchData();
  }, [fetchData]);

  useEffect(() => {
    mountedRef.current = true;

    if (!enabled) return;

    void fetchData();

    let intervalId: ReturnType<typeof setInterval> | null = null;
    if (pollInterval > 0) {
      intervalId = setInterval(() => {
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
      if (intervalId) clearInterval(intervalId);
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, [enabled, pollInterval, fetchData]);

  return { metrics, isLoading, error, isConnected, lastUpdated, refetch };
}
