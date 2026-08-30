import { useEffect, useState } from "react";

import { getAgentSessionTranscript } from "@/api/terminal";
import type { TranscriptEntry } from "@/types/agent";
import { useWorkspaceContext } from "@/hooks/workspace";

export function useAgentSessionTranscript(
  agentName: string | null,
  sessionId: string | null,
  isActive: boolean,
) {
  const { workspaceId } = useWorkspaceContext();
  const [entries, setEntries] = useState<TranscriptEntry[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  useEffect(() => {
    if (!agentName || !sessionId) {
      setEntries([]);
      setError(null);
      return;
    }
    let cancelled = false;
    const load = async () => {
      setIsLoading(true);
      try {
        const result = await getAgentSessionTranscript(
          workspaceId,
          agentName,
          sessionId,
        );
        if (!cancelled) {
          setEntries(result);
          setError(null);
        }
      } catch (err) {
        if (!cancelled)
          setError(err instanceof Error ? err : new Error(String(err)));
      } finally {
        if (!cancelled) setIsLoading(false);
      }
    };
    void load();
    const timer = isActive ? setInterval(() => void load(), 3_000) : null;
    return () => {
      cancelled = true;
      if (timer) clearInterval(timer);
    };
  }, [agentName, isActive, sessionId, workspaceId]);

  return { entries, isLoading, error };
}
