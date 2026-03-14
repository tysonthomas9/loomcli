import { useState, useCallback, useEffect, useRef } from "react";

import { listTerminalSessions } from "@/api/terminal";
import type { TerminalSessionInfo } from "@/api/terminal";

const DEFAULT_SESSIONS: TerminalSessionInfo[] = [
  { name: "talk-to-lead", label: "talk-to-lead", created: 0 },
];

export interface UseTerminalSessionsReturn {
  sessions: TerminalSessionInfo[];
  isLoading: boolean;
  error: Error | null;
  refetch: () => Promise<void>;
}

export function useTerminalSessions(): UseTerminalSessionsReturn {
  const [sessions, setSessions] =
    useState<TerminalSessionInfo[]>(DEFAULT_SESSIONS);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<Error | null>(null);
  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const fetchSessions = useCallback(async () => {
    setIsLoading(true);
    setError(null);
    try {
      const data = await listTerminalSessions();
      if (mountedRef.current) {
        setSessions(data.length > 0 ? data : DEFAULT_SESSIONS);
      }
    } catch (err) {
      if (mountedRef.current) {
        setError(err instanceof Error ? err : new Error(String(err)));
      }
    } finally {
      if (mountedRef.current) {
        setIsLoading(false);
      }
    }
  }, []);

  useEffect(() => {
    fetchSessions();
  }, [fetchSessions]);

  return { sessions, isLoading, error, refetch: fetchSessions };
}
