import { useEffect, useState } from "react";

import { getAgentSessionDiff } from "@/api/terminal";
import { useWorkspaceContext } from "@/hooks/workspace";

export function useAgentSessionDiff(
  agentName: string | null,
  sessionId: string | null,
  enabled: boolean,
) {
  const { workspaceId } = useWorkspaceContext();
  const [diff, setDiff] = useState<string | null>(null);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  useEffect(() => {
    if (!enabled || !agentName || !sessionId) return;
    let cancelled = false;
    setIsLoading(true);
    void getAgentSessionDiff(workspaceId, agentName, sessionId)
      .then((result) => {
        if (!cancelled) {
          setDiff(result);
          setError(null);
        }
      })
      .catch((err: unknown) => {
        if (!cancelled)
          setError(err instanceof Error ? err : new Error(String(err)));
      })
      .finally(() => {
        if (!cancelled) setIsLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [agentName, enabled, sessionId, workspaceId]);

  return { diff, isLoading, error };
}
