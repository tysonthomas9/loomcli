/**
 * useSessionTranscript - React hook for fetching transcript entries for a session.
 * Polls every 3s when the session is active; fetches once when inactive.
 */

import { useState, useEffect, useRef } from "react";

import { getSessionTranscript } from "../api/sessions";
import type { TranscriptEntry } from "../types/session";

/** Return type for the useSessionTranscript hook. */
export interface UseSessionTranscriptResult {
  /** Transcript entries for the session. */
  entries: TranscriptEntry[];
  /** Whether a fetch is in progress. */
  isLoading: boolean;
  /** Error from the last fetch, null if successful. */
  error: Error | null;
}

/** Poll interval when session is active (ms). */
const POLL_INTERVAL_ACTIVE = 3_000;

export function useSessionTranscript(
  workspaceId: string,
  taskId: string | null,
  sessionId: string | null,
  isActive: boolean,
): UseSessionTranscriptResult {
  const [entries, setEntries] = useState<TranscriptEntry[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;

    if (!taskId || !sessionId) {
      setEntries([]);
      setError(null);
      return;
    }

    let timer: ReturnType<typeof setTimeout> | null = null;

    const fetchTranscript = async () => {
      setIsLoading(true);
      try {
        const result = await getSessionTranscript(
          workspaceId,
          taskId,
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

    // Poll only when active
    if (isActive) {
      timer = setInterval(() => {
        void fetchTranscript();
      }, POLL_INTERVAL_ACTIVE);
    }

    return () => {
      mountedRef.current = false;
      if (timer) clearInterval(timer);
    };
  }, [workspaceId, taskId, sessionId, isActive]);

  return { entries, isLoading, error };
}
