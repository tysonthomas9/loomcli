import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import {
  getWorkflowRunEvents,
  isWorkflowRunLive,
  type WorkflowRun,
  type WorkflowRunEvent,
} from "@/api/workflows";
import { useWorkspaceContext } from "@/hooks/workspace";

export interface UseWorkflowRunEventSnapshotsResult {
  eventsByRunId: Record<string, WorkflowRunEvent[]>;
  isLoading: boolean;
  error: Error | null;
  refetch: () => void;
}

const POLL_INTERVAL_ACTIVE = 5_000;
const RUN_ID_SEPARATOR = "\u0000";

export function useWorkflowRunEventSnapshots(
  runs: WorkflowRun[],
): UseWorkflowRunEventSnapshotsResult {
  const { workspaceId } = useWorkspaceContext();
  const [eventsByRunId, setEventsByRunId] = useState<
    Record<string, WorkflowRunEvent[]>
  >({});
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const mountedRef = useRef(true);
  const requestSeqRef = useRef(0);

  const runIds = useMemo(() => runs.map((run) => run.run_id), [runs]);
  const runKey = useMemo(() => runIds.join(RUN_ID_SEPARATOR), [runIds]);
  const hasLiveRun = useMemo(
    () => runs.some((run) => isWorkflowRunLive(run.status)),
    [runs],
  );

  const fetchData = useCallback(async () => {
    const ids = runKey ? runKey.split(RUN_ID_SEPARATOR) : [];
    if (ids.length === 0) {
      setEventsByRunId({});
      setError(null);
      setIsLoading(false);
      return;
    }
    const requestSeq = requestSeqRef.current + 1;
    requestSeqRef.current = requestSeq;
    setIsLoading(true);
    try {
      const results = await Promise.allSettled(
        ids.map(async (runId) => {
          const events = await getWorkflowRunEvents(workspaceId, runId);
          return { runId, events };
        }),
      );
      const next: Record<string, WorkflowRunEvent[]> = {};
      const failures: string[] = [];
      for (const result of results) {
        if (result.status === "fulfilled") {
          next[result.value.runId] = result.value.events;
        } else {
          failures.push(
            result.reason instanceof Error
              ? result.reason.message
              : String(result.reason),
          );
        }
      }
      if (mountedRef.current && requestSeq === requestSeqRef.current) {
        setEventsByRunId(next);
        setError(
          failures.length > 0
            ? new Error(
                `Failed to load compared run events: ${failures.join("; ")}`,
              )
            : null,
        );
      }
    } catch (err) {
      if (mountedRef.current && requestSeq === requestSeqRef.current) {
        setError(err instanceof Error ? err : new Error(String(err)));
      }
    } finally {
      if (mountedRef.current && requestSeq === requestSeqRef.current) {
        setIsLoading(false);
      }
    }
  }, [workspaceId, runKey]);

  const refetch = useCallback(() => {
    void fetchData();
  }, [fetchData]);

  useEffect(() => {
    mountedRef.current = true;
    if (!runKey) {
      setEventsByRunId({});
      setError(null);
      setIsLoading(false);
      return;
    }

    void fetchData();
    if (!hasLiveRun) {
      return () => {
        mountedRef.current = false;
      };
    }

    const timer = setInterval(() => {
      void fetchData();
    }, POLL_INTERVAL_ACTIVE);
    return () => {
      mountedRef.current = false;
      clearInterval(timer);
    };
  }, [fetchData, hasLiveRun, runKey]);

  return { eventsByRunId, isLoading, error, refetch };
}
