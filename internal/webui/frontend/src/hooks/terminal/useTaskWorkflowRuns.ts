import { useCallback, useEffect, useRef, useState } from "react";

import { getTaskWorkflowRuns, type TaskWorkflowRun } from "@/api/workflows";
import { useWorkspaceContext } from "@/hooks/workspace";

export interface UseTaskWorkflowRunsResult {
  runs: TaskWorkflowRun[];
  isLoading: boolean;
  error: Error | null;
  refetch: () => void;
}

const POLL_INTERVAL_NORMAL = 10_000;
const POLL_INTERVAL_ACTIVE = 3_000;

function isActive(run: TaskWorkflowRun): boolean {
  return (
    run.status === "queued" ||
    run.status === "running" ||
    run.status === "suspended_awaiting_event"
  );
}

function dedupeRuns(runs: TaskWorkflowRun[]): TaskWorkflowRun[] {
  const seen = new Set<string>();
  return runs.filter((run) => {
    if (seen.has(run.run_id)) return false;
    seen.add(run.run_id);
    return true;
  });
}

/** Poll workflow history that has no corresponding agent session row yet. */
export function useTaskWorkflowRuns(
  taskId: string | null,
): UseTaskWorkflowRunsResult {
  const { workspaceId } = useWorkspaceContext();
  const requestKey = taskId == null ? null : `${workspaceId}\u0000${taskId}`;
  const [runs, setRuns] = useState<TaskWorkflowRun[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const generationRef = useRef(0);
  const stateKeyRef = useRef<string | null>(null);
  const fetchInProgressRef = useRef<number | null>(null);
  const runsRef = useRef(runs);
  runsRef.current = runs;

  const fetchData = useCallback(
    async (generation: number) => {
      if (!taskId || fetchInProgressRef.current === generation) return;
      fetchInProgressRef.current = generation;
      if (generationRef.current === generation) setIsLoading(true);
      try {
        const result = await getTaskWorkflowRuns(workspaceId, taskId);
        if (generationRef.current === generation) {
          const nextRuns = dedupeRuns(result);
          runsRef.current = nextRuns;
          setRuns(nextRuns);
          setError(null);
        }
      } catch (err) {
        if (generationRef.current === generation) {
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      } finally {
        if (generationRef.current === generation) setIsLoading(false);
        if (fetchInProgressRef.current === generation) {
          fetchInProgressRef.current = null;
        }
      }
    },
    [workspaceId, taskId],
  );

  const refetch = useCallback(
    () => void fetchData(generationRef.current),
    [fetchData],
  );

  useEffect(() => {
    const generation = generationRef.current + 1;
    generationRef.current = generation;

    // Clear the previous task/workspace generation before starting this one.
    // The keyed return guard below also hides stale state during the render
    // before this effect runs, so a previous task can never flash on screen.
    stateKeyRef.current = requestKey;
    runsRef.current = [];
    setRuns([]);
    setError(null);
    setIsLoading(taskId != null);
    if (!taskId) {
      return;
    }

    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | null = null;
    function scheduleNext() {
      if (cancelled || generationRef.current !== generation) return;
      const interval = runsRef.current.some(isActive)
        ? POLL_INTERVAL_ACTIVE
        : POLL_INTERVAL_NORMAL;
      timer = setTimeout(fetchAndSchedule, interval);
    }
    function fetchAndSchedule() {
      void fetchData(generation).then(scheduleNext);
    }
    // Schedule from the completed fetch so the first returned status selects
    // the correct active (3s) or idle (10s) cadence.
    fetchAndSchedule();
    return () => {
      cancelled = true;
      if (generationRef.current === generation) {
        generationRef.current = generation + 1;
      }
      if (timer) clearTimeout(timer);
    };
  }, [taskId, workspaceId, requestKey, fetchData]);

  const stateIsCurrent = stateKeyRef.current === requestKey;
  return {
    runs: stateIsCurrent ? runs : [],
    isLoading: taskId != null && (!stateIsCurrent || isLoading),
    error: stateIsCurrent ? error : null,
    refetch,
  };
}
