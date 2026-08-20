/**
 * Workspace-scoped live terminal session count for the NavRail badge.
 *
 * Deliberately independent of the Terminal view being mounted or active: the
 * count comes from the server's per-workspace tab metadata, so it is correct on
 * every route and survives switching workspaces away and back. It counts
 * non-agent tabs whose PTY is attachable (`pty_alive`), i.e. *live sessions in
 * this workspace*, not sockets this browser tab currently holds open.
 *
 * Freshness: refetches on workspace change, on SSE terminal mutations
 * (debounced), every 30s, and when the document becomes visible. The slow poll
 * exists because a PTY that exits on its own emits no SSE event, so that case
 * corrects within one poll interval (or immediately on tab focus).
 */

import { useState, useCallback, useEffect, useRef } from "react";

import { listTabMetadata } from "@/api/terminal";
import type { MutationPayload } from "@/api/common";
import { isAgentMetadata } from "@/utils/terminalTabMetadata";
import { useEventSubscription } from "@/hooks/common";
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
const POLL_MS = 30000;

export function useWorkspaceSessionCount(
  options: UseWorkspaceSessionCountOptions = {},
): UseWorkspaceSessionCountReturn {
  const enabled = options.enabled ?? true;
  const { workspaceId } = useWorkspaceContext();
  const [sessionCount, setSessionCount] = useState(0);
  const mountedRef = useRef(true);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  // Workspace the newest request was issued for; responses for anything else
  // are stale and must not overwrite the current count.
  const currentWorkspaceRef = useRef(workspaceId);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      if (debounceRef.current) {
        clearTimeout(debounceRef.current);
      }
    };
  }, []);

  const fetchCount = useCallback(async () => {
    if (!enabled) return;
    if (!workspaceId) return; // Wait until the route workspace ID is known
    try {
      const tabs = await listTabMetadata(workspaceId);
      if (!mountedRef.current) return;
      if (currentWorkspaceRef.current !== workspaceId) return; // stale response
      setSessionCount(
        tabs.filter((meta) => !isAgentMetadata(meta) && meta.pty_alive).length,
      );
    } catch {
      // Silently fail — the badge is a non-critical UI enhancement, and keeping
      // the last known count beats flashing to 0 on a transient failure.
    }
  }, [enabled, workspaceId]);

  // Reset synchronously on a workspace change so the badge never shows the
  // previous workspace's count while the new fetch is in flight.
  useEffect(() => {
    currentWorkspaceRef.current = workspaceId;
    setSessionCount(0);
  }, [workspaceId]);

  useEffect(() => {
    if (!enabled) return;
    fetchCount();
  }, [enabled, fetchCount]);

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
    if (!enabled) return;
    const interval = setInterval(() => {
      fetchCount();
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
