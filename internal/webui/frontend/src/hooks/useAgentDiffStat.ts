/**
 * useAgentDiffStat - React hook for polling agent diff statistics.
 * Provides branch name and line-level diff stats (+added -removed) for
 * an agent's worktree, resolved directly by agent name.
 */

import { useState, useEffect, useCallback, useRef } from "react";

import { fetchAgentDiffStat } from "@/api";
import type { IssueDiffStat } from "@/api";

import { useWorkspaceContext } from "./useWorkspaceContext";

/** Options for the useAgentDiffStat hook. */
export interface UseAgentDiffStatOptions {
  /** Agent name to fetch diff stats for. Empty string skips fetching. */
  agentName: string;
  /** Whether to fetch (default: true). */
  enabled?: boolean;
  /** Poll interval in ms (default: 60000). Set to 0 to disable polling. */
  pollInterval?: number;
}

/** Return type for the useAgentDiffStat hook. */
export interface UseAgentDiffStatReturn {
  /** Diff stat data, null if not yet loaded. */
  data: IssueDiffStat | null;
  /** Whether a fetch is in progress. */
  isLoading: boolean;
  /** Error from last fetch, null if successful. */
  error: Error | null;
  /** Manually trigger a refetch. */
  refetch: () => Promise<void>;
}

export function useAgentDiffStat(
  options: UseAgentDiffStatOptions,
): UseAgentDiffStatReturn {
  const { workspaceId } = useWorkspaceContext();
  const { agentName, enabled = true, pollInterval = 60000 } = options;

  const [data, setData] = useState<IssueDiffStat | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  const mountedRef = useRef(true);
  const fetchInProgressRef = useRef(false);

  const fetchData = useCallback(async () => {
    if (fetchInProgressRef.current || !agentName) return;
    fetchInProgressRef.current = true;
    setIsLoading(true);

    try {
      const result = await fetchAgentDiffStat(workspaceId, agentName);
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
  }, [workspaceId, agentName]);

  const refetch = useCallback(async () => {
    await fetchData();
  }, [fetchData]);

  useEffect(() => {
    if (!enabled || !agentName) return;

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
  }, [enabled, agentName, pollInterval, fetchData]);

  return { data, isLoading, error, refetch };
}
