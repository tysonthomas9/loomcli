import { useCallback, useEffect, useRef, useState } from "react";

import {
  isWorkflowRunLive,
  listWorkflowRuns,
  type ListWorkflowRunsParams,
  type WorkflowRunListItem,
} from "@/api/workflows";
import { useWorkspaceContext } from "@/hooks/workspace";

export interface UseWorkflowRunsResult {
  runs: WorkflowRunListItem[];
  isLoading: boolean;
  error: Error | null;
  refetch: () => void;
}

const POLL_INTERVAL_NORMAL = 10_000;
const POLL_INTERVAL_ACTIVE = 3_000;

export function useWorkflowRuns(
  params: ListWorkflowRunsParams = {},
): UseWorkflowRunsResult {
  const { workspaceId } = useWorkspaceContext();
  const [runs, setRuns] = useState<WorkflowRunListItem[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const mountedRef = useRef(true);
  const fetchInProgressRef = useRef(false);
  const runsRef = useRef(runs);
  runsRef.current = runs;

  const workItemId = params.workItemId;
  const workflowName = params.workflowName;
  const status = params.status;
  const limit = params.limit;

  const fetchData = useCallback(async () => {
    if (fetchInProgressRef.current) return;
    fetchInProgressRef.current = true;
    setIsLoading(true);
    try {
      const request: ListWorkflowRunsParams = {};
      if (workItemId) request.workItemId = workItemId;
      if (workflowName) request.workflowName = workflowName;
      if (status) request.status = status;
      if (limit != null) request.limit = limit;
      const result = await listWorkflowRuns(workspaceId, request);
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
  }, [workspaceId, workItemId, workflowName, status, limit]);

  const refetch = useCallback(() => {
    void fetchData();
  }, [fetchData]);

  useEffect(() => {
    mountedRef.current = true;
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
  }, [fetchData]);

  return { runs, isLoading, error, refetch };
}
