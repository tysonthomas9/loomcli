/**
 * useTaskSessions - React hook for fetching session records for a given task.
 * Polls at 10s normally, 3s when any session is active (running).
 * Also subscribes to SSE session_change events for immediate refetch.
 */

import {
  useState,
  useEffect,
  useCallback,
  useRef,
  useMemo,
  useContext,
} from "react";

import { getTaskSessions } from "@/api/terminal";
import type { MutationPayload } from "@/api/common";
import type { SessionRecord } from "@/types/agent";
import { useEventContext, useEventSubscription } from "@/hooks/common";
import { ScopedQueryRequest } from "@/hooks/common/scopedQueryRequest";
import { QueryRecoveryContext } from "@/hooks/common/queryRecovery";
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
  const { connectionEpoch } = useEventContext();
  const [sessions, setSessions] = useState<SessionRecord[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  const recovery = useContext(QueryRecoveryContext);
  const sessionsRef = useRef(sessions);
  const connectionEpochRef = useRef(connectionEpoch);
  sessionsRef.current = sessions; // always keep ref in sync with latest state

  const request = useMemo(
    () =>
      new ScopedQueryRequest<SessionRecord[]>({
        load: (signal) => {
          if (!taskId)
            return Promise.reject(new Error("Task session scope disabled"));
          return getTaskSessions(workspaceId, taskId, { signal });
        },
        commit: (result) => {
          setSessions(result);
          setError(null);
        },
        onError: setError,
        onLoading: setIsLoading,
      }),
    [workspaceId, taskId],
  );

  const fetchData = useCallback(async () => {
    if (!taskId) return;
    await request.run().catch(() => {});
  }, [request, taskId]);

  useEffect(() => {
    if (!taskId || !recovery) return;
    return recovery.register(
      `task-sessions:${workspaceId}:${taskId}`,
      (signal) => request.run({ signal, fresh: true }),
    );
  }, [recovery, request, workspaceId, taskId]);

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
    const previous = connectionEpochRef.current;
    connectionEpochRef.current = connectionEpoch;
    if (!taskId || connectionEpoch <= previous) return;
    void request.run({ fresh: true }).catch(() => {});
  }, [connectionEpoch, request, taskId]);

  useEffect(() => {
    let active = true;
    setSessions([]);
    setError(null);

    if (!taskId) {
      setSessions([]);
      setError(null);
      setIsLoading(false);
      return () => request.cancel();
    }

    void fetchData();

    const hasActive = () => sessionsRef.current.some((s) => s.is_active);

    const getPollInterval = () =>
      hasActive() ? POLL_INTERVAL_ACTIVE : POLL_INTERVAL_NORMAL;

    // Use setTimeout chain so interval adapts to active state changes
    let timer: ReturnType<typeof setTimeout> | null = null;

    const scheduleNext = () => {
      if (!active) return;
      timer = setTimeout(() => {
        void fetchData().then(scheduleNext);
      }, getPollInterval());
    };

    scheduleNext();

    return () => {
      active = false;
      request.cancel();
      if (timer) clearTimeout(timer);
    };
  }, [workspaceId, taskId, fetchData, request]);

  return { sessions, isLoading, error, refetch };
}
