import { useCallback, useEffect, useRef, useState } from "react";

import {
  getWorkflowRunEvents,
  workflowRunEventStreamUrl,
  type WorkflowRunEvent,
  type WorkflowRunStreamCompletion,
  type WorkflowRunStreamError,
} from "@/api/workflows";
import { useWorkspaceContext } from "@/hooks/workspace";

export type WorkflowRunEventStreamStatus =
  | "idle"
  | "connecting"
  | "connected"
  | "reconnecting"
  | "polling"
  | "complete"
  | "error";

export interface UseWorkflowRunEventsResult {
  events: WorkflowRunEvent[];
  streamCompletion: WorkflowRunStreamCompletion | null;
  streamStatus: WorkflowRunEventStreamStatus;
  reconnectCount: number;
  lastEventIndex: number | null;
  isStreamComplete: boolean;
  isLoading: boolean;
  error: Error | null;
  refetch: () => void;
  retryStream: () => void;
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
  const [streamStatus, setStreamStatus] =
    useState<WorkflowRunEventStreamStatus>("idle");
  const [reconnectCount, setReconnectCount] = useState(0);
  const [streamRetryNonce, setStreamRetryNonce] = useState(0);
  const [lastEventIndex, setLastEventIndex] = useState<number | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const mountedRef = useRef(true);
  const fetchInProgressRef = useRef(false);
  const lastEventIndexRef = useRef<number | null>(null);
  const currentRunIdRef = useRef<string | null>(null);

  const resetLastEventIndex = useCallback(() => {
    lastEventIndexRef.current = null;
    setLastEventIndex(null);
  }, []);

  const recordLastEventIndex = useCallback((index: number) => {
    if (index <= 0) return;
    setLastEventIndex((current) => {
      const next = current == null ? index : Math.max(current, index);
      lastEventIndexRef.current = next;
      return next;
    });
  }, []);

  const fetchData = useCallback(async () => {
    if (!runId || fetchInProgressRef.current) return;
    fetchInProgressRef.current = true;
    setIsLoading(true);
    try {
      const result = await getWorkflowRunEvents(workspaceId, runId);
      if (mountedRef.current) {
        setEvents(result);
        const maxIndex = result.reduce(
          (current, event) => Math.max(current, event.event_index),
          0,
        );
        if (maxIndex > 0) recordLastEventIndex(maxIndex);
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
  }, [workspaceId, runId, recordLastEventIndex]);

  const refetch = useCallback(() => {
    void fetchData();
  }, [fetchData]);

  const retryStream = useCallback(() => {
    if (!runId) return;
    setError(null);
    setStreamCompletion(null);
    setStreamStatus(shouldPoll ? "connecting" : "polling");
    void fetchData();
    if (shouldPoll && typeof EventSource !== "undefined") {
      setStreamRetryNonce((current) => current + 1);
    }
  }, [fetchData, runId, shouldPoll]);

  useEffect(() => {
    mountedRef.current = true;
    if (!runId) {
      currentRunIdRef.current = null;
      setEvents([]);
      setStreamCompletion(null);
      setStreamStatus("idle");
      setReconnectCount(0);
      resetLastEventIndex();
      setError(null);
      return;
    }

    if (currentRunIdRef.current !== runId) {
      currentRunIdRef.current = runId;
      setEvents([]);
      resetLastEventIndex();
      setError(null);
    }
    setStreamCompletion(null);
    setReconnectCount(0);
    void fetchData();
    if (!shouldPoll) {
      setStreamStatus("idle");
      return () => {
        mountedRef.current = false;
      };
    }

    if (typeof EventSource !== "undefined") {
      let closedByServerEvent = false;
      setStreamStatus("connecting");
      const since = lastEventIndexRef.current;
      const streamOptions: { untilTerminal: true; since?: string } = {
        untilTerminal: true,
      };
      if (since != null) streamOptions.since = String(since);
      const source = new EventSource(
        workflowRunEventStreamUrl(workspaceId, runId, streamOptions),
      );
      source.onopen = () => {
        if (mountedRef.current) {
          setStreamStatus("connected");
        }
      };
      source.addEventListener("workflow_event", (event) => {
        try {
          const parsed = JSON.parse(event.data) as WorkflowRunEvent;
          if (mountedRef.current) {
            setEvents((current) => mergeWorkflowRunEvent(current, parsed));
            recordLastEventIndex(parsed.event_index);
            setStreamStatus("connected");
            setError(null);
          }
        } catch (err) {
          if (mountedRef.current) {
            setStreamStatus("error");
            setError(err instanceof Error ? err : new Error(String(err)));
          }
        }
      });
      source.addEventListener("workflow_run_stream_complete", (event) => {
        try {
          const parsed = JSON.parse(event.data) as WorkflowRunStreamCompletion;
          if (mountedRef.current) {
            setStreamCompletion(parsed);
            setStreamStatus("complete");
            setError(null);
          }
          closedByServerEvent = true;
          source.close();
        } catch (err) {
          if (mountedRef.current) {
            setStreamStatus("error");
            setError(err instanceof Error ? err : new Error(String(err)));
          }
        }
      });
      source.addEventListener("workflow_run_stream_error", (event) => {
        try {
          const parsed = JSON.parse(event.data) as WorkflowRunStreamError;
          const message =
            parsed.message || parsed.error || "Workflow run stream failed";
          if (mountedRef.current) {
            setStreamStatus("error");
            setError(new Error(message));
          }
          closedByServerEvent = true;
          source.close();
        } catch (err) {
          if (mountedRef.current) {
            setStreamStatus("error");
            setError(err instanceof Error ? err : new Error(String(err)));
          }
        }
      });
      source.onerror = () => {
        if (closedByServerEvent) return;
        if (mountedRef.current) {
          setStreamStatus("reconnecting");
          setReconnectCount((current) => current + 1);
          void fetchData();
        }
      };
      return () => {
        mountedRef.current = false;
        source.close();
      };
    }

    setStreamStatus("polling");
    const timer = setInterval(() => {
      void fetchData();
    }, POLL_INTERVAL);
    return () => {
      mountedRef.current = false;
      clearInterval(timer);
    };
  }, [
    workspaceId,
    runId,
    shouldPoll,
    fetchData,
    streamRetryNonce,
    recordLastEventIndex,
    resetLastEventIndex,
  ]);

  return {
    events,
    streamCompletion,
    streamStatus,
    reconnectCount,
    lastEventIndex,
    isStreamComplete: streamCompletion != null,
    isLoading,
    error,
    refetch,
    retryStream,
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
