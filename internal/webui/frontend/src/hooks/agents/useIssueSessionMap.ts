import {
  useState,
  useCallback,
  useEffect,
  useRef,
  useMemo,
  useContext,
} from "react";

import { listSessionsByIssue } from "@/api/terminal";
import type { MutationPayload } from "@/api/common";
import { useEventSubscription, useEventContext } from "@/hooks/common";
import { useWorkspaceContext } from "@/hooks/workspace";
import { QueryRecoveryContext } from "@/hooks/common/queryRecovery";
import { ScopedQueryRequest } from "@/utils/scopedQueryRequest";

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
  const { connectionEpoch } = useEventContext();
  const recovery = useContext(QueryRecoveryContext);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const request = useMemo(
    () =>
      new ScopedQueryRequest({
        load: (signal) => {
          if (!enabled || !workspace)
            return Promise.reject(new Error("Session map is disabled"));
          return listSessionsByIssue(workspace, { signal });
        },
        commit: setIssueSessionMap,
      }),
    [workspace, enabled],
  );

  useEffect(() => {
    setIssueSessionMap({});
    return () => {
      request.cancel();
      if (debounceRef.current) clearTimeout(debounceRef.current);
      debounceRef.current = null;
    };
  }, [request]);

  const fetchMap = useCallback(async () => {
    if (!enabled || !workspace) return;
    // Ordinary session badges remain a non-critical UI enhancement.
    await request.run().catch(() => {});
  }, [enabled, workspace, request]);

  useEffect(() => {
    if (!enabled || !workspace) return;
    void request.run({ fresh: true }).catch(() => {});
  }, [enabled, workspace, request, connectionEpoch]);

  useEffect(() => {
    if (!enabled || !workspace || !recovery) return;
    return recovery.register("issue session map", (signal) =>
      request.run({ signal, fresh: true }),
    );
  }, [enabled, workspace, recovery, request]);

  const hasActiveSession = useCallback(
    (issueId: string): boolean => {
      const sessions = issueSessionMap[issueId];
      return sessions !== undefined && sessions.length > 0;
    },
    [issueSessionMap],
  );

  const handleMutation = useCallback(
    (mutation: MutationPayload) => {
      if (!enabled || !workspace) return;
      if (mutation.workspace_id && mutation.workspace_id !== workspace) return;
      if (
        mutation.type !== "terminal_session_change" &&
        mutation.type !== "terminal_metadata" &&
        mutation.type !== "refresh"
      )
        return;
      if (debounceRef.current) {
        clearTimeout(debounceRef.current);
      }
      debounceRef.current = setTimeout(() => {
        debounceRef.current = null;
        void fetchMap();
      }, DEBOUNCE_MS);
    },
    [enabled, workspace, fetchMap],
  );

  useEventSubscription(handleMutation, {
    types: ["terminal_session_change", "terminal_metadata", "refresh"],
  });

  return {
    issueSessionMap,
    hasActiveSession,
    refetch: fetchMap,
    handleMutation,
  };
}
