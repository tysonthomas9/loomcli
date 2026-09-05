/**
 * useBlockedIssues - shared, event-invalidated blocked-issue projection.
 * The endpoint remains authoritative; the SSE stream is only an invalidation
 * hint and a five-minute hidden-paused poll repairs missed or early events.
 */

import { useCallback, useMemo } from "react";

import { getBlockedIssues, type BlockedFilter } from "@/api/issues";
import { useInvalidatedQuery } from "@/hooks/common";
import type { BlockedIssue } from "@/types";

import { useWorkspaceContext } from "@/hooks/workspace";

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
  /** Optional: whether to fetch (default: true) */
  enabled?: boolean;
}

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
 * React hook for the workspace's issues with blocking dependencies.
 *
 * @param options - Configuration options for the hook
 * @returns Object with data, loading, error states and refetch function
 *
 * @example
 * ```tsx
 * function DependencyGraph() {
 *   const { data, loading, error, refetch } = useBlockedIssues()
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
export function useBlockedIssues(
  options?: UseBlockedIssuesOptions,
): UseBlockedIssuesResult {
  const { workspaceId, sourceReposFilter } = useWorkspaceContext();
  const {
    parentId,
    priority,
    type,
    assignee,
    limit,
    enabled = true,
  } = options ?? {};

  const filter = useMemo<BlockedFilter>(() => {
    const next: BlockedFilter = {};
    if (parentId) next.parent_id = parentId;
    if (priority !== undefined) next.priority = priority;
    if (type) next.type = type;
    if (assignee) next.assignee = assignee;
    if (sourceReposFilter?.length) next.source_repos = sourceReposFilter;
    if (limit !== undefined) next.limit = limit;
    return next;
  }, [assignee, limit, parentId, priority, sourceReposFilter, type]);

  const key = useMemo(
    () =>
      `blocked:${workspaceId}:${JSON.stringify({ parentId, priority, type, assignee, limit, sourceReposFilter })}`,
    [assignee, limit, parentId, priority, sourceReposFilter, type, workspaceId],
  );
  const fetcher = useCallback(
    (signal: AbortSignal) => getBlockedIssues(workspaceId, filter, { signal }),
    [filter, workspaceId],
  );

  return useInvalidatedQuery<BlockedIssue[]>(fetcher, {
    key,
    enabled,
    entityTypes: ["issue", "dependency", "label"],
    safetyPollMs: 5 * 60_000,
    pauseWhenHidden: true,
    refetchOnConnect: true,
    resetOnKeyChange: false,
  });
}
