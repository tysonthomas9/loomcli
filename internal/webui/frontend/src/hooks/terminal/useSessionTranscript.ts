/**
 * useSessionTranscript - React hook for fetching transcript entries for a session.
 * Polls every 3s when the session is active; fetches once when inactive.
 */

import { useEffect, useMemo, useState } from "react";

import {
  getAgentSessionTranscript,
  getSessionTranscript,
} from "@/api/terminal";
import type { TranscriptEntry } from "@/types/agent";
import { ApiError } from "@/types/common";
import { useWorkspaceContext } from "@/hooks/workspace";

/** Return type for the useSessionTranscript hook. */
export interface UseSessionTranscriptResult {
  /** Transcript entries for the session. */
  entries: TranscriptEntry[];
  /** Whether a fetch is in progress. */
  isLoading: boolean;
  /** The server has no transcript for this terminal session. */
  isUnavailable: boolean;
  /** Error from the last fetch, null if successful. */
  error: Error | null;
}

export interface UseSessionTranscriptOptions {
  /**
   * Retry a missing or failed one-shot fetch until the transcript is available.
   * Workflow run detail uses this while a terminal run's durable session
   * projection is still catching up with the run projection.
   */
  retryUnavailable?: boolean;
  /**
   * Fetch through the generic agent-session route when the session has no
   * owning task (for example, an interactive Lead or PR Review terminal).
   * Task-scoped sessions continue to use taskId when both are provided.
   */
  agentId?: string;
}

interface TranscriptState extends UseSessionTranscriptResult {
  requestKey: string;
}

/** Poll interval when session is active (ms). */
const POLL_INTERVAL_ACTIVE = 3_000;
/** Cap missing active-session transcript polling at one request per 30s. */
const POLL_INTERVAL_ACTIVE_MAX = 30_000;
/** Bound recovery retries for a completed session to 15 seconds. */
const MAX_TRANSCRIPT_RETRIES = 5;

function isTransientTranscriptError(err: unknown): boolean {
  return (
    err instanceof ApiError &&
    (err.status === 0 ||
      err.status === 408 ||
      err.status === 429 ||
      err.status >= 500)
  );
}

