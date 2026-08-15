import { useState, useCallback, useEffect, useRef } from "react";

import {
  listTabMetadata,
  putTabMetadata,
  patchTabMetadata,
  deleteTabMetadata,
} from "@/api/terminal";
import type { TabMetadata } from "@/api/terminal";
import { ApiError, type MutationPayload } from "@/api/common";

export interface UseTerminalMetadataReturn {
  tabs: TabMetadata[];
  isLoading: boolean;
  error: Error | null;
  /**
   * Workspace whose fetch last settled — either a success or the 404/503
   * "metadata storage unavailable" outcome, which is a settled empty list.
   * Null while unsettled. Consumers gate on `loadedFor === workspace` rather
   * than on `!isLoading`, so readiness cannot be read stale in the commit
   * where the workspace (or `enabled`) changes.
   */
  loadedFor: string | null;
  /** True when the last settle was 404/503: metadata storage is not configured. */
  unavailable: boolean;
  createTab: (
    session: string,
    label: string,
    sortOrder: number,
  ) => Promise<void>;
  updateLabel: (session: string, label: string) => Promise<void>;
  updateNotes: (session: string, notes: string) => Promise<void>;
  updatePinned: (session: string, pinned: boolean) => Promise<void>;
  reorderTabs: (orderedSessionNames: string[]) => Promise<void>;
  deleteTab: (session: string) => Promise<void>;
  /**
   * Record a replacement the terminal WebSocket detected live, so the tab
   * metadata carries it immediately instead of only after the next refetch.
   * The server has already persisted it — this is local state catching up,
   * not a write, which is why it is synchronous and issues no request.
   */
  markTabReplaced: (session: string, replacedAt: string) => void;
  linkToIssue: (session: string, issueId: string) => Promise<void>;
  unlinkFromIssue: (session: string) => Promise<void>;
  refetch: () => Promise<void>;
  /** Call this from an SSE onMutation handler to trigger debounced refetch */
  handleMutation: (mutation: MutationPayload) => void;
}

export interface UseTerminalMetadataOptions {
  enabled?: boolean;
}

const DEBOUNCE_MS = 100;

