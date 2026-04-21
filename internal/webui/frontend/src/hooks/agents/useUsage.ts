/**
 * useUsage - React hook for fetching usage/cost data from the loom server.
 * Provides on-demand and polling-based access to token usage summaries.
 */

import { useState, useEffect, useCallback, useRef } from "react";

import { fetchUsage } from "@/api";
import { useWorkspaceContext } from "@/hooks/workspace";
import type { UsageResponse, UsageParams } from "@/types";

/** Options for the useUsage hook. */
export interface UseUsageOptions {
  /** Poll interval in ms (default: 30000). Set to 0 to disable polling. */
  pollInterval?: number;
  /** Filter parameters for the usage query. */
  params?: UsageParams;
  /** Whether to fetch (default: true). */
  enabled?: boolean;
}

/** Return type for the useUsage hook. */
export interface UseUsageResult {
  /** Usage data, null if not yet loaded. */
  data: UsageResponse | null;
  /** Whether a fetch is in progress. */
  isLoading: boolean;
  /** Whether connected to the loom server. */
  isConnected: boolean;
  /** Error from last fetch, null if successful. */
  error: Error | null;
  /** Last successful update time. */
  lastUpdated: Date | null;
  /** Manually trigger a refetch. */
  refetch: () => Promise<void>;
}

export function useUsage(options?: UseUsageOptions): UseUsageResult {
  const { pollInterval = 30000, params, enabled = true } = options ?? {};
  const { workspaceId } = useWorkspaceContext();

  const [data, setData] = useState<UsageResponse | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [isConnected, setIsConnected] = useState(true);
  const [error, setError] = useState<Error | null>(null);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);

  const mountedRef = useRef(true);
  const fetchInProgressRef = useRef(false);

  // Serialize params for dependency tracking
  const paramsKey = JSON.stringify(params ?? {});

  const fetchData = useCallback(async () => {
    if (fetchInProgressRef.current) return;
    fetchInProgressRef.current = true;
    setIsLoading(true);

    try {
      const result = await fetchUsage(workspaceId, params);
      if (mountedRef.current) {
        setData(result);
        setError(null);
        setIsConnected(true);
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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [paramsKey, workspaceId]);

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

    return () => {
      mountedRef.current = false;
      if (intervalId) clearInterval(intervalId);
    };
  }, [enabled, pollInterval, fetchData]);

  return { data, isLoading, isConnected, error, lastUpdated, refetch };
}
