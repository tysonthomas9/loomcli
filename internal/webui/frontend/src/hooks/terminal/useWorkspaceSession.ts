import { useEffect, useState } from "react";

import { getWorkspaceSession } from "@/api/terminal";
import type { WorkspaceSessionListItem } from "@/types/agent";
import { useWorkspaceContext } from "@/hooks/workspace";

export interface UseWorkspaceSessionResult {
  session: WorkspaceSessionListItem | null;
  isLoading: boolean;
  error: Error | null;
}

export function useWorkspaceSession(
  sessionId: string | null,
): UseWorkspaceSessionResult {
  const { workspaceId } = useWorkspaceContext();
  const [session, setSession] = useState<WorkspaceSessionListItem | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  useEffect(() => {
    if (!sessionId) {
      setSession(null);
      setError(null);
      return;
    }

    let cancelled = false;
    setIsLoading(true);

    const run = async () => {
      try {
        const result = await getWorkspaceSession(workspaceId, sessionId);
        if (cancelled) return;
        setSession(result);
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
  }, [workspaceId, sessionId]);

  return { session, isLoading, error };
}
