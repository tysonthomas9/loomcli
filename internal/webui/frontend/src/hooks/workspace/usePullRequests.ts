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
const MAX_POLL_INTERVAL = 5 * 60_000;

export interface UsePullRequestsOptions {
  state?: PullRequestListState;
  enabled?: boolean;
}

export interface UsePullRequestsReturn {
  pullRequests: GitPullRequest[];
  /** Per-repo listing failures; non-fatal (e.g. gh missing for one repo). */
  warnings: string[];
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
  const [warnings, setWarnings] = useState<string[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  // Monotonic sequence: bumped on every fetch and on effect cleanup, so a
  // response is only committed if no newer fetch (or workspace/state switch)
  // superseded it.
  const requestSeqRef = useRef(0);
  // Dedupes overlapping polls for the same workspace/state without blocking
  // the first fetch after a switch.
  const inFlightKeyRef = useRef<string | null>(null);
  const pollDelayRef = useRef(POLL_INTERVAL);

  const invalidatePendingRequest = useCallback(() => {
    requestSeqRef.current++;
    inFlightKeyRef.current = null;
  }, []);

  const doFetch = useCallback(async () => {
    if (!enabled) return;
    const key = `${workspaceId}|${state}`;
    if (inFlightKeyRef.current === key) return;
    inFlightKeyRef.current = key;
    const seq = ++requestSeqRef.current;
    setLoading((prev) => (prev ? prev : true));

    try {
      const result = await fetchPullRequests(workspaceId, state);
      if (seq === requestSeqRef.current) {
        setPullRequests(result.pullRequests);
        setWarnings(result.warnings);
        setError(null);
        pollDelayRef.current = POLL_INTERVAL;
      }
    } catch (err) {
      if (seq === requestSeqRef.current) {
        setError(err instanceof Error ? err : new Error(String(err)));
        pollDelayRef.current = Math.min(
          pollDelayRef.current * 2,
          MAX_POLL_INTERVAL,
        );
      }
    } finally {
      if (inFlightKeyRef.current === key) {
        inFlightKeyRef.current = null;
      }
      if (seq === requestSeqRef.current) {
        setLoading(false);
      }
    }
  }, [workspaceId, state, enabled]);

  useEffect(() => {
    setPullRequests([]);
    setWarnings([]);
    setError(null);
    pollDelayRef.current = POLL_INTERVAL;
  }, [workspaceId, state]);

  useEffect(() => {
    if (!enabled) return;

    let stopped = false;
    let timeoutId: ReturnType<typeof setTimeout> | null = null;

    const clearPoll = (): void => {
      if (timeoutId !== null) {
        clearTimeout(timeoutId);
        timeoutId = null;
      }
    };
    const schedulePoll = (): void => {
      clearPoll();
      if (stopped || document.hidden) return;
      timeoutId = setTimeout(() => {
        void runPoll();
      }, pollDelayRef.current);
    };
    const runPoll = async (): Promise<void> => {
      await doFetch();
      schedulePoll();
    };
    const handleVisibilityChange = (): void => {
      clearPoll();
      if (!document.hidden) void runPoll();
    };

    document.addEventListener("visibilitychange", handleVisibilityChange);
    if (!document.hidden) void runPoll();
    return () => {
      stopped = true;
      clearPoll();
      document.removeEventListener("visibilitychange", handleVisibilityChange);
      // Invalidate any in-flight response for the old workspace/state and
      // let the next key's fetch start immediately.
      invalidatePendingRequest();
    };
  }, [enabled, doFetch, invalidatePendingRequest]);

  return { pullRequests, warnings, loading, error, refetch: doFetch };
}
