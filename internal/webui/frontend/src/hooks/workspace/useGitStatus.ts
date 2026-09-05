/**
 * Hook for polling git status for an agent's worktree.
 * Follows a polling pattern with backoff.
 */

import { useState, useEffect, useMemo, useContext, useCallback } from "react";

import { ScopedQueryRequest } from "@/hooks/common/scopedQueryRequest";
import { QueryRecoveryContext } from "@/hooks/common/queryRecovery";

import type { GitStatus } from "@/api/workspace";
import { fetchGitStatus } from "@/api/workspace";

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
  const recovery = useContext(QueryRecoveryContext);
  const request = useMemo(
    () =>
      new ScopedQueryRequest<GitStatus>({
        load: (signal) => {
          if (!enabled || !agentName)
            return Promise.reject(new Error("Git status scope disabled"));
          return fetchGitStatus(workspaceId, agentName, { signal });
        },
        commit: (result) => {
          setStatus(result);
          setError(null);
        },
        onError: setError,
        onLoading: setLoading,
      }),
    [workspaceId, agentName, enabled],
  );
  const doFetch = useCallback(async () => {
    if (!enabled || !agentName) return;
    await request.run().catch(() => {});
  }, [request, enabled, agentName]);

  useEffect(() => {
    setStatus(null);
    setError(null);
    setLoading(false);
    if (!enabled || !agentName) return () => request.cancel();
    void doFetch();
    const intervalId = setInterval(doFetch, POLL_INTERVAL);
    return () => {
      request.cancel();
      clearInterval(intervalId);
    };
  }, [enabled, agentName, request, doFetch]);

  useEffect(() => {
    if (!enabled || !agentName || !recovery) return;
    return recovery.register(
      `git-status:${workspaceId}:${agentName}`,
      (signal) => request.run({ signal, fresh: true }),
    );
  }, [recovery, request, enabled, workspaceId, agentName]);

  return { status, loading, error, refetch: doFetch };
}
