/**
 * useWorkspaceRepos - React hook for workspace repository data.
 * Fetches workspace config on mount (read-only, no update support).
 */

import { useState, useCallback, useEffect, useRef } from "react";

import { fetchWorkspace } from "@/api/workspace";
import type { WorkspaceData, RepoInfo } from "@/api/workspace";

export interface UseWorkspaceReposReturn {
  /** Full workspace data, null if not loaded */
  workspace: WorkspaceData | null;
  /** Convenience alias for workspace.repos (empty array if not loaded) */
  repos: RepoInfo[];
  /** Whether a fetch is in progress */
  isLoading: boolean;
  /** Error message from the last fetch */
  error: string | null;
  /** Re-fetch workspace data from the API */
  refetch: () => void;
}

/**
 * React hook for workspace repository data.
 * Fetches from GET /api/workspace on mount. Read-only — repo list is static.
 */
export function useWorkspaceRepos(): UseWorkspaceReposReturn {
  const [workspace, setWorkspace] = useState<WorkspaceData | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const fetchData = useCallback(async () => {
    setIsLoading(true);
    setError(null);

    try {
      const data = await fetchWorkspace();
      if (mountedRef.current) {
        setWorkspace(data);
      }
    } catch (err) {
      if (mountedRef.current) {
        const message =
          err instanceof Error ? err.message : "Failed to load workspace data";
        setError(message);
      }
    } finally {
      if (mountedRef.current) {
        setIsLoading(false);
      }
    }
  }, []);

  useEffect(() => {
    fetchData();
  }, [fetchData]);

  return {
    workspace,
    repos: workspace?.repos ?? [],
    isLoading,
    error,
    refetch: fetchData,
  };
}
