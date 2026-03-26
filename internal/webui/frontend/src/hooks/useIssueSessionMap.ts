import { useState, useCallback, useEffect, useRef } from "react";

import { listSessionsByIssue } from "@/api/terminal";
import type { MutationPayload } from "@/api/sse";
import { useSSE } from "@/hooks/useSSE";

export interface UseIssueSessionMapReturn {
  /** Map of issue_id to session_name[] */
  issueSessionMap: Record<string, string[]>;
  /** Check if an issue has at least one active session */
  hasActiveSession: (issueId: string) => boolean;
  /** Refetch the session map from the server */
  refetch: () => Promise<void>;
  /** Call from SSE onMutation handler to trigger debounced refetch */
  handleMutation: (mutation: MutationPayload) => void;
}

const DEBOUNCE_MS = 200;

export function useIssueSessionMap(
  workspace: string,
): UseIssueSessionMapReturn {
  const [issueSessionMap, setIssueSessionMap] = useState<
    Record<string, string[]>
  >({});
  const mountedRef = useRef(true);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      if (debounceRef.current) {
        clearTimeout(debounceRef.current);
      }
    };
  }, []);

  const fetchMap = useCallback(async () => {
    if (!workspace) return; // Wait until workspace ID is known
    try {
      const data = await listSessionsByIssue(workspace);
      if (mountedRef.current) {
        setIssueSessionMap(data);
      }
    } catch {
      // Silently fail — session map is non-critical UI enhancement
    }
  }, [workspace]);

  useEffect(() => {
    fetchMap();
  }, [fetchMap]);

  const hasActiveSession = useCallback(
    (issueId: string): boolean => {
      const sessions = issueSessionMap[issueId];
      return sessions !== undefined && sessions.length > 0;
    },
    [issueSessionMap],
  );

  const handleMutation = useCallback(
    (mutation: MutationPayload) => {
      if (
        mutation.type !== "terminal_session_change" &&
        mutation.type !== "terminal_metadata"
      )
        return;
      if (debounceRef.current) {
        clearTimeout(debounceRef.current);
      }
      debounceRef.current = setTimeout(() => {
        fetchMap();
      }, DEBOUNCE_MS);
    },
    [fetchMap],
  );

  useSSE({ workspaceId: workspace, onMutation: handleMutation });

  return {
    issueSessionMap,
    hasActiveSession,
    refetch: fetchMap,
    handleMutation,
  };
}
