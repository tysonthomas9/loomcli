import { useEffect, useState } from "react";

import {
  getWorkspaceSessionSubagentTranscript,
  listWorkspaceSessionSubagents,
} from "@/api/terminal";
import type { TranscriptEntry } from "@/types/agent";
import { useWorkspaceContext } from "@/hooks/workspace";

export interface UseWorkspaceSessionSubagentsResult {
  subagentIds: string[];
  isLoading: boolean;
  error: Error | null;
}

export interface UseWorkspaceSubagentTranscriptResult {
  entries: TranscriptEntry[];
  isLoading: boolean;
  error: Error | null;
}

export function useWorkspaceSessionSubagents(
  sessionId: string | null,
  enabled: boolean,
): UseWorkspaceSessionSubagentsResult {
  const { workspaceId } = useWorkspaceContext();
  const [subagentIds, setSubagentIds] = useState<string[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  useEffect(() => {
    if (!enabled || !sessionId) {
      setSubagentIds([]);
      setError(null);
      return;
    }

    let cancelled = false;
    setIsLoading(true);

    const run = async () => {
      try {
        const result = await listWorkspaceSessionSubagents(
          workspaceId,
          sessionId,
        );
        if (cancelled) return;
        setSubagentIds(result);
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

  return { subagentIds, isLoading, error };
}

export function useWorkspaceSubagentTranscript(
  sessionId: string | null,
  subagentId: string | null,
  enabled: boolean,
): UseWorkspaceSubagentTranscriptResult {
  const { workspaceId } = useWorkspaceContext();
  const [entries, setEntries] = useState<TranscriptEntry[]>([]);
  const [isLoading, setIsLoading] = useState(false);
  const [error, setError] = useState<Error | null>(null);

  useEffect(() => {
    if (!enabled || !sessionId || !subagentId) {
      setEntries([]);
      setError(null);
      return;
    }

    let cancelled = false;
    setIsLoading(true);

    const run = async () => {
      try {
        const result = await getWorkspaceSessionSubagentTranscript(
          workspaceId,
          sessionId,
          subagentId,
        );
        if (cancelled) return;
        setEntries(result);
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
  }, [workspaceId, sessionId, subagentId, enabled]);

  return { entries, isLoading, error };
}
