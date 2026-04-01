/**
 * useWorkspace - React hook for workspace data with automatic polling.
 * Owns caching and in-flight deduplication via React state/refs.
 * Extends useWorkspaceRepos pattern with configurable poll interval
 * and tab-visibility-aware refetching.
 */

import { useState, useCallback, useEffect, useRef } from "react";

import { fetchWorkspaceApi } from "@/api/workspace";
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
 * Refetches on tab visibility change. Deduplicates concurrent in-flight requests.
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

  // In-flight deduplication: holds the current pending promise
  const inflightRef = useRef<Promise<WorkspaceData> | null>(null);
  // Generation counter: incremented on workspaceId change or explicit refetch
  // to discard stale responses
  const generationRef = useRef(0);

  // Initial fetch and polling.
  // Generation is bumped inside the effect (not during render) to avoid
  // double-increments in React Concurrent Mode where renders can be replayed.
  useEffect(() => {
    mountedRef.current = true;
    inflightRef.current = null;
    const gen = ++generationRef.current;
    const controller = new AbortController();

    const fetchData = async () => {
      // Deduplicate: if there's already an in-flight request for this generation, reuse it
      if (inflightRef.current && generationRef.current === gen) {
        try {
          const data = await inflightRef.current;
          if (
            mountedRef.current &&
            !controller.signal.aborted &&
            generationRef.current === gen
          ) {
            setWorkspace(data);
            setError(null);
          }
        } catch (err) {
          if (
            !mountedRef.current ||
            controller.signal.aborted ||
            generationRef.current !== gen
          )
            return;
          if (err instanceof DOMException && err.name === "AbortError") return;
          const message =
            err instanceof Error
              ? err.message
              : "Failed to load workspace data";
          setError(message);
        } finally {
          if (mountedRef.current && !controller.signal.aborted) {
            setIsLoading(false);
          }
        }
        return;
      }

      // Start a new fetch
      const promise = fetchWorkspaceApi(workspaceId);
      inflightRef.current = promise;

      try {
        const data = await promise;
        if (
          mountedRef.current &&
          !controller.signal.aborted &&
          generationRef.current === gen
        ) {
          setWorkspace(data);
          setError(null);
        }
      } catch (err) {
        if (
          !mountedRef.current ||
          controller.signal.aborted ||
          generationRef.current !== gen
        )
          return;
        if (err instanceof DOMException && err.name === "AbortError") return;
        const message =
          err instanceof Error ? err.message : "Failed to load workspace data";
        setError(message);
        // Keep stale data on error
      } finally {
        // Clear in-flight promise once settled (only if it's still ours)
        if (inflightRef.current === promise) {
          inflightRef.current = null;
        }
        if (mountedRef.current && !controller.signal.aborted) {
          setIsLoading(false);
        }
      }
    };

    void fetchData();

    let intervalId: ReturnType<typeof setInterval> | null = null;
    if (pollInterval > 0) {
      intervalId = setInterval(() => {
        // For poll ticks, always make a fresh request (clear in-flight to avoid dedup)
        inflightRef.current = null;
        void fetchData();
      }, pollInterval);
    }

    // Refetch on tab visibility change
    const handleVisibilityChange = () => {
      if (document.visibilityState === "visible") {
        inflightRef.current = null;
        void fetchData();
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
    // Clear in-flight promise and bump generation to force a fresh fetch
    inflightRef.current = null;
    const gen = ++generationRef.current;
    const promise = fetchWorkspaceApi(workspaceIdRef.current);
    inflightRef.current = promise;

    void promise.then(
      (data) => {
        if (inflightRef.current === promise) {
          inflightRef.current = null;
        }
        if (mountedRef.current && generationRef.current === gen) {
          setWorkspace(data);
          setError(null);
        }
      },
      (err) => {
        if (inflightRef.current === promise) {
          inflightRef.current = null;
        }
        if (mountedRef.current && generationRef.current === gen) {
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
