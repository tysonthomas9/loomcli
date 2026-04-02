/**
 * Hook for polling git status for an agent's worktree.
 * Follows a polling pattern with backoff.
 */

import { useState, useEffect, useRef, useCallback } from "react";

import type { GitStatus } from "@/api/git";
import { fetchGitStatus } from "@/api/git";

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

export function useGitStatus({
  agentName,
  enabled,
}: UseGitStatusOptions): UseGitStatusReturn {
  const { workspaceId } = useWorkspaceContext();
  const [status, setStatus] = useState<GitStatus | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const fetchInProgressRef = useRef(false);
  const mountedRef = useRef(true);

  // Reset state when agent changes
  useEffect(() => {
    setStatus(null);
    setError(null);
    setLoading(false);
  }, [agentName]);

  const doFetch = useCallback(async () => {
    if (!agentName || fetchInProgressRef.current) return;

    fetchInProgressRef.current = true;
    setLoading((prev) => (prev ? prev : true));

    try {
      const result = await fetchGitStatus(workspaceId, agentName);
      if (mountedRef.current) {
        setStatus(result);
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
  }, [workspaceId, agentName]);

  useEffect(() => {
    mountedRef.current = true;

    if (!enabled || !agentName) return;

    // Fetch immediately
    void doFetch();

    // Poll on interval
    const intervalId = setInterval(doFetch, POLL_INTERVAL);

    return () => {
      mountedRef.current = false;
      clearInterval(intervalId);
    };
  }, [enabled, agentName, doFetch]);

  return { status, loading, error, refetch: doFetch };
}
