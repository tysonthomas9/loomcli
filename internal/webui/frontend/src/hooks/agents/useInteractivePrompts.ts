import { useEffect, useState } from "react";

import {
  fetchInteractivePrompts,
  type InteractivePromptInfo,
} from "@/api/workspace";

export interface UseInteractivePromptsReturn {
  prompts: InteractivePromptInfo[];
  isLoading: boolean;
  error: string | null;
}

export function useInteractivePrompts(
  workspaceId: string,
): UseInteractivePromptsReturn {
  const [prompts, setPrompts] = useState<InteractivePromptInfo[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!workspaceId) {
      setPrompts([]);
      setIsLoading(false);
      setError(null);
      return;
    }

    let active = true;
    setIsLoading(true);
    setError(null);
    fetchInteractivePrompts(workspaceId)
      .then((items) => {
        if (!active) return;
        setPrompts(items);
      })
      .catch((err: unknown) => {
        if (!active) return;
        setError(err instanceof Error ? err.message : "Failed to load prompts");
      })
      .finally(() => {
        if (active) setIsLoading(false);
      });

    return () => {
      active = false;
    };
  }, [workspaceId]);

  return { prompts, isLoading, error };
}
