import { useState, useCallback, useEffect, useRef } from "react";

import {
  listTabMetadata,
  putTabMetadata,
  patchTabMetadata,
  deleteTabMetadata,
  dismissTabRestartNotice,
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
  /**
   * True while a list request is on the wire for the currently requested
   * workspace. `isLoading` stays true after a *failed* load (the stamp is not
   * advanced), so a consumer that wants to distinguish "still trying" from
   * "gave up, offer a retry" reads this alongside `error`.
   */
  isFetching: boolean;
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
  /** Clear a tab's persisted session-replacement marker. */
  dismissRestartNotice: (session: string) => Promise<void>;
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
   * to the workspace this hook is asked about *and* to the workspace the
   * mutation was issued for (`issuedFor`). A mutation that resolves after a
   * workspace switch must neither resurrect the previous workspace's list nor
   * let its rollback overwrite the new workspace's list.
   */
  const updateTabs = useCallback(
    (issuedFor: string, updater: (current: TabMetadata[]) => TabMetadata[]) => {
      setLoaded((current) => {
        if (
          current.workspace === "" ||
          current.workspace !== issuedFor ||
          current.workspace !== requestedWorkspaceRef.current
        ) {
          return current;
        }
        const next = updater(current.tabs);
        return next === current.tabs
          ? current
          : { workspace: current.workspace, tabs: next };
      });
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
    if (requestedWorkspaceRef.current !== requested) {
      // This closure belongs to a workspace the hook has since moved off (e.g.
      // the `refetch` a late 409 reaches for). Touching `isFetching`/`error`
      // here would strand the current workspace in the loading skeleton,
      // because only the matching workspace's fetch resets them again.
      return;
    }
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
      const issued = workspace;
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
        // `replaced_at` is deliberately absent: a tab created just now has
        // never been replaced, and seeding it would flash a restart marker.
        attachable: true,
        attached_clients: 0,
      };
      // `prev` stays null when the optimistic apply was skipped (stale
      // workspace, or no workspace-fresh list held yet). React runs queued
      // state updaters in order, so the rollback below sees the assignment iff
      // the optimistic update really applied — and otherwise leaves the held
      // list alone instead of clobbering it with an empty array.
      let prev: TabMetadata[] | null = null;
      updateTabs(issued, (current) => {
        prev = current;
        return [...current, optimistic];
      });
      try {
        await putTabMetadata(issued, session, {
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
          // server's truth. Only refetch while this is still the requested
          // workspace: `fetchTabs` here closes over the workspace the PUT was
          // issued for, and the current one has already fetched for itself.
          if (requestedWorkspaceRef.current === issued) {
            void fetchTabs();
          }
          return;
        }
        if (mountedRef.current && requestedWorkspaceRef.current === issued) {
          updateTabs(issued, (current) => prev ?? current);
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    },
    [workspace, updateTabs, fetchTabs],
  );

  const updateLabel = useCallback(
    async (session: string, label: string) => {
      const issued = workspace;
      let prev: TabMetadata[] | null = null;
      updateTabs(issued, (current) => {
        prev = current;
        return current.map((t) =>
          t.session_name === session ? { ...t, label } : t,
        );
      });
      try {
        await patchTabMetadata(issued, session, { label });
      } catch (err) {
        if (mountedRef.current && requestedWorkspaceRef.current === issued) {
          updateTabs(issued, (current) => prev ?? current);
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    },
    [workspace, updateTabs],
  );

  const updateNotes = useCallback(
    async (session: string, notes: string) => {
      const issued = workspace;
      let prev: TabMetadata[] | null = null;
      updateTabs(issued, (current) => {
        prev = current;
        return current.map((t) =>
          t.session_name === session ? { ...t, notes } : t,
        );
      });
      try {
        await patchTabMetadata(issued, session, { notes });
      } catch (err) {
        if (mountedRef.current && requestedWorkspaceRef.current === issued) {
          updateTabs(issued, (current) => prev ?? current);
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    },
    [workspace, updateTabs],
  );

  const updatePinned = useCallback(
    async (session: string, pinned: boolean) => {
      const issued = workspace;
      let prev: TabMetadata[] | null = null;
      updateTabs(issued, (current) => {
        prev = current;
        return current.map((t) =>
          t.session_name === session ? { ...t, pinned } : t,
        );
      });
      try {
        await patchTabMetadata(issued, session, { pinned });
      } catch (err) {
        if (mountedRef.current && requestedWorkspaceRef.current === issued) {
          updateTabs(issued, (current) => prev ?? current);
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    },
    [workspace, updateTabs],
  );

  const reorderTabs = useCallback(
    async (orderedSessionNames: string[]) => {
      const issued = workspace;
      let prev: TabMetadata[] | null = null;
      updateTabs(issued, (current) => {
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
            patchTabMetadata(issued, name, { sort_order: i }),
          ),
        );
      } catch (err) {
        if (mountedRef.current && requestedWorkspaceRef.current === issued) {
          updateTabs(issued, (current) => prev ?? current);
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    },
    [workspace, updateTabs],
  );

  const deleteTab = useCallback(
    async (session: string) => {
      const issued = workspace;
      let prev: TabMetadata[] | null = null;
      updateTabs(issued, (current) => {
        prev = current;
        return current.filter((t) => t.session_name !== session);
      });
      try {
        await deleteTabMetadata(issued, session);
      } catch (err) {
        if (mountedRef.current && requestedWorkspaceRef.current === issued) {
          updateTabs(issued, (current) => prev ?? current);
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    },
    [workspace, updateTabs],
  );

  const linkToIssue = useCallback(
    async (session: string, issueId: string) => {
      const issued = workspace;
      let prev: TabMetadata[] | null = null;
      updateTabs(issued, (current) => {
        prev = current;
        return current.map((t) =>
          t.session_name === session ? { ...t, issue_id: issueId } : t,
        );
      });
      try {
        await patchTabMetadata(issued, session, { issue_id: issueId });
      } catch (err) {
        if (mountedRef.current && requestedWorkspaceRef.current === issued) {
          updateTabs(issued, (current) => prev ?? current);
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    },
    [workspace, updateTabs],
  );

  const unlinkFromIssue = useCallback(
    async (session: string) => {
      const issued = workspace;
      let prev: TabMetadata[] | null = null;
      updateTabs(issued, (current) => {
        prev = current;
        return current.map((t) => {
          if (t.session_name !== session) return t;
          const { issue_id: _, ...rest } = t;
          return rest;
        });
      });
      try {
        await patchTabMetadata(issued, session, { issue_id: "" });
      } catch (err) {
        if (mountedRef.current && requestedWorkspaceRef.current === issued) {
          updateTabs(issued, (current) => prev ?? current);
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    },
    [workspace, updateTabs],
  );

  const dismissRestartNotice = useCallback(
    async (session: string) => {
      let prev: TabMetadata[] = [];
      setTabs((current) => {
        prev = current;
        return current.map((t) =>
          t.session_name === session ? { ...t, replaced_at: "" } : t,
        );
      });
      try {
        await dismissTabRestartNotice(workspace, session);
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
    isFetching: enabled && isFetching,
    error,
    createTab,
    updateLabel,
    updateNotes,
    updatePinned,
    reorderTabs,
    deleteTab,
    linkToIssue,
    unlinkFromIssue,
    dismissRestartNotice,
    refetch: fetchTabs,
    handleMutation,
  };
}
