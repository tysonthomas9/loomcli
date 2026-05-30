import { useCallback, useEffect, useRef, useState } from "react";

import { getWorkflowRunEvents, type WorkflowRunEvent } from "@/api/workflows";
import { useWorkspaceContext } from "@/hooks/workspace";

export interface UseWorkflowRunEventsResult {
  events: WorkflowRunEvent[];
  isLoading: boolean;
  error: Error | null;
  refetch: () => void;
}

const POLL_INTERVAL = 3_000;

export function useWorkflowRunEvents(
  runId: string | null,
  shouldPoll: boolean,
): UseWorkflowRunEventsResult {
  const { workspaceId } = useWorkspaceContext();
  const [events, setEvents] = useState<WorkflowRunEvent[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const mountedRef = useRef(true);
  const fetchInProgressRef = useRef(false);

  const fetchData = useCallback(async () => {
    if (!runId || fetchInProgressRef.current) return;
    fetchInProgressRef.current = true;
    setIsLoading(true);
    try {
      const result = await getWorkflowRunEvents(workspaceId, runId);
      if (mountedRef.current) {
        setEvents(result);
        setError(null);
      }
    } catch (err) {
      if (mountedRef.current) {
        setError(err instanceof Error ? err : new Error(String(err)));
      }
    } finally {
      if (mountedRef.current) setIsLoading(false);
      fetchInProgressRef.current = false;
    }
  }, [workspaceId, runId]);

  const refetch = useCallback(() => {
    void fetchData();
  }, [fetchData]);

  useEffect(() => {
    mountedRef.current = true;
    if (!runId) {
      setEvents([]);
      setError(null);
      return;
    }

    void fetchData();
    if (!shouldPoll) {
      return () => {
        mountedRef.current = false;
      };
    }

    const timer = setInterval(() => {
      void fetchData();
    }, POLL_INTERVAL);
    return () => {
      mountedRef.current = false;
      clearInterval(timer);
    };
  }, [workspaceId, runId, shouldPoll, fetchData]);

  return { events, isLoading, error, refetch };
}