export function useSessionTranscript(
  taskId: string | null,
  sessionId: string | null,
  isActive: boolean,
  options: UseSessionTranscriptOptions = {},
): UseSessionTranscriptResult {
  const { workspaceId } = useWorkspaceContext();
  const retryUnavailable = options.retryUnavailable === true;
  const normalizedTaskId = taskId?.trim() || null;
  const normalizedSessionId = sessionId?.trim() || null;
  const agentId = options.agentId?.trim() || null;
  const transcriptOwnerKind = normalizedTaskId
    ? "task"
    : agentId
      ? "agent"
      : null;
  const transcriptOwnerId = normalizedTaskId || agentId;
  const requestKey = useMemo(
    () =>
      transcriptOwnerKind && transcriptOwnerId && normalizedSessionId
        ? JSON.stringify([
            workspaceId,
            transcriptOwnerKind,
            transcriptOwnerId,
            normalizedSessionId,
          ])
        : "",
    [workspaceId, transcriptOwnerKind, transcriptOwnerId, normalizedSessionId],
  );
  const [state, setState] = useState<TranscriptState>({
    requestKey,
    entries: [],
    isLoading: false,
    isUnavailable: false,
    error: null,
  });

  useEffect(() => {
    if (!transcriptOwnerKind || !transcriptOwnerId || !normalizedSessionId) {
      setState({
        requestKey,
        entries: [],
        isLoading: false,
        isUnavailable: false,
        error: null,
      });
      return;
    }

    let cancelled = false;
    let activeTimer: ReturnType<typeof setTimeout> | null = null;
    let retryTimer: ReturnType<typeof setTimeout> | null = null;
    let latestRequest = 0;
    let retryAttempts = 0;
    let activePollDelay = POLL_INTERVAL_ACTIVE;
    let backoffActivePoll = false;

    setState((current) => ({
      requestKey,
      entries: current.requestKey === requestKey ? current.entries : [],
      isLoading: true,
      isUnavailable: false,
      error: current.requestKey === requestKey ? current.error : null,
    }));

    const scheduleRetry = (shouldRetry: boolean): boolean => {
      if (
        !shouldRetry ||
        isActive ||
        retryTimer ||
        retryAttempts >= MAX_TRANSCRIPT_RETRIES
      ) {
        return false;
      }
      retryAttempts += 1;
      retryTimer = setTimeout(() => {
        retryTimer = null;
        void fetchTranscript();
      }, POLL_INTERVAL_ACTIVE);
      return true;
    };

    const fetchTranscript = async () => {
      const request = ++latestRequest;
      setState((current) =>
        current.requestKey === requestKey
          ? {
              ...current,
              isLoading: true,
              isUnavailable: false,
              error: null,
            }
          : current,
      );
      try {
        const result =
          transcriptOwnerKind === "task"
            ? await getSessionTranscript(
                workspaceId,
                transcriptOwnerId,
                normalizedSessionId,
                { preserveNotFound: true },
              )
            : await getAgentSessionTranscript(
                workspaceId,
                transcriptOwnerId,
                normalizedSessionId,
                { preserveNotFound: true },
              );
        if (!cancelled && request === latestRequest) {
          retryAttempts = 0;
          activePollDelay = POLL_INTERVAL_ACTIVE;
          backoffActivePoll = false;
          setState({
            requestKey,
            entries: result,
            isLoading: false,
            isUnavailable: false,
            error: null,
          });
        }
      } catch (err) {
        if (!cancelled && request === latestRequest) {
          const unavailable = err instanceof ApiError && err.status === 404;
          const transient = isTransientTranscriptError(err);
          backoffActivePoll = unavailable || transient;
          // retryUnavailable retains its existing projection-catchup behavior
          // for any one-shot failure. A transient API failure retries even for
          // canonical completed sessions, whose callers do not opt into that
          // projection behavior.
          const willRetry = scheduleRetry(retryUnavailable || transient);
          const projectionPending = unavailable && (isActive || willRetry);
          const transientRetryPending = transient && (isActive || willRetry);
          setState((current) => ({
            requestKey,
            entries: current.requestKey === requestKey ? current.entries : [],
            // A missing durable projection is still pending while a bounded
            // retry is scheduled. Keep the transcript in its loading state
            // instead of briefly claiming that it has no entries.
            isLoading: projectionPending || transientRetryPending,
            isUnavailable: unavailable && !isActive && !willRetry,
            error:
              unavailable || transientRetryPending
                ? null
                : err instanceof Error
                  ? err
                  : new Error(String(err)),
          }));
        }
      }
    };

    const scheduleActivePoll = () => {
      if (cancelled || !isActive) return;
      const delay = activePollDelay;
      if (backoffActivePoll) {
        activePollDelay = Math.min(
          activePollDelay * 2,
          POLL_INTERVAL_ACTIVE_MAX,
        );
      }
      activeTimer = setTimeout(() => {
        activeTimer = null;
        void fetchTranscript().finally(scheduleActivePoll);
      }, delay);
    };

    // Chain active polling from request completion so a slow server never
    // accumulates overlapping reads or invalidates every response.
    void fetchTranscript().finally(scheduleActivePoll);

    return () => {
      cancelled = true;
      if (activeTimer) clearTimeout(activeTimer);
      if (retryTimer) clearTimeout(retryTimer);
    };
  }, [
    workspaceId,
    transcriptOwnerKind,
    transcriptOwnerId,
    normalizedSessionId,
    isActive,
    retryUnavailable,
    requestKey,
  ]);

  if (state.requestKey !== requestKey) {
    return {
      entries: [],
      isLoading: requestKey !== "",
      isUnavailable: false,
      error: null,
    };
  }
  return {
    entries: state.entries,
    isLoading: state.isLoading,
    isUnavailable: state.isUnavailable,
    error: state.error,
  };
}
