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
  /** Workspace UUID to fetch. If omitted, falls back to /api/workspaces/active */
  workspaceId?: string;
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
  const { pollInterval = DEFAULT_POLL_INTERVAL, workspaceId } = options ?? {};

  const [workspace, setWorkspace] = useState<WorkspaceData | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const mountedRef = useRef(true);
  const workspaceIdRef = useRef(workspaceId);
  workspaceIdRef.current = workspaceId;

  // Initial fetch and polling.
  // Note: AbortController gates post-fetch state updates only — it does NOT cancel
  // the HTTP request because fetchWorkspace/refreshWorkspace use a module-level cache
  // with generation counters that already discard stale in-flight responses.
  useEffect(() => {
    mountedRef.current = true;
    const controller = new AbortController();

    const fetchData = async (invalidateCache = false) => {
      try {
        const data = invalidateCache
          ? await refreshWorkspace(workspaceId)
          : await fetchWorkspace(workspaceId);
        if (mountedRef.current && !controller.signal.aborted) {
          setWorkspace(data);
          setError(null);
        }
      } catch (err) {
        if (!mountedRef.current || controller.signal.aborted) return;
        if (err instanceof DOMException && err.name === "AbortError") return;
        const message =
          err instanceof Error ? err.message : "Failed to load workspace data";
        setError(message);
        // Keep stale data on error
      } finally {
        if (mountedRef.current && !controller.signal.aborted) {
          setIsLoading(false);
        }
      }
    };

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
      controller.abort();
      if (intervalId) {
        clearInterval(intervalId);
      }
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    };
  }, [pollInterval, workspaceId]);

  const refetch = useCallback(() => {
    void refreshWorkspace(workspaceIdRef.current).then(
      (data) => {
        if (mountedRef.current) {
          setWorkspace(data);
          setError(null);
        }
      },
      (err) => {
        if (mountedRef.current) {
          const message =
            err instanceof Error
              ? err.message
              : "Failed to load workspace data";
          setError(message);
        }
      },
    );
  }, []);

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
