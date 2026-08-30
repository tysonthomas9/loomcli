import { useCallback, useEffect, useRef, useState } from "react";

import { getAgentSessions } from "@/api/terminal";
import type { SessionRecord } from "@/types/agent";
import { useWorkspaceContext } from "@/hooks/workspace";

export function useAgentSessions(agentName: string | null) {
  const { workspaceId } = useWorkspaceContext();
  const [sessions, setSessions] = useState<SessionRecord[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const activeRef = useRef(true);
  const fetchingRef = useRef(false);

  const fetchData = useCallback(async () => {
    if (!agentName || fetchingRef.current) return;
    fetchingRef.current = true;
    setIsLoading(true);
    try {
      const result = await getAgentSessions(workspaceId, agentName);
      if (activeRef.current) {
        setSessions(result);
        setError(null);
      }
    } catch (err) {
      if (activeRef.current) {
        setError(err instanceof Error ? err : new Error(String(err)));
      }
    } finally {
      if (activeRef.current) setIsLoading(false);
      fetchingRef.current = false;
    }
  }, [agentName, workspaceId]);

  useEffect(() => {
    activeRef.current = true;
    if (!agentName) {
      setSessions([]);
      setError(null);
      return;
    }
    void fetchData();
    const timer = setInterval(() => void fetchData(), 10_000);
    return () => {
      activeRef.current = false;
      clearInterval(timer);
    };
  }, [agentName, fetchData]);

  return { sessions, isLoading, error, refetch: fetchData };
}
