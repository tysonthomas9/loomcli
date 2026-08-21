/**
 * Hook for polling git status for an agent's worktree.
 * Follows a polling pattern with backoff.
 */

import { useCallback } from "react";
import { useQuery } from "@tanstack/react-query";

import type { GitStatus } from "@/api/workspace";
import { fetchGitStatus } from "@/api/workspace";
import { agentQueryKeys } from "@/hooks/queryKeys";

import { useWorkspaceContext } from "./useWorkspaceContext";

const POLL_INTERVAL = 5000; // 5 seconds

export interface UseGitStatusOptions {
  agentName: string | null;
  enabled: boolean;
  /**
   * Poll cadence in ms while the query is active. Defaults to 5s. Consumers
   * that only need coarse freshness (e.g. the PR-link badge) can pass a longer
   * interval; they still share the same query key, so React Query dedupes their
   * fetches against any 5s consumer that is mounted at the same time.
   */
  pollInterval?: number;
}

export interface UseGitStatusReturn {
  status: GitStatus | null;
  loading: boolean;
  error: Error | null;
  refetch: () => Promise<void>;
}

function toError(error: unknown): Error | null {
  if (error == null) return null;
  return error instanceof Error ? error : new Error(String(error));
}

export function useGitStatus({
  agentName,
  enabled,
  pollInterval = POLL_INTERVAL,
}: UseGitStatusOptions): UseGitStatusReturn {
  const { workspaceId } = useWorkspaceContext();
  const canFetch = enabled && !!agentName;
  const statusQuery = useQuery({
    queryKey: agentQueryKeys.agentGitStatus(workspaceId, agentName ?? ""),
    queryFn: () => fetchGitStatus(workspaceId, agentName ?? ""),
    enabled: canFetch,
    refetchInterval: canFetch ? pollInterval : false,
  });
  const { refetch: refetchStatus } = statusQuery;

  const refetch = useCallback(async () => {
    if (!agentName) return;
    await refetchStatus();
  }, [agentName, refetchStatus]);

  return {
    status: statusQuery.data ?? null,
    loading: statusQuery.isFetching,
    error: toError(statusQuery.error),
    refetch,
  };
}
