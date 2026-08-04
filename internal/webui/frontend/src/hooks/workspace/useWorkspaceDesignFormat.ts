import { useCallback, useState } from "react";

import { updateWorkspaceDesignFormat } from "@/api/workspace";

import { useWorkspaceContext } from "./useWorkspaceContext";

export interface UseWorkspaceDesignFormatReturn {
  isSaving: boolean;
  error: string | null;
  updateDesignFormat: (format: "markdown" | "html") => Promise<boolean>;
}

export function useWorkspaceDesignFormat(): UseWorkspaceDesignFormatReturn {
  const { workspaceId, refetch } = useWorkspaceContext();
  const [isSaving, setIsSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const updateDesignFormat = useCallback(
    async (format: "markdown" | "html"): Promise<boolean> => {
      if (!workspaceId || isSaving) return false;
      setIsSaving(true);
      setError(null);
      try {
        await updateWorkspaceDesignFormat(workspaceId, format);
        refetch();
        return true;
      } catch (err) {
        setError(
          err instanceof Error ? err.message : "Failed to update design format",
        );
        return false;
      } finally {
        setIsSaving(false);
      }
    },
    [isSaving, refetch, workspaceId],
  );

  return { isSaving, error, updateDesignFormat };
}
