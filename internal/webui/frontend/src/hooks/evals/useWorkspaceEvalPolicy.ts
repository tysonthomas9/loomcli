import { useCallback, useState } from "react";

import {
  updateWorkspaceEvalPolicy,
  type WorkspaceEvalPolicyPatch,
} from "@/api/workspace";
import { useWorkspaceContext } from "@/hooks/workspace";

export interface UseWorkspaceEvalPolicyReturn {
  isSaving: boolean;
  error: string | null;
  updateEvalPolicy: (patch: WorkspaceEvalPolicyPatch) => Promise<boolean>;
}

export function useWorkspaceEvalPolicy(): UseWorkspaceEvalPolicyReturn {
  const { workspaceId, refetch } = useWorkspaceContext();
  const [isSaving, setIsSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const updateEvalPolicy = useCallback(
    async (patch: WorkspaceEvalPolicyPatch): Promise<boolean> => {
      if (!workspaceId || isSaving) return false;
      setIsSaving(true);
      setError(null);
      try {
        await updateWorkspaceEvalPolicy(workspaceId, patch);
        refetch();
        return true;
      } catch (err) {
        setError(
          err instanceof Error ? err.message : "Failed to update eval policy",
        );
        return false;
      } finally {
        setIsSaving(false);
      }
    },
    [isSaving, refetch, workspaceId],
  );

  return { isSaving, error, updateEvalPolicy };
}
