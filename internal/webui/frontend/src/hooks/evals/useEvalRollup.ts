import { useCallback, useEffect, useRef, useState } from "react";

import {
  buildEvalRollupWindow,
  fetchEvalRollup,
  type EvalRollupWindowDays,
} from "@/api/evals";
import type { EvalRollupData } from "@/types";
import { useWorkspaceContext } from "@/hooks/workspace";

export interface UseEvalRollupOptions {
  pollInterval?: number;
  enabled?: boolean;
}

export interface UseEvalRollupResult {
  rollup: EvalRollupData | null;
  isLoading: boolean;
  error: Error | null;
  lastUpdated: Date | null;
  refetch: () => Promise<void>;
}

export function useEvalRollup(
  windowDays: EvalRollupWindowDays,
  options?: UseEvalRollupOptions,
): UseEvalRollupResult {
  const { pollInterval = 60000, enabled = true } = options ?? {};
  const { workspaceId } = useWorkspaceContext();
  const [rollup, setRollup] = useState<EvalRollupData | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const [lastUpdated, setLastUpdated] = useState<Date | null>(null);
  const fetchInProgressRef = useRef(false);
  const mountedRef = useRef(true);

  const fetchData = useCallback(async () => {
    if (!enabled || fetchInProgressRef.current) return;
    fetchInProgressRef.current = true;
    setIsLoading(true);
    try {
      const window = buildEvalRollupWindow(windowDays);
      const data = await fetchEvalRollup(workspaceId, window);
      if (!mountedRef.current) return;
      setRollup(data);
      setError(null);
      setLastUpdated(new Date());
    } catch (err) {
      if (mountedRef.current) {
        setError(err instanceof Error ? err : new Error(String(err)));
      }
    } finally {
      if (mountedRef.current) setIsLoading(false);
      fetchInProgressRef.current = false;
    }
  }, [enabled, windowDays, workspaceId]);

  useEffect(() => {
    mountedRef.current = true;
    if (!enabled) {
      setIsLoading(false);
      return;
    }

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
  }, [enabled, fetchData, pollInterval]);

  const refetch = useCallback(async () => {
    fetchInProgressRef.current = false;
    await fetchData();
  }, [fetchData]);

  return { rollup, isLoading, error, lastUpdated, refetch };
}
