import { useCallback, useState } from "react";

import { updateWorkspaceTaskDelivery } from "@/api/workspace";
import type { TaskDeliveryRequirement } from "@/api/workspace";

import { useWorkspaceContext } from "./useWorkspaceContext";

export interface UseTaskDeliveryPolicyReturn {
  savingScope: string | null;
  error: string | null;
  updateRequirement: (
    requirement: TaskDeliveryRequirement | "",
    repository?: string,
  ) => Promise<boolean>;
}

export function useTaskDeliveryPolicy(): UseTaskDeliveryPolicyReturn {
  const { workspaceId, refetch } = useWorkspaceContext();
  const [savingScope, setSavingScope] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  const updateRequirement = useCallback(
    async (
      requirement: TaskDeliveryRequirement | "",
      repository?: string,
    ): Promise<boolean> => {
      if (!workspaceId || savingScope) return false;
      setSavingScope(repository ?? "workspace");
      setError(null);
      try {
        await updateWorkspaceTaskDelivery(workspaceId, requirement, repository);
        await refetch();
        return true;
      } catch (err) {
        setError(
          err instanceof Error
            ? err.message
            : "Failed to update task delivery policy",
        );
        return false;
      } finally {
        setSavingScope(null);
      }
    },
    [refetch, savingScope, workspaceId],
  );

  return { savingScope, error, updateRequirement };
}
