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
}: UseGitStatusOptions): UseGitStatusReturn {
  const { workspaceId } = useWorkspaceContext();
  const canFetch = enabled && !!agentName;
  const statusQuery = useQuery({
    queryKey: agentQueryKeys.agentGitStatus(workspaceId, agentName ?? ""),
    queryFn: () => fetchGitStatus(workspaceId, agentName ?? ""),
    enabled: canFetch,
    refetchInterval: canFetch ? POLL_INTERVAL : false,
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
