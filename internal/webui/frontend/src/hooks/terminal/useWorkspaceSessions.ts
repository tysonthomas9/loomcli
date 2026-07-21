import { useCallback, useEffect, useState } from "react";

import { listWorkspaceSessions } from "@/api/terminal";
import type {
  WorkspaceSessionFilters,
  WorkspaceSessionListItem,
} from "@/types/agent";
import { useWorkspaceContext } from "@/hooks/workspace";

export interface UseWorkspaceSessionsResult {
  sessions: WorkspaceSessionListItem[];
  total: number;
  limit: number;
  scoreDimensions: string[];
  isLoading: boolean;
  error: Error | null;
  refetch: () => Promise<void>;
}

export function useWorkspaceSessions(
  filters: WorkspaceSessionFilters,
): UseWorkspaceSessionsResult {
  const { workspaceId } = useWorkspaceContext();
  const [sessions, setSessions] = useState<WorkspaceSessionListItem[]>([]);
  const [total, setTotal] = useState(0);
  const [limit, setLimit] = useState(filters.limit ?? 0);
  const [scoreDimensions, setScoreDimensions] = useState<string[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  const fetchSessions = useCallback(async () => {
    setIsLoading(true);
    try {
      const result = await listWorkspaceSessions(workspaceId, filters);
      setSessions(result.sessions);
      setTotal(result.total);
      setLimit(result.limit);
      setScoreDimensions(result.score_dimensions);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err : new Error(String(err)));
    } finally {
      setIsLoading(false);
    }
  }, [workspaceId, filters]);

  useEffect(() => {
    let cancelled = false;
    setIsLoading(true);

    const run = async () => {
      try {
        const result = await listWorkspaceSessions(workspaceId, filters);
        if (cancelled) return;
        setSessions(result.sessions);
        setTotal(result.total);
        setLimit(result.limit);
        setScoreDimensions(result.score_dimensions);
        setError(null);
      } catch (err) {
        if (cancelled) return;
        setError(err instanceof Error ? err : new Error(String(err)));
      } finally {
        if (!cancelled) setIsLoading(false);
      }
    };

    void run();

    return () => {
      cancelled = true;
    };
  }, [workspaceId, filters]);

  return {
    sessions,
    total,
    limit,
    scoreDimensions,
    isLoading,
    error,
    refetch: fetchSessions,
  };
}
