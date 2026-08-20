import { useState, useCallback, useEffect, useRef } from "react";

import {
  listTabMetadata,
  putTabMetadata,
  patchTabMetadata,
  deleteTabMetadata,
} from "@/api/terminal";
import type { TabMetadata } from "@/api/terminal";
import type { MutationPayload } from "@/api/common";
import { ApiError } from "@/types/common";

export interface UseTerminalMetadataReturn {
  /**
   * Tab metadata for the workspace this hook is currently asked about, and
   * nothing else. While a fetch for a newly requested workspace is still in
   * flight this is empty rather than the previous workspace's list.
   */
  tabs: TabMetadata[];
  /**
   * True until `tabs` is authoritative for the currently requested workspace.
   * A consumer that sees `isLoading === false` may treat `tabs` as the truth
   * for that workspace — in particular, an empty `tabs` then really means
   * "this workspace has no terminal tabs" (see PUPPET-125).
   */
  isLoading: boolean;
  error: Error | null;
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

/**
 * Single module-level empty list so `tabs` keeps a stable identity across
 * renders while no workspace-fresh data is held. `useTabInit` compares
 * `tabMetadata` by reference (and lists it in an effect dep array), so a fresh
 * `[]` per render would retrigger it needlessly.
 */
const EMPTY_TABS: TabMetadata[] = [];

/** Tab metadata together with the workspace it was fetched for. */
interface LoadedTabs {
  workspace: string;
  tabs: TabMetadata[];
}

export function useTerminalMetadata(
  workspace: string,
  options: UseTerminalMetadataOptions = {},
): UseTerminalMetadataReturn {
  const enabled = options.enabled ?? true;
  const [loaded, setLoaded] = useState<LoadedTabs>({
    workspace: "",
    tabs: EMPTY_TABS,
  });
  const [isFetching, setIsFetching] = useState(false);
  const [error, setError] = useState<Error | null>(null);
  const mountedRef = useRef(true);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  // The workspace the latest effect run asked for. Fetch resolutions compare
  // against it so an out-of-order response cannot be stamped onto the wrong
  // workspace.
  const requestedWorkspaceRef = useRef("");

  // A stamp of "" never matches: workspace === "" means "not resolved yet",
  // and the hook must stay in the loading state there (see fetchTabs below).
  const isFresh = workspace !== "" && loaded.workspace === workspace;
  const tabs = isFresh ? loaded.tabs : EMPTY_TABS;
  const isLoading = enabled && (!isFresh || isFetching);

  /**
   * Apply an optimistic mutation, but only while the held data still belongs
   * to the workspace this hook is asked about. A mutation that resolves after
   * a workspace switch must not resurrect the previous workspace's list.
   */
  const updateTabs = useCallback(
    (updater: (current: TabMetadata[]) => TabMetadata[]) => {
      setLoaded((current) =>
        current.workspace === requestedWorkspaceRef.current &&
        current.workspace !== ""
          ? { workspace: current.workspace, tabs: updater(current.tabs) }
          : current,
      );
    },
    [],
  );

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
      // Workspace not resolved yet — stay in the loading state so
      // downstream consumers (e.g. useTabInit) don't see
      // "ready with zero tabs" and auto-create a default, locking out
      // the real tab list that arrives when workspace resolves.
      return;
    }
    const requested = workspace;
    setIsFetching(true);
    setError(null);
    try {
      const data = await listTabMetadata(requested);
      if (mountedRef.current && requestedWorkspaceRef.current === requested) {
        setLoaded({ workspace: requested, tabs: data });
      }
    } catch (err) {
      if (mountedRef.current && requestedWorkspaceRef.current === requested) {
        // The stamp is deliberately NOT advanced: a failed load must leave the
        // hook loading rather than report "this workspace has zero tabs",
        // which would make useTabInit manufacture a terminal session.
        setError(err instanceof Error ? err : new Error(String(err)));
      }
    } finally {
      if (mountedRef.current && requestedWorkspaceRef.current === requested) {
        setIsFetching(false);
      }
    }
  }, [enabled, workspace]);

  // Re-fetch when workspace changes, or when the hook is re-enabled.
  useEffect(() => {
    if (requestedWorkspaceRef.current !== workspace) {
      requestedWorkspaceRef.current = workspace;
      // A failure in the previous workspace must not persist into this one.
      setError(null);
      setIsFetching(false);
    }
    if (!enabled) {
      if (debounceRef.current) {
        clearTimeout(debounceRef.current);
        debounceRef.current = null;
      }
      return;
    }
    // No setLoaded([]) here: staleness is expressed by the workspace stamp, so
    // clearing is implicit and a workspace whose data is already correct is
    // spared a gratuitous empty-list render.
    fetchTabs();
  }, [enabled, workspace, fetchTabs]);

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
        pty_alive: true,
        attached_clients: 0,
      };
      let prev: TabMetadata[] = [];
      updateTabs((current) => {
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
        if (err instanceof ApiError && err.status === 409) {
          // The session already exists server-side with a live PTY — another
          // browser tab created it, or this PUT lost the race with the first
          // WS attach. Not an error: keep the optimistic tab and adopt the
          // server's truth. fetchTabs no-ops on a stale workspace and guards
          // mountedRef itself.
          void fetchTabs();
          return;
        }
        if (mountedRef.current) {
          updateTabs(() => prev);
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    },
    [workspace, updateTabs, fetchTabs],
  );

  const updateLabel = useCallback(
    async (session: string, label: string) => {
      let prev: TabMetadata[] = [];
      updateTabs((current) => {
        prev = current;
        return current.map((t) =>
          t.session_name === session ? { ...t, label } : t,
        );
      });
      try {
        await patchTabMetadata(workspace, session, { label });
      } catch (err) {
        if (mountedRef.current) {
          updateTabs(() => prev);
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    },
    [workspace, updateTabs],
  );

  const updateNotes = useCallback(
    async (session: string, notes: string) => {
      let prev: TabMetadata[] = [];
      updateTabs((current) => {
        prev = current;
        return current.map((t) =>
          t.session_name === session ? { ...t, notes } : t,
        );
      });
      try {
        await patchTabMetadata(workspace, session, { notes });
      } catch (err) {
        if (mountedRef.current) {
          updateTabs(() => prev);
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    },
    [workspace, updateTabs],
  );

  const updatePinned = useCallback(
    async (session: string, pinned: boolean) => {
      let prev: TabMetadata[] = [];
      updateTabs((current) => {
        prev = current;
        return current.map((t) =>
          t.session_name === session ? { ...t, pinned } : t,
        );
      });
      try {
        await patchTabMetadata(workspace, session, { pinned });
      } catch (err) {
        if (mountedRef.current) {
          updateTabs(() => prev);
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    },
    [workspace, updateTabs],
  );

  const reorderTabs = useCallback(
    async (orderedSessionNames: string[]) => {
      let prev: TabMetadata[] = [];
      updateTabs((current) => {
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
          updateTabs(() => prev);
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    },
    [workspace, updateTabs],
  );

  const deleteTab = useCallback(
    async (session: string) => {
      let prev: TabMetadata[] = [];
      updateTabs((current) => {
        prev = current;
        return current.filter((t) => t.session_name !== session);
      });
      try {
        await deleteTabMetadata(workspace, session);
      } catch (err) {
        if (mountedRef.current) {
          updateTabs(() => prev);
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    },
    [workspace, updateTabs],
  );

  const linkToIssue = useCallback(
    async (session: string, issueId: string) => {
      let prev: TabMetadata[] = [];
      updateTabs((current) => {
        prev = current;
        return current.map((t) =>
          t.session_name === session ? { ...t, issue_id: issueId } : t,
        );
      });
      try {
        await patchTabMetadata(workspace, session, { issue_id: issueId });
      } catch (err) {
        if (mountedRef.current) {
          updateTabs(() => prev);
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    },
    [workspace, updateTabs],
  );

  const unlinkFromIssue = useCallback(
    async (session: string) => {
      let prev: TabMetadata[] = [];
      updateTabs((current) => {
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
          updateTabs(() => prev);
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    },
    [workspace, updateTabs],
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
    createTab,
    updateLabel,
    updateNotes,
    updatePinned,
    reorderTabs,
    deleteTab,
    linkToIssue,
    unlinkFromIssue,
    refetch: fetchTabs,
    handleMutation,
  };
}
