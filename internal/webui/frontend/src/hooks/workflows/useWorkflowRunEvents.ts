import { useCallback, useEffect, useRef, useState } from "react";

import {
  getWorkflowRunEvents,
  workflowRunEventStreamUrl,
  type WorkflowRunEvent,
  type WorkflowRunStreamCompletion,
} from "@/api/workflows";
import { useWorkspaceContext } from "@/hooks/workspace";

export interface UseWorkflowRunEventsResult {
  events: WorkflowRunEvent[];
  streamCompletion: WorkflowRunStreamCompletion | null;
  isStreamComplete: boolean;
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
  const [streamCompletion, setStreamCompletion] =
    useState<WorkflowRunStreamCompletion | null>(null);
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
      setStreamCompletion(null);
      setError(null);
      return;
    }

    setStreamCompletion(null);
    void fetchData();
    if (!shouldPoll) {
      return () => {
        mountedRef.current = false;
      };
    }

    if (typeof EventSource !== "undefined") {
      let closedByCompletion = false;
      const source = new EventSource(
        workflowRunEventStreamUrl(workspaceId, runId, {
          untilTerminal: true,
        }),
      );
      source.addEventListener("workflow_event", (event) => {
        try {
          const parsed = JSON.parse(event.data) as WorkflowRunEvent;
          if (mountedRef.current) {
            setEvents((current) => mergeWorkflowRunEvent(current, parsed));
            setError(null);
          }
        } catch (err) {
          if (mountedRef.current) {
            setError(err instanceof Error ? err : new Error(String(err)));
          }
        }
      });
      source.addEventListener("workflow_run_stream_complete", (event) => {
        try {
          const parsed = JSON.parse(
            event.data,
          ) as WorkflowRunStreamCompletion;
          if (mountedRef.current) {
            setStreamCompletion(parsed);
            setError(null);
          }
          closedByCompletion = true;
          source.close();
        } catch (err) {
          if (mountedRef.current) {
            setError(err instanceof Error ? err : new Error(String(err)));
          }
        }
      });
      source.onerror = () => {
        if (closedByCompletion) return;
        if (mountedRef.current) void fetchData();
      };
      return () => {
        mountedRef.current = false;
        source.close();
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

  return {
    events,
    streamCompletion,
    isStreamComplete: streamCompletion != null,
    isLoading,
    error,
    refetch,
  };
}

function mergeWorkflowRunEvent(
  current: WorkflowRunEvent[],
  next: WorkflowRunEvent,
): WorkflowRunEvent[] {
  const existingIndex = current.findIndex(
    (event) => event.event_id === next.event_id,
  );
  if (existingIndex !== -1) {
    const copy = current.slice();
    copy[existingIndex] = next;
    return copy.sort(compareWorkflowRunEvents);
  }
  return [...current, next].sort(compareWorkflowRunEvents);
}

function compareWorkflowRunEvents(
  left: WorkflowRunEvent,
  right: WorkflowRunEvent,
): number {
  return left.event_index - right.event_index;
}
