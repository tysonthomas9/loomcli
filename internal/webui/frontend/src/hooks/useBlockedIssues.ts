/**
 * useBlockedIssues - React hook for fetching and managing blocked issues.
 * Provides issues that have blocking dependencies (waiting on other issues to complete).
 */

import { useState, useEffect, useCallback, useRef } from 'react';
import { getBlockedIssues, type BlockedFilter } from '@/api/issues';
import type { BlockedIssue } from '@/types';

/**
 * Options for the useBlockedIssues hook.
 */
export interface UseBlockedIssuesOptions {
  /** Optional: filter to descendants of this issue/epic */
  parentId?: string;
  /** Optional: filter by priority (0-4) */
  priority?: number;
  /** Optional: filter by issue type */
  type?: string;
  /** Optional: filter by assignee */
  assignee?: string;
  /** Optional: max results to return */
  limit?: number;
  /** Optional: poll interval in ms (default: no polling) */
  pollInterval?: number;
  /** Optional: whether to fetch (default: true) */
  enabled?: boolean;
}

/**
 * Return type for the useBlockedIssues hook.
 */
export interface UseBlockedIssuesResult {
  /** Blocked issues data, null if not yet loaded */
  data: BlockedIssue[] | null;
  /** Whether a fetch is currently in progress */
  loading: boolean;
  /** Error from the last fetch attempt, null if successful */
  error: Error | null;
  /** Function to manually trigger a refetch */
  refetch: () => Promise<void>;
}

/**
 * React hook for fetching issues that have blocking dependencies.
 *
 * @param options - Configuration options for the hook
 * @returns Object with data, loading, error states and refetch function
 *
 * @example
 * ```tsx
 * function DependencyGraph() {
 *   const { data, loading, error, refetch } = useBlockedIssues({
 *     pollInterval: 30000, // Poll every 30 seconds
 *   })
 *
 *   if (loading && !data) return <Loading />
 *   if (error) return <Error message={error.message} />
 *
 *   return (
 *     <Graph
 *       blockedIssues={data ?? []}
 *       onRefresh={refetch}
 *     />
 *   )
 * }
 * ```
 */
export function useBlockedIssues(options?: UseBlockedIssuesOptions): UseBlockedIssuesResult {
  const { parentId, priority, type, assignee, limit, pollInterval, enabled = true } = options ?? {};

  const [data, setData] = useState<BlockedIssue[] | null>(null);
  const [loading, setLoading] = useState<boolean>(false);
  const [error, setError] = useState<Error | null>(null);

  // Track if a fetch is in progress to prevent overlapping requests
  const fetchInProgressRef = useRef<boolean>(false);

  // Track if the component is mounted for cleanup
  const mountedRef = useRef<boolean>(true);

  // Stable fetch function using useCallback
  const fetchData = useCallback(async () => {
    // Skip if already fetching (prevents stacking poll requests)
    if (fetchInProgressRef.current) {
      return;
    }

    fetchInProgressRef.current = true;
    setLoading(true);

    try {
      const filter: BlockedFilter = {};
      if (parentId) {
        filter.parent_id = parentId;
      }
      if (priority !== undefined) {
        filter.priority = priority;
      }
      if (type) {
        filter.type = type;
      }
      if (assignee) {
        filter.assignee = assignee;
      }
      if (limit !== undefined) {
        filter.limit = limit;
      }

      const result = await getBlockedIssues(filter);

      // Only update state if still mounted
      if (mountedRef.current) {
        setData(result);
        setError(null);
      }
    } catch (err) {
      // Only update state if still mounted
      if (mountedRef.current) {
        setError(err instanceof Error ? err : new Error(String(err)));
        // Keep stale data on error
      }
    } finally {
      if (mountedRef.current) {
        setLoading(false);
      }
      fetchInProgressRef.current = false;
    }
  }, [parentId, priority, type, assignee, limit]);

  // Refetch function exposed to consumers
  const refetch = useCallback(async () => {
    await fetchData();
  }, [fetchData]);

  // Initial fetch and polling setup
  useEffect(() => {
    mountedRef.current = true;

    // Don't fetch if disabled
    if (!enabled) {
      return;
    }

    // Initial fetch
    fetchData();

    // Setup polling if interval is specified
    let intervalId: ReturnType<typeof setInterval> | null = null;
    if (pollInterval && pollInterval > 0) {
      intervalId = setInterval(() => {
        fetchData();
      }, pollInterval);
    }

    // Cleanup
    return () => {
      mountedRef.current = false;
      if (intervalId) {
        clearInterval(intervalId);
      }
    };

    // it has fetchInProgressRef guard preventing overlapping requests, and including it
    // causes interval stacking when parentId changes (fetchData recreated → effect reruns)
  }, [enabled, pollInterval, parentId, priority, type, assignee, limit]);

  return {
    data,
    loading,
    error,
    refetch,
  };
}
