import { useEffect, useState } from "react";

import { getWorkspaceTraceRun } from "@/api/terminal";
import type { WorkspaceTraceRunData } from "@/types/agent";
import { useWorkspaceContext } from "@/hooks/workspace";

export interface UseWorkspaceTraceRunResult {
  run: WorkspaceTraceRunData | null;
  isLoading: boolean;
  error: Error | null;
}

export function useWorkspaceTraceRun(
  taskRunId: string | undefined,
): UseWorkspaceTraceRunResult {
  const { workspaceId } = useWorkspaceContext();
  const [run, setRun] = useState<WorkspaceTraceRunData | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  useEffect(() => {
    if (!taskRunId) {
      setRun(null);
      setError(null);
      setIsLoading(false);
      return;
    }

    let cancelled = false;
    setIsLoading(true);
    void getWorkspaceTraceRun(workspaceId, taskRunId).then(
      (result) => {
        if (cancelled) return;
        setRun(result);
        setError(null);
        setIsLoading(false);
      },
      (err) => {
        if (cancelled) return;
        setError(err instanceof Error ? err : new Error(String(err)));
        setIsLoading(false);
      },
    );

    return () => {
      cancelled = true;
    };
  }, [taskRunId, workspaceId]);

  return { run, isLoading, error };
}
