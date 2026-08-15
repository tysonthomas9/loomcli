import { useCallback } from "react";

import {
  getRole,
  updateRolePrompt,
  type RolePromptDTO,
  type RoleSourceKind,
  type UpdateRolePromptRequest,
} from "@/api/roles";

export type { RolePromptDTO, RoleSourceKind };

export interface RolePromptActions {
  get(): Promise<RolePromptDTO>;
  update(request: UpdateRolePromptRequest): Promise<RolePromptDTO>;
}

export function useRolePrompt(
  workspaceId: string,
  roleName: string,
): RolePromptActions {
  const get = useCallback(
    () => getRole(workspaceId, roleName),
    [roleName, workspaceId],
  );
  const update = useCallback(
    (request: UpdateRolePromptRequest) =>
      updateRolePrompt(workspaceId, roleName, request),
    [roleName, workspaceId],
  );
  return { get, update };
}
