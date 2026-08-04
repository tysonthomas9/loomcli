import { useState, useCallback, useEffect, useRef } from "react";

import { listSessionsByIssue } from "@/api/terminal";
import type { MutationPayload } from "@/api/common";
import { useEventSubscription } from "@/hooks/common";
import { useWorkspaceContext } from "@/hooks/workspace";

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

export interface UseIssueSessionMapOptions {
  enabled?: boolean;
}

const DEBOUNCE_MS = 200;

export function useIssueSessionMap(
  options: UseIssueSessionMapOptions = {},
): UseIssueSessionMapReturn {
  const enabled = options.enabled ?? true;
  const { workspaceId: workspace } = useWorkspaceContext();
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
    if (!enabled) return;
    if (!workspace) return; // Wait until workspace ID is known
    try {
      const data = await listSessionsByIssue(workspace);
      if (mountedRef.current) {
        setIssueSessionMap(data);
      }
    } catch {
      // Silently fail — session map is non-critical UI enhancement
    }
  }, [enabled, workspace]);

  useEffect(() => {
    if (!enabled) return;
    fetchMap();
  }, [enabled, fetchMap]);

  const hasActiveSession = useCallback(
    (issueId: string): boolean => {
      const sessions = issueSessionMap[issueId];
      return sessions !== undefined && sessions.length > 0;
    },
    [issueSessionMap],
  );

  const handleMutation = useCallback(
    (mutation: MutationPayload) => {
      if (!enabled) return;
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
    [enabled, fetchMap],
  );

  useEventSubscription(handleMutation, {
    types: ["terminal_session_change", "terminal_metadata"],
  });

  return {
    issueSessionMap,
    hasActiveSession,
    refetch: fetchMap,
    handleMutation,
  };
}
