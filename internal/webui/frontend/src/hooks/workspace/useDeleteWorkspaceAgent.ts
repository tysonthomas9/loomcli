import { useCallback } from "react";

import { deleteWorkspaceAgent } from "@/api/workspace/workspace";

/** Keep workspace-agent mutations behind the hook layer used by components. */
export function useDeleteWorkspaceAgent(): (
  workspaceId: string,
  name: string,
) => Promise<void> {
  return useCallback(
    (workspaceId: string, name: string) =>
      deleteWorkspaceAgent(workspaceId, name),
    [],
  );
}
