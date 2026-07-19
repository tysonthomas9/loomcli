import { useEffect, useState } from "react";

import { getWorkspaceSessionDiff } from "@/api/terminal";
import { useWorkspaceContext } from "@/hooks/workspace";

export interface UseWorkspaceSessionDiffResult {
  diff: string | null;
  isLoading: boolean;
  error: Error | null;
}

export function useWorkspaceSessionDiff(
  sessionId: string | null,
  enabled: boolean,
): UseWorkspaceSessionDiffResult {
  const { workspaceId } = useWorkspaceContext();
  const [diff, setDiff] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  useEffect(() => {
    if (!enabled || !sessionId) return;

    let cancelled = false;
    setIsLoading(true);

    const run = async () => {
      try {
        const result = await getWorkspaceSessionDiff(workspaceId, sessionId);
        if (cancelled) return;
        setDiff(result);
        setError(null);
      } catch (err) {
        if (cancelled) return;
        setError(err instanceof Error ? err : new Error(String(err)));
      } finally {
        if (!cancelled) setIsLoading(false);
      }
    };

    void run();

    return () => {
      cancelled = true;
    };
  }, [workspaceId, sessionId, enabled]);

  return { diff, isLoading, error };
}
