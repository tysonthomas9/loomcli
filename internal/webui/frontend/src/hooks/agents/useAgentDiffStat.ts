/**
 * useAgentDiffStat - React hook for polling agent diff statistics.
 * Provides branch name and line-level diff stats (+added -removed) for
 * an agent's worktree, resolved directly by agent name.
 */

import { useCallback } from "react";
import { useQuery } from "@tanstack/react-query";

import { fetchAgentDiffStat } from "@/api";
import type { IssueDiffStat } from "@/api";

import { agentQueryKeys } from "@/hooks/queryKeys";
import { useWorkspaceContext } from "@/hooks/workspace";

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

function toError(error: unknown): Error | null {
  if (error == null) return null;
  return error instanceof Error ? error : new Error(String(error));
}

export function useAgentDiffStat(
  options: UseAgentDiffStatOptions,
): UseAgentDiffStatReturn {
  const { workspaceId } = useWorkspaceContext();
  const { agentName, enabled = true, pollInterval = 60000 } = options;
  const canFetch = enabled && !!agentName;

  const diffStatQuery = useQuery({
    queryKey: agentQueryKeys.diffStat(workspaceId, agentName),
    queryFn: () => fetchAgentDiffStat(workspaceId, agentName),
    enabled: canFetch,
    refetchInterval: canFetch && pollInterval > 0 ? pollInterval : false,
  });
  const { refetch: refetchDiffStat } = diffStatQuery;

  const refetch = useCallback(async () => {
    if (!agentName) return;
    await refetchDiffStat();
  }, [agentName, refetchDiffStat]);

  return {
    data: diffStatQuery.data ?? null,
    isLoading: diffStatQuery.isFetching,
    error: toError(diffStatQuery.error),
    refetch,
  };
}
