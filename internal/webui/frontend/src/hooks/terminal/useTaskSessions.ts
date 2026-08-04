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

export function useTaskSessions(taskId: string | null): UseTaskSessionsResult {
  const { workspaceId } = useWorkspaceContext();
  const [sessions, setSessions] = useState<SessionRecord[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  const mountedRef = useRef(true);
  const fetchInProgressRef = useRef(false);
  const sessionsRef = useRef(sessions);
  sessionsRef.current = sessions; // always keep ref in sync with latest state

  const fetchData = useCallback(async () => {
    if (!taskId) return;
    if (fetchInProgressRef.current) return;
    fetchInProgressRef.current = true;
    setIsLoading(true);

    try {
      const result = await getTaskSessions(workspaceId, taskId);
      if (mountedRef.current) {
        setSessions(result);
        setError(null);
      }
    } catch (err) {
      if (mountedRef.current) {
        setError(err instanceof Error ? err : new Error(String(err)));
      }
    } finally {
      if (mountedRef.current) {
        setIsLoading(false);
      }
      fetchInProgressRef.current = false;
    }
  }, [workspaceId, taskId]);

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

    if (!taskId) {
      setSessions([]);
      setError(null);
      return;
    }

    void fetchData();

    const hasActive = () => sessionsRef.current.some((s) => s.is_active);

    const getPollInterval = () =>
      hasActive() ? POLL_INTERVAL_ACTIVE : POLL_INTERVAL_NORMAL;

    // Use setTimeout chain so interval adapts to active state changes
    let timer: ReturnType<typeof setTimeout> | null = null;

    const scheduleNext = () => {
      if (!mountedRef.current) return;
      timer = setTimeout(() => {
        void fetchData().then(scheduleNext);
      }, getPollInterval());
    };

    scheduleNext();

    return () => {
      mountedRef.current = false;
      if (timer) clearTimeout(timer);
    };
  }, [workspaceId, taskId, fetchData]);

  return { sessions, isLoading, error, refetch };
}
