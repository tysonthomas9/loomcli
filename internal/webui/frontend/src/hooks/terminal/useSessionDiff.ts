/**
 * useSessionDiff - React hook for fetching the git diff patch for a session.
 * One-shot fetch: only fetches when enabled is true and both IDs are provided.
 */

import { useState, useEffect } from "react";

import { getSessionDiff } from "@/api/terminal";
import { useWorkspaceContext } from "@/hooks/workspace";

/** Return type for the useSessionDiff hook. */
export interface UseSessionDiffResult {
  /** The diff patch string, or null if not yet loaded / not found. */
  diff: string | null;
  /** Whether a fetch is in progress. */
  isLoading: boolean;
  /** Error from the last fetch, null if successful. */
  error: Error | null;
}

export function useSessionDiff(
  taskId: string | null,
  sessionId: string | null,
  enabled: boolean,
): UseSessionDiffResult {
  const { workspaceId } = useWorkspaceContext();
  const [diff, setDiff] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  useEffect(() => {
    if (!enabled || !taskId || !sessionId) {
      return;
    }

    let cancelled = false;
    setIsLoading(true);

    const fetchDiff = async () => {
      try {
        const result = await getSessionDiff(workspaceId, taskId, sessionId);
        if (!cancelled) {
          setDiff(result);
          setError(null);
        }
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      } finally {
        if (!cancelled) {
          setIsLoading(false);
        }
      }
    };

    void fetchDiff();

    return () => {
      cancelled = true;
    };
  }, [workspaceId, taskId, sessionId, enabled]);

  return { diff, isLoading, error };
}