export function useTerminalMetadata(
  workspace: string,
  options: UseTerminalMetadataOptions = {},
): UseTerminalMetadataReturn {
  const enabled = options.enabled ?? true;
  const [tabs, setTabs] = useState<TabMetadata[]>([]);
  const [isLoading, setIsLoading] = useState(Boolean(enabled && workspace));
  const [error, setError] = useState<Error | null>(null);
  const [loadedFor, setLoadedFor] = useState<string | null>(null);
  const [unavailable, setUnavailable] = useState(false);
  const mountedRef = useRef(true);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // The workspace this hook is currently fetching for. "" means "not fetching":
  // either disabled, or the workspace has not resolved yet.
  const fetchKey = enabled ? workspace : "";
  const [syncedKey, setSyncedKey] = useState(fetchKey);
  if (fetchKey !== syncedKey) {
    // React's supported "adjust state during render" pattern: re-renders before
    // children/effects, so consumers never observe the previous key's readiness.
    setSyncedKey(fetchKey);
    setTabs([]);
    setLoadedFor(null);
    setUnavailable(false);
    setError(null);
    setIsLoading(Boolean(enabled && workspace));
  }

  // Mirrors the key so late settle handlers can tell whether their response
  // still belongs to the workspace the hook is on.
  const keyRef = useRef(fetchKey);
  keyRef.current = fetchKey;

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      if (debounceRef.current) {
        clearTimeout(debounceRef.current);
      }
    };
  }, []);

  const fetchTabs = useCallback(async () => {
    if (!enabled) {
      return;
    }
    if (!workspace) {
      // Workspace not resolved yet — stay unsettled (loadedFor null) so
      // downstream consumers (e.g. useTabInit) don't see "ready with zero
      // tabs" and auto-create a default, locking out the real tab list that
      // arrives when workspace resolves.
      return;
    }
    setIsLoading(true);
    setError(null);
    // A settle only counts if the hook is still mounted AND still on the
    // workspace this call was issued for; a late response for a superseded
    // workspace must not mark the new one ready.
    const settleable = () => mountedRef.current && keyRef.current === workspace;
    try {
      const data = await listTabMetadata(workspace);
      if (settleable()) {
        setTabs(data);
        setUnavailable(false);
        setLoadedFor(workspace);
      }
    } catch (err) {
      if (!settleable()) return;
      if (
        err instanceof ApiError &&
        (err.status === 404 || err.status === 503)
      ) {
        // Metadata storage is not configured (404) or is down (503). That is a
        // supported degraded mode, and a settled empty list — not a failure.
        setTabs([]);
        setUnavailable(true);
        setLoadedFor(workspace);
      } else {
        // A genuine failure leaves loadedFor null: no tabs are invented.
        setError(err instanceof Error ? err : new Error(String(err)));
      }
    } finally {
      if (settleable()) {
        setIsLoading(false);
      }
    }
  }, [enabled, workspace]);

  // Fetch when the workspace (or enabled) changes. The state reset for the new
  // key already happened during render, above.
  useEffect(() => {
    if (!enabled) {
      if (debounceRef.current) {
        clearTimeout(debounceRef.current);
        debounceRef.current = null;
      }
      return;
    }
    fetchTabs();
  }, [enabled, fetchTabs]);

  const createTab = useCallback(
    async (session: string, label: string, sortOrder: number) => {
      const now = new Date().toISOString();
      const optimistic: TabMetadata = {
        session_name: session,
        label,
        notes: "",
        sort_order: sortOrder,
        pinned: false,
        created_at: now,
        updated_at: now,
        // Optimistic; next ListTabs refresh returns the server's truth.
        attachable: true,
        attached_clients: 0,
      };
      let prev: TabMetadata[] = [];
      setTabs((current) => {
        prev = current;
        return [...current, optimistic];
      });
      try {
        await putTabMetadata(workspace, session, {
          session_name: session,
          label,
          sort_order: sortOrder,
          notes: "",
          pinned: false,
        });
      } catch (err) {
        if (mountedRef.current) {
          setTabs(prev);
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    },
    [workspace],
  );

  const updateLabel = useCallback(
    async (session: string, label: string) => {
      let prev: TabMetadata[] = [];
      setTabs((current) => {
        prev = current;
        return current.map((t) =>
          t.session_name === session ? { ...t, label } : t,
        );
      });
      try {
        await patchTabMetadata(workspace, session, { label });
      } catch (err) {
        if (mountedRef.current) {
          setTabs(prev);
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    },
    [workspace],
  );

  const updateNotes = useCallback(
    async (session: string, notes: string) => {
      let prev: TabMetadata[] = [];
      setTabs((current) => {
        prev = current;
        return current.map((t) =>
          t.session_name === session ? { ...t, notes } : t,
        );
      });
      try {
        await patchTabMetadata(workspace, session, { notes });
      } catch (err) {
        if (mountedRef.current) {
          setTabs(prev);
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    },
    [workspace],
  );

  const updatePinned = useCallback(
    async (session: string, pinned: boolean) => {
      let prev: TabMetadata[] = [];
      setTabs((current) => {
        prev = current;
        return current.map((t) =>
          t.session_name === session ? { ...t, pinned } : t,
        );
      });
      try {
        await patchTabMetadata(workspace, session, { pinned });
      } catch (err) {
        if (mountedRef.current) {
          setTabs(prev);
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    },
    [workspace],
  );

  const reorderTabs = useCallback(
    async (orderedSessionNames: string[]) => {
      let prev: TabMetadata[] = [];
      setTabs((current) => {
        prev = current;
        const byName = new Map(current.map((t) => [t.session_name, t]));
        return orderedSessionNames
          .map((name, i) => {
            const tab = byName.get(name);
            if (!tab) return null;
            return { ...tab, sort_order: i };
          })
          .filter((t): t is TabMetadata => t !== null);
      });

      try {
        await Promise.all(
          orderedSessionNames.map((name, i) =>
            patchTabMetadata(workspace, name, { sort_order: i }),
          ),
        );
      } catch (err) {
        if (mountedRef.current) {
          setTabs(prev);
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    },
    [workspace],
  );

  const deleteTab = useCallback(
    async (session: string) => {
      let prev: TabMetadata[] = [];
      setTabs((current) => {
        prev = current;
        return current.filter((t) => t.session_name !== session);
      });
      try {
        await deleteTabMetadata(workspace, session);
      } catch (err) {
        if (mountedRef.current) {
          setTabs(prev);
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    },
    [workspace],
  );

  const markTabReplaced = useCallback((session: string, replacedAt: string) => {
    setTabs((current) =>
      current.map((t) =>
        t.session_name === session && t.replaced_at !== replacedAt
          ? { ...t, replaced_at: replacedAt, replaced_reason: "server_restart" }
          : t,
      ),
    );
  }, []);

  const linkToIssue = useCallback(
    async (session: string, issueId: string) => {
      let prev: TabMetadata[] = [];
      setTabs((current) => {
        prev = current;
        return current.map((t) =>
          t.session_name === session ? { ...t, issue_id: issueId } : t,
        );
      });
      try {
        await patchTabMetadata(workspace, session, { issue_id: issueId });
      } catch (err) {
        if (mountedRef.current) {
          setTabs(prev);
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    },
    [workspace],
  );

  const unlinkFromIssue = useCallback(
    async (session: string) => {
      let prev: TabMetadata[] = [];
      setTabs((current) => {
        prev = current;
        return current.map((t) => {
          if (t.session_name !== session) return t;
          const { issue_id: _, ...rest } = t;
          return rest;
        });
      });
      try {
        await patchTabMetadata(workspace, session, { issue_id: "" });
      } catch (err) {
        if (mountedRef.current) {
          setTabs(prev);
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    },
    [workspace],
  );

  const handleMutation = useCallback(
    (mutation: MutationPayload) => {
      if (!enabled) return;
      if (mutation.type !== "terminal_metadata") return;
      // Debounce refetch to collapse multiple rapid events (e.g., reorder)
      if (debounceRef.current) {
        clearTimeout(debounceRef.current);
      }
      debounceRef.current = setTimeout(() => {
        fetchTabs();
      }, DEBOUNCE_MS);
    },
    [enabled, fetchTabs],
  );

  return {
    tabs,
    isLoading,
    error,
    loadedFor,
    unavailable,
    createTab,
    updateLabel,
    updateNotes,
    updatePinned,
    reorderTabs,
    deleteTab,
    markTabReplaced,
    linkToIssue,
    unlinkFromIssue,
    refetch: fetchTabs,
    handleMutation,
  };
}
