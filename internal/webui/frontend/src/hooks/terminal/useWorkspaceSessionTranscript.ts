import { useEffect, useRef, useState } from "react";

import { getWorkspaceSessionTranscript } from "@/api/terminal";
import type { TranscriptEntry } from "@/types/agent";
import { useWorkspaceContext } from "@/hooks/workspace";

const POLL_INTERVAL_ACTIVE = 3_000;

export interface UseWorkspaceSessionTranscriptResult {
  entries: TranscriptEntry[];
  isLoading: boolean;
  error: Error | null;
}

export function useWorkspaceSessionTranscript(
  sessionId: string | null,
  isActive: boolean,
): UseWorkspaceSessionTranscriptResult {
  const { workspaceId } = useWorkspaceContext();
  const [entries, setEntries] = useState<TranscriptEntry[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;

    if (!sessionId) {
      setEntries([]);
      setError(null);
      return;
    }

    let timer: ReturnType<typeof setInterval> | null = null;

    const fetchTranscript = async () => {
      setIsLoading(true);
      try {
        const result = await getWorkspaceSessionTranscript(
          workspaceId,
          sessionId,
        );
        if (mountedRef.current) {
          setEntries(result);
          setError(null);
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
    };

    void fetchTranscript();

    if (isActive) {
      timer = setInterval(() => {
        void fetchTranscript();
      }, POLL_INTERVAL_ACTIVE);
    }

    return () => {
      mountedRef.current = false;
      if (timer) clearInterval(timer);
    };
  }, [workspaceId, sessionId, isActive]);

  return { entries, isLoading, error };
}
