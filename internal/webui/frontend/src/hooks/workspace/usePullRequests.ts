/**
 * Hook for loading GitHub pull requests for the active workspace.
 */

import { useState, useEffect, useRef, useCallback } from "react";

import {
  fetchPullRequests,
  type GitPullRequest,
  type PullRequestListState,
} from "@/api/workspace/pullRequests";

import { useWorkspaceContext } from "./useWorkspaceContext";

const POLL_INTERVAL = 30_000;

export interface UsePullRequestsOptions {
  state?: PullRequestListState;
  enabled?: boolean;
}

export interface UsePullRequestsReturn {
  pullRequests: GitPullRequest[];
  loading: boolean;
  error: Error | null;
  refetch: () => Promise<void>;
}

export function usePullRequests({
  state = "all",
  enabled = true,
}: UsePullRequestsOptions = {}): UsePullRequestsReturn {
  const { workspaceId } = useWorkspaceContext();
  const [pullRequests, setPullRequests] = useState<GitPullRequest[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const fetchInProgressRef = useRef(false);
  const mountedRef = useRef(true);

  const doFetch = useCallback(async () => {
    if (!enabled || fetchInProgressRef.current) return;

    fetchInProgressRef.current = true;
    setLoading((prev) => (prev ? prev : true));

    try {
      const result = await fetchPullRequests(workspaceId, state);
      if (mountedRef.current) {
        setPullRequests(result);
        setError(null);
      }
    } catch (err) {
      if (mountedRef.current) {
        setError(err instanceof Error ? err : new Error(String(err)));
      }
    } finally {
      fetchInProgressRef.current = false;
      if (mountedRef.current) {
        setLoading(false);
      }
    }
  }, [workspaceId, state, enabled]);

  useEffect(() => {
    mountedRef.current = true;
    setPullRequests([]);
    setError(null);
  }, [workspaceId, state]);

  useEffect(() => {
    mountedRef.current = true;
    if (!enabled) return;

    void doFetch();
    const intervalId = setInterval(doFetch, POLL_INTERVAL);
    return () => {
      mountedRef.current = false;
      clearInterval(intervalId);
    };
  }, [enabled, doFetch]);

  return { pullRequests, loading, error, refetch: doFetch };
}
