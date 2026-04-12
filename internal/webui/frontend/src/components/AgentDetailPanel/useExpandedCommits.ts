import { useState, useEffect, useCallback, useRef } from "react";

import { fetchDiffCommits } from "@/hooks/api";
import type { DiffCommit } from "@/api/issues";
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
  const [expandedCommits, setExpandedCommits] = useState<
    LoomCommitDetail[] | null
  >(null);
  const [loadingCommits, setLoadingCommits] = useState(false);

  // Reset when agent changes
  useEffect(() => {
    setExpandedCommits(null);
    setLoadingCommits(false);
  }, [agentName]);

  const agentNameRef = useRef(agentName);
  agentNameRef.current = agentName;

  const handleShowAll = useCallback(async () => {
    const name = agentNameRef.current;
    if (!name) return;
    setLoadingCommits(true);
    try {
      const allCommits = await fetchDiffCommits(workspaceId, name);
      if (agentNameRef.current !== name) return;
      setExpandedCommits(
        allCommits.map((c: DiffCommit) => ({
          hash: c.short_hash || c.hash.slice(0, 7),
          message: c.subject,
        })),
      );
    } catch (err) {
      console.error("Failed to fetch all commits:", err);
    } finally {
      if (agentNameRef.current === name) {
        setLoadingCommits(false);
      }
    }
  }, [workspaceId]);

  const handleShowLess = useCallback(() => {
    setExpandedCommits(null);
  }, []);

  return { expandedCommits, loadingCommits, handleShowAll, handleShowLess };
}
