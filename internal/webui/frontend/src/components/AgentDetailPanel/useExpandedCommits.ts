import { useState, useEffect, useCallback, useRef, useMemo } from "react";
import { useQuery } from "@tanstack/react-query";

import { fetchDiffCommits } from "@/hooks/api";
import type { DiffCommit } from "@/api/issues";
import { agentQueryKeys } from "@/hooks/queryKeys";
import type { LoomCommitDetail } from "@/types/agent";

/**
 * Hook to manage fetching and displaying all commits for an agent.
 * Returns expanded commits (or null if not expanded), loading state,
 * and handlers to show all / show less.
 */
export function useExpandedCommits(
  workspaceId: string,
  agentName: string | null,
) {
  const [isExpanded, setIsExpanded] = useState(false);

  // Reset when agent changes
  useEffect(() => {
    setIsExpanded(false);
  }, [agentName]);

  const agentNameRef = useRef(agentName);
  agentNameRef.current = agentName;

  const commitsQuery = useQuery({
    queryKey: agentQueryKeys.diffCommits(workspaceId, agentName ?? ""),
    queryFn: () => fetchDiffCommits(workspaceId, agentName ?? ""),
    enabled: isExpanded && !!agentName,
  });

  useEffect(() => {
    if (commitsQuery.error) {
      console.error("Failed to fetch all commits:", commitsQuery.error);
    }
  }, [commitsQuery.error]);

  const expandedCommits = useMemo<LoomCommitDetail[] | null>(() => {
    if (!isExpanded || !commitsQuery.data) return null;
    return commitsQuery.data.map((c: DiffCommit) => ({
      hash: c.short_hash || c.hash.slice(0, 7),
      message: c.subject,
    }));
  }, [commitsQuery.data, isExpanded]);

  const handleShowAll = useCallback(async () => {
    if (!agentNameRef.current) return;
    setIsExpanded(true);
  }, []);

  const handleShowLess = useCallback(() => {
    setIsExpanded(false);
  }, []);

  return {
    expandedCommits,
    loadingCommits: isExpanded && commitsQuery.isFetching,
    handleShowAll,
    handleShowLess,
  };
}
