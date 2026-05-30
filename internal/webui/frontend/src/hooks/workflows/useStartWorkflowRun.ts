import { useCallback } from "react";

import {
  startWorkflowRun,
  type StartWorkflowRunOptions,
  type WorkflowRunResponse,
} from "@/api/workflows";
import { useWorkspaceContext } from "@/hooks/workspace";

export function useStartWorkflowRun(): (
  workflowName: string,
  options?: StartWorkflowRunOptions,
) => Promise<WorkflowRunResponse> {
  const { workspaceId } = useWorkspaceContext();
  return useCallback(
    (workflowName: string, options: StartWorkflowRunOptions = {}) =>
      startWorkflowRun(workspaceId, workflowName, options),
    [workspaceId],
  );
}
