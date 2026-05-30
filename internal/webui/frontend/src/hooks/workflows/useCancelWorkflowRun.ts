import { useCallback } from "react";

import { cancelWorkflowRun, type WorkflowRun } from "@/api/workflows";
import { useWorkspaceContext } from "@/hooks/workspace";

export function useCancelWorkflowRun(): (
  runId: string,
) => Promise<WorkflowRun> {
  const { workspaceId } = useWorkspaceContext();
  return useCallback(
    (runId: string) => cancelWorkflowRun(workspaceId, runId),
    [workspaceId],
  );
}
