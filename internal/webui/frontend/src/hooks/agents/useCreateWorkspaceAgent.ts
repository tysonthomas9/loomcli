import { useCallback } from "react";

import {
  createWorkspaceAgent,
  type CreateAgentRequest,
  type WorkspaceAgentInfo,
} from "@/api/workspace";
import { createAgent, newRequestId } from "@/api/agentsv1";
import type { Agent, CreateAgentBody } from "@/api/agentsv1";

export function useCreateWorkspaceAgent(
  workspaceId: string,
): (request: CreateAgentRequest) => Promise<WorkspaceAgentInfo> {
  return useCallback(
    (request: CreateAgentRequest) => createWorkspaceAgent(workspaceId, request),
    [workspaceId],
  );
}

/** Creates an agent through the Agent API, one RequestID per call. */
export function useCreateLead(
  workspaceId: string,
): (body: CreateAgentBody) => Promise<Agent> {
  return useCallback(
    (body: CreateAgentBody) => createAgent(workspaceId, body, newRequestId()),
    [workspaceId],
  );
}
