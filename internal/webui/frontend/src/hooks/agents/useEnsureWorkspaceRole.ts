import { useCallback } from "react";

import {
  createWorkspaceRole,
  type CreateRoleRequest,
  type WorkspaceRole,
} from "@/api/workspace";

/**
 * Returns an idempotent "ensure role" function for a workspace. The custom-role
 * templates in the create-agent gallery call this to provision their Role (and
 * seed its prompt file) before creating the agent that uses it.
 */
export function useEnsureWorkspaceRole(
  workspaceId: string,
): (req: CreateRoleRequest) => Promise<WorkspaceRole> {
  return useCallback(
    (req: CreateRoleRequest) => createWorkspaceRole(workspaceId, req),
    [workspaceId],
  );
}
