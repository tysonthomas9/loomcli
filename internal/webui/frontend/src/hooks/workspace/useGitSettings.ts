import { useCallback, useEffect, useState } from "react";

import {
  getGitSettings,
  updateGitSettings,
  type GitSettings,
} from "@/api/workspace/git";

export type { GitSettings };

function errorText(cause: unknown): string {
  return cause instanceof Error ? cause.message : String(cause);
}

/** Reads and changes the workspace Git settings (D29) on the server. */
export function useGitSettings(workspaceId: string | null) {
  const [settings, setSettings] = useState<GitSettings | null>(null);
  const [error, setError] = useState("");
  const [warning, setWarning] = useState("");
  const [isSaving, setIsSaving] = useState(false);

  useEffect(() => {
    if (!workspaceId) return;
    let cancelled = false;
    setSettings(null);
    setError("");
    getGitSettings(workspaceId)
      .then((loaded) => {
        if (!cancelled) setSettings(loaded);
      })
      .catch((cause: unknown) => {
        if (!cancelled) setError(errorText(cause));
      });
    return () => {
      cancelled = true;
    };
  }, [workspaceId]);

  const update = useCallback(
    async (change: Partial<GitSettings>) => {
      if (!workspaceId) return;
      setIsSaving(true);
      try {
        const result = await updateGitSettings(workspaceId, change);
        setSettings(result.settings);
        setWarning(result.warning);
        setError("");
      } catch (cause) {
        setError(errorText(cause));
      } finally {
        setIsSaving(false);
      }
    },
    [workspaceId],
  );

  return { settings, error, warning, isSaving, update };
}
