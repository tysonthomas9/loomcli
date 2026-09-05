/**
 * Workspace-scoped live terminal session count for the NavRail badge.
 *
 * Deliberately independent of the Terminal view being mounted or active: the
 * count comes from the server's per-workspace tab metadata, so it is correct on
 * every route and survives switching workspaces away and back. It counts
 * non-agent tabs whose PTY is attachable (`pty_alive`), i.e. *live sessions in
 * this workspace*, not sockets this browser tab currently holds open.
 *
 * Freshness: refetches on workspace change, on SSE terminal metadata/lifecycle
 * mutations (debounced), every five minutes as a reconnect safety net, and
 * when the document becomes visible. The safety poll pauses while hidden;
 * normal PTY spawn, exit, and kill transitions arrive through SSE.
 */

import {
  useState,
  useCallback,
  useEffect,
  useRef,
  useMemo,
  useContext,
} from "react";

import { QueryRecoveryContext } from "@/hooks/common/queryRecovery";
import { ScopedQueryRequest } from "@/hooks/common/scopedQueryRequest";
import { listTabMetadata } from "@/api/terminal";
import type { MutationPayload } from "@/api/common";
import { isAgentMetadata } from "@/utils/terminalTabMetadata";
import { useEventContext, useEventSubscription } from "@/hooks/common";
import { useWorkspaceContext } from "@/hooks/workspace";

export interface UseWorkspaceSessionCountReturn {
  /** Number of non-agent terminal tabs with a live PTY in the active workspace. */
  sessionCount: number;
  /** Refetch the count from the server. */
  refetch: () => Promise<void>;
}

export interface UseWorkspaceSessionCountOptions {
  enabled?: boolean;
}

const DEBOUNCE_MS = 200;
const POLL_MS = 5 * 60_000;

export function useWorkspaceSessionCount(
  options: UseWorkspaceSessionCountOptions = {},
): UseWorkspaceSessionCountReturn {
  const enabled = options.enabled ?? true;
  const { workspaceId } = useWorkspaceContext();
  const { connectionEpoch } = useEventContext();
  const [sessionCount, setSessionCount] = useState(0);
  const recovery = useContext(QueryRecoveryContext);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const connectionEpochRef = useRef(connectionEpoch);
  const request = useMemo(
    () =>
      enabled
        ? new ScopedQueryRequest({
            load: (signal) => listTabMetadata(workspaceId, { signal }),
            commit: (tabs) =>
              setSessionCount(
                tabs.filter((meta) => !isAgentMetadata(meta) && meta.pty_alive)
                  .length,
              ),
          })
        : null,
    [workspaceId, enabled],
  );

  const fetchCount = useCallback(async () => {
    if (!enabled || !workspaceId || !request) return;
    try {
      await request.run();
    } catch {
      // Ordinary badge refresh is noncritical; recovery uses the strict promise.
    }
  }, [enabled, workspaceId, request]);

  useEffect(() => {
    setSessionCount(0);
  }, [workspaceId]);

  useEffect(() => {
    if (enabled && workspaceId) void fetchCount();
    return () => {
      request?.cancel();
      if (debounceRef.current) clearTimeout(debounceRef.current);
      debounceRef.current = null;
    };
  }, [enabled, workspaceId, fetchCount, request]);

  useEffect(() => {
    if (!enabled || !workspaceId || !recovery || !request) return;
    return recovery.register("workspace-session-count", (signal) =>
      request.run({ signal, fresh: true }),
    );
  }, [enabled, workspaceId, recovery, request]);

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
        fetchCount();
      }, DEBOUNCE_MS);
    },
    [enabled, fetchCount],
  );

  useEventSubscription(handleMutation, {
    types: ["terminal_session_change", "terminal_metadata"],
  });

  useEffect(() => {
    const previous = connectionEpochRef.current;
    connectionEpochRef.current = connectionEpoch;
    if (!enabled || !workspaceId || !request || connectionEpoch <= previous)
      return;
    void request.run({ fresh: true }).catch(() => {});
  }, [connectionEpoch, enabled, workspaceId, request]);

  useEffect(() => {
    if (!enabled) return;
    const interval = setInterval(() => {
      if (!document.hidden) {
        fetchCount();
      }
    }, POLL_MS);
    const onVisibility = () => {
      if (document.visibilityState === "visible") {
        fetchCount();
      }
    };
    document.addEventListener("visibilitychange", onVisibility);
    return () => {
      clearInterval(interval);
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [enabled, fetchCount]);

  return { sessionCount, refetch: fetchCount };
}
