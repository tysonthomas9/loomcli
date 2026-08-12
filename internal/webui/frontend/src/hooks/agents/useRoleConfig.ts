import { useCallback, useMemo } from "react";

import {
  cloneWorkspaceRole,
  deleteWorkspaceRole,
  getWorkspaceRole,
  updateWorkspaceRole,
  type CloneRoleRequest,
  type RoleWithPrompt,
  type UpdateRoleRequest,
  type WorkspaceRole,
} from "@/api/workspace";

export interface UseRoleConfigReturn {
  /** Read a single role + its current prompt body for the editor. */
  getRole: (name: string) => Promise<RoleWithPrompt>;
  /**
   * Apply a partial edit. Sending `prompt` rewrites the prompt file; the change
   * takes effect on the agent's NEXT start/restart — a running agent keeps the
   * prompt it read at launch.
   */
  updateRole: (name: string, req: UpdateRoleRequest) => Promise<RoleWithPrompt>;
  /** Clone a role under a new name. Rejects with `ApiError` 409 when taken. */
  cloneRole: (name: string, req: CloneRoleRequest) => Promise<WorkspaceRole>;
  /** Delete a custom role. Rejects with `ApiError` 400 for builtins. */
  deleteRole: (name: string) => Promise<void>;
}

/**
 * Role read/edit/clone/delete bound to a workspace. Backs the agent-config
 * panel's Phase B management actions, mirroring the idempotent provisioning
 * shape of `useEnsureWorkspaceRole` / `useConnectorProvisioning`.
 */
export function useRoleConfig(workspaceId: string): UseRoleConfigReturn {
  const getRole = useCallback(
    (name: string): Promise<RoleWithPrompt> =>
      getWorkspaceRole(workspaceId, name),
    [workspaceId],
  );

  const updateRole = useCallback(
    (name: string, req: UpdateRoleRequest): Promise<RoleWithPrompt> =>
      updateWorkspaceRole(workspaceId, name, req),
    [workspaceId],
  );

  const cloneRole = useCallback(
    (name: string, req: CloneRoleRequest): Promise<WorkspaceRole> =>
      cloneWorkspaceRole(workspaceId, name, req),
    [workspaceId],
  );

  const deleteRole = useCallback(
    (name: string): Promise<void> => deleteWorkspaceRole(workspaceId, name),
    [workspaceId],
  );

  return useMemo(
    () => ({ getRole, updateRole, cloneRole, deleteRole }),
    [getRole, updateRole, cloneRole, deleteRole],
  );
}
