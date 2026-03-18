/**
 * useIssueDiffStat - React hook for polling issue diff statistics.
 * Provides branch name and line-level diff stats (+added -removed) for
 * an issue's assigned agent worktree.
 */

import { useState, useEffect, useCallback, useRef } from "react";

import { fetchIssueDiffStat } from "@/api";
import type { IssueDiffStat } from "@/api";

/** Options for the useIssueDiffStat hook. */
export interface UseIssueDiffStatOptions {
  /** Issue ID to fetch diff stats for. Null skips fetching. */
  issueId: string | null;
  /** Whether to fetch (default: true). */
  enabled?: boolean;
  /** Poll interval in ms (default: 30000). Set to 0 to disable polling. */
  pollInterval?: number;
}

/** Return type for the useIssueDiffStat hook. */
export interface UseIssueDiffStatReturn {
  /** Diff stat data, null if not yet loaded. */
  data: IssueDiffStat | null;
  /** Whether a fetch is in progress. */
  isLoading: boolean;
  /** Error from last fetch, null if successful. */
  error: Error | null;
  /** Manually trigger a refetch. */
  refetch: () => Promise<void>;
}

export function useIssueDiffStat(
  options: UseIssueDiffStatOptions,
): UseIssueDiffStatReturn {
  const { issueId, enabled = true, pollInterval = 30000 } = options;

  const [data, setData] = useState<IssueDiffStat | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  const mountedRef = useRef(true);
  const fetchInProgressRef = useRef(false);

  const fetchData = useCallback(async () => {
    if (fetchInProgressRef.current || !issueId) return;
    fetchInProgressRef.current = true;
    setIsLoading(true);

    try {
      const result = await fetchIssueDiffStat(issueId);
      if (mountedRef.current) {
        setData(result);
        setError(null);
      }
    } catch (err) {
      if (mountedRef.current) {
        setError(err instanceof Error ? err : new Error(String(err)));
      }
    } finally {
      if (mountedRef.current) {
        setIsLoading(false);
      }
      fetchInProgressRef.current = false;
    }
  }, [issueId]);

  const refetch = useCallback(async () => {
    await fetchData();
  }, [fetchData]);

  useEffect(() => {
    if (!enabled || !issueId) return;

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
  }, [enabled, issueId, pollInterval, fetchData]);

  return { data, isLoading, error, refetch };
}
