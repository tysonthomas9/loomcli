/**
 * useTaskSessions - React hook for fetching session records for a given task.
 * Polls at 10s normally, 3s when any session is active (running).
 * Also subscribes to SSE session_change events for immediate refetch.
 */

import { useState, useEffect, useCallback, useRef } from "react";

import { getTaskSessions } from "@/api/terminal";
import type { MutationPayload } from "@/api/common";
import type { SessionRecord } from "@/types/agent";
import { useEventSubscription } from "@/hooks/common";
import { useWorkspaceContext } from "@/hooks/workspace";

/** Return type for the useTaskSessions hook. */
export interface UseTaskSessionsResult {
  /** Session records for the task, newest first. */
  sessions: SessionRecord[];
  /** Whether a fetch is in progress. */
  isLoading: boolean;
  /** Error from the last fetch, null if successful. */
  error: Error | null;
  /** Manually trigger a refetch. */
  refetch: () => void;
}

/** Normal polling interval (ms). */
const POLL_INTERVAL_NORMAL = 10_000;
/** Fast polling interval when any session is active (ms). */
const POLL_INTERVAL_ACTIVE = 3_000;

interface TaskSessionsState {
  requestKey: string;
  sessions: SessionRecord[];
  isLoading: boolean;
  error: Error | null;
}

export function useTaskSessions(taskId: string | null): UseTaskSessionsResult {
  const { workspaceId } = useWorkspaceContext();
  // Preserve the local/unscoped compatibility path where WorkspaceContext
  // intentionally supplies an empty workspace ID. Task identity still gates
  // the request, while workspace ID remains part of the generation fence.
  const requestKey = taskId ? JSON.stringify([workspaceId, taskId]) : "";
  const [state, setState] = useState<TaskSessionsState>({
    requestKey: "",
    sessions: [],
    isLoading: false,
    error: null,
  });
  const mountedRef = useRef(true);
  const activeRequestKeyRef = useRef(requestKey);
  const generationRef = useRef(0);
  const inFlightGenerationRef = useRef<number | null>(null);
  const sessionsRef = useRef<SessionRecord[]>([]);
  activeRequestKeyRef.current = requestKey;

  const fetchData = useCallback(async () => {
    if (!requestKey || !taskId) return;
    const generation = generationRef.current;
    // A new route generation may read immediately while an old visit drains,
    // but never overlap reads for the same visible task generation.
    if (inFlightGenerationRef.current === generation) return;
    inFlightGenerationRef.current = generation;
    setState((current) => ({
      requestKey,
      sessions: current.requestKey === requestKey ? current.sessions : [],
      isLoading: true,
      error: current.requestKey === requestKey ? current.error : null,
    }));

    try {
      const result = await getTaskSessions(workspaceId, taskId);
      if (
        mountedRef.current &&
        generation === generationRef.current &&
        activeRequestKeyRef.current === requestKey
      ) {
        setState({
          requestKey,
          sessions: result,
          isLoading: false,
          error: null,
        });
      }
    } catch (err) {
      if (
        mountedRef.current &&
        generation === generationRef.current &&
        activeRequestKeyRef.current === requestKey
      ) {
        setState((current) => ({
          requestKey,
          sessions: current.requestKey === requestKey ? current.sessions : [],
          isLoading: false,
          error: err instanceof Error ? err : new Error(String(err)),
        }));
      }
    } finally {
      if (inFlightGenerationRef.current === generation) {
        inFlightGenerationRef.current = null;
      }
    }
  }, [workspaceId, taskId, requestKey]);

  const refetch = useCallback(() => {
    void fetchData();
  }, [fetchData]);

  // Subscribe to SSE session_change events for immediate refetch.
  // Keeps polling as a fallback in case the SSE connection drops.
  const handleMutation = useCallback(
    (mutation: MutationPayload) => {
      if (mutation.type !== "session_change") return;
      // Only refetch when the event is for our task (or no task filter)
      if (taskId && mutation.issue_id && mutation.issue_id !== taskId) return;
      void fetchData();
    },
    [taskId, fetchData],
  );

  useEventSubscription(handleMutation, {
    types: ["session_change"],
  });

  useEffect(() => {
    mountedRef.current = true;
    generationRef.current += 1;

    setState({
      requestKey,
      sessions: [],
      isLoading: Boolean(requestKey),
      error: null,
    });
    if (!requestKey) {
      return;
    }

    const hasActive = () => sessionsRef.current.some((s) => s.is_active);

    const getPollInterval = () =>
      hasActive() ? POLL_INTERVAL_ACTIVE : POLL_INTERVAL_NORMAL;

    // Chain each poll from request completion so slow reads never overlap, and
    // recompute the interval after every response as active state changes.
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;
    const poll = async () => {
      await fetchData();
      if (cancelled) return;
      timer = setTimeout(() => void poll(), getPollInterval());
    };
    void poll();

    return () => {
      cancelled = true;
      mountedRef.current = false;
      if (timer) clearTimeout(timer);
    };
  }, [requestKey, fetchData]);

  // Effects reset state after commit. Mask a prior task's response during the
  // intervening render so it cannot populate the newly selected task detail.
  const visible =
    requestKey && state.requestKey === requestKey
      ? state
      : {
          requestKey,
          sessions: [],
          isLoading: Boolean(requestKey),
          error: null,
        };
  sessionsRef.current = visible.sessions;

  return {
    sessions: visible.sessions,
    isLoading: visible.isLoading,
    error: visible.error,
    refetch,
  };
}
