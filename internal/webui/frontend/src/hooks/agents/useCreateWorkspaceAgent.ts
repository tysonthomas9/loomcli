import { useCallback } from "react";

import {
  createWorkspaceAgent,
  type CreateAgentRequest,
  type WorkspaceAgentInfo,
} from "@/api/workspace";

export function useCreateWorkspaceAgent(
  workspaceId: string,
): (request: CreateAgentRequest) => Promise<WorkspaceAgentInfo> {
  return useCallback(
    (request: CreateAgentRequest) => createWorkspaceAgent(workspaceId, request),
    [workspaceId],
  );
}
