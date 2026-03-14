/**
 * useWorkspace - React hook for workspace data with automatic polling.
 * Extends useWorkspaceRepos pattern with configurable poll interval
 * and tab-visibility-aware refetching.
 */

import { useState, useCallback, useEffect, useRef } from "react";

import { fetchWorkspace, refreshWorkspace } from "@/api/workspace";
import type {
  WorkspaceData,
  RepoInfo,
  WorkspaceAgentInfo,
} from "@/api/workspace";

export interface UseWorkspaceOptions {
  /** Poll interval in ms (default: 60000 = 60s) */
  pollInterval?: number;
}

export interface UseWorkspaceReturn {
  /** Full workspace data, null if not loaded */
  workspace: WorkspaceData | null;
  /** Convenience alias for workspace.repos (empty array if not loaded) */
  repos: RepoInfo[];
  /** Workspace group names */
  groups: string[];
  /** Agent assignments */
  agents: WorkspaceAgentInfo[];
  /** Whether a fetch is in progress */
  isLoading: boolean;
  /** Error message from the last fetch */
  error: string | null;
  /** Re-fetch workspace data from the API (invalidates cache) */
  refetch: () => void;
}

const DEFAULT_POLL_INTERVAL = 60000;

/**
 * React hook for workspace data with automatic polling.
 * Polls GET /api/workspace at configurable interval. Keeps stale data on error.
 * Refetches on tab visibility change.
 */
export function useWorkspace(
  options?: UseWorkspaceOptions,
): UseWorkspaceReturn {
  const { pollInterval = DEFAULT_POLL_INTERVAL } = options ?? {};

  const [workspace, setWorkspace] = useState<WorkspaceData | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const mountedRef = useRef(true);

  const fetchData = useCallback(async (invalidateCache = false) => {
    try {
      const data = invalidateCache
        ? await refreshWorkspace()
        : await fetchWorkspace();
      if (mountedRef.current) {
        setWorkspace(data);
        setError(null);
      }
    } catch (err) {
      if (mountedRef.current) {
        const message =
          err instanceof Error ? err.message : "Failed to load workspace data";
        setError(message);
        // Keep stale data on error
      }
    } finally {
      if (mountedRef.current) {
        setIsLoading(false);
      }
    }
  }, []);

  // Initial fetch and polling
  useEffect(() => {
    mountedRef.current = true;

    void fetchData();

    let intervalId: ReturnType<typeof setInterval> | null = null;
    if (pollInterval > 0) {
      intervalId = setInterval(() => {
        void fetchData(true);
      }, pollInterval);
    }

    // Refetch on tab visibility change
    const handleVisibilityChange = () => {
      if (document.visibilityState === "visible") {
        void fetchData(true);
      }
    };
    document.addEventListener("visibilitychange", handleVisibilityChange);

    return () => {
      mountedRef.current = false;
      if (intervalId) {
        clearInterval(intervalId);
      }
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, [pollInterval, fetchData]);

  const refetch = useCallback(() => {
    void fetchData(true);
  }, [fetchData]);

  return {
    workspace,
    repos: workspace?.repos ?? [],
    groups: workspace?.groups ?? [],
    agents: workspace?.agents ?? [],
    isLoading,
    error,
    refetch,
  };
}
