import { useCallback, useEffect, useRef, useState } from "react";

import {
  isWorkflowRunLive,
  listWorkflowRuns,
  type WorkflowRunListItem,
} from "@/api/workflows";
import { useWorkspaceContext } from "@/hooks/workspace";

export interface UseTaskWorkflowRunsResult {
  runs: WorkflowRunListItem[];
  isLoading: boolean;
  error: Error | null;
  refetch: () => void;
}

const POLL_INTERVAL_NORMAL = 10_000;
const POLL_INTERVAL_ACTIVE = 3_000;

export function useTaskWorkflowRuns(
  taskId: string | null,
): UseTaskWorkflowRunsResult {
  const { workspaceId } = useWorkspaceContext();
  const [runs, setRuns] = useState<WorkflowRunListItem[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const mountedRef = useRef(true);
  const fetchInProgressRef = useRef(false);
  const runsRef = useRef(runs);
  runsRef.current = runs;

  const fetchData = useCallback(async () => {
    if (!taskId || fetchInProgressRef.current) return;
    fetchInProgressRef.current = true;
    setIsLoading(true);
    try {
      const result = await listWorkflowRuns(workspaceId, {
        workItemId: taskId,
        limit: 100,
      });
      if (mountedRef.current) {
        setRuns(result);
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
  }, [workspaceId, taskId]);

  const refetch = useCallback(() => {
    void fetchData();
  }, [fetchData]);

  useEffect(() => {
    mountedRef.current = true;
    if (!taskId) {
      setRuns([]);
      setError(null);
      return;
    }

    void fetchData();

    const hasLiveRun = () =>
      runsRef.current.some((item) => isWorkflowRunLive(item.run.status));
    const getPollInterval = () =>
      hasLiveRun() ? POLL_INTERVAL_ACTIVE : POLL_INTERVAL_NORMAL;

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

  return { runs, isLoading, error, refetch };
}
