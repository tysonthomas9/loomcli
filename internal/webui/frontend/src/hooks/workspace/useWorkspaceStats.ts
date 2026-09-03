/**
 * useWorkspaceStats - workspace-wide aggregate counts from
 * GET /api/workspaces/{ws}/stats.
 *
 * This is the authoritative source for "how many issues does this workspace
 * have". It is deliberately independent of whatever issue collection a view
 * has fetched: a board that is filtered, searched or paginated must not be
 * able to move these numbers.
 *
 * Fetches on mount and on workspace change, and refetches when the SSE
 * mutation stream reports a write in the same workspace. Counts change on
 * every write, so the refetch is debounced — a bulk close or an agent burst
 * must not fan out one request per mutation.
 */

import { useCallback, useEffect, useRef, useState } from "react";

import { getStats } from "@/api";
import { useDebouncedCallback, useEventSubscription } from "@/hooks/common";
import type { MutationPayload, Statistics } from "@/types";

/** Trailing debounce applied to SSE-triggered refetches. */
const REFETCH_DEBOUNCE_MS = 1000;

export interface UseWorkspaceStatsReturn {
  /** Workspace-wide counts, or null while loading or after a failure. */
  stats: Statistics | null;
  isLoading: boolean;
  error: string | null;
  /** Force an immediate refetch (not debounced). */
  refetch: () => void;
}

export function useWorkspaceStats(
  workspaceId: string,
): UseWorkspaceStatsReturn {
  const [stats, setStats] = useState<Statistics | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const mountedRef = useRef(true);
  const abortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      abortRef.current?.abort();
    };
  }, []);

  const fetchStats = useCallback((): void => {
    if (!workspaceId) return;

    // Only one request in flight per workspace; a newer one supersedes it.
    abortRef.current?.abort();
    const controller = new AbortController();
    abortRef.current = controller;

    setIsLoading(true);
    void getStats(workspaceId, { signal: controller.signal })
      .then((next) => {
        if (!mountedRef.current || controller.signal.aborted) return;
        setStats(next);
        setError(null);
      })
      .catch((err: unknown) => {
        if (!mountedRef.current || controller.signal.aborted) return;
        // Leave stats as-is rather than falling back to a page-derived count:
        // a silently-wrong number is worse than an empty card, and the next
        // SSE-triggered refetch recovers on its own.
        setError(err instanceof Error ? err.message : "Failed to load stats");
      })
      .finally(() => {
        if (!mountedRef.current || controller.signal.aborted) return;
        setIsLoading(false);
      });
  }, [workspaceId]);

  // Workspace switch: drop the previous workspace's numbers before the new
  // ones arrive, so they never render under the new workspace's name.
  useEffect(() => {
    abortRef.current?.abort();
    abortRef.current = null;
    setStats(null);
    setError(null);
    fetchStats();
  }, [fetchStats, workspaceId]);

  const debouncedRefetch = useDebouncedCallback(
    fetchStats,
    REFETCH_DEBOUNCE_MS,
  );

  useEventSubscription(
    useCallback(
      (mutation: MutationPayload) => {
        if (mutation.workspace_id !== workspaceId) return;
        debouncedRefetch();
      },
      [debouncedRefetch, workspaceId],
    ),
  );

  return { stats, isLoading, error, refetch: fetchStats };
}
