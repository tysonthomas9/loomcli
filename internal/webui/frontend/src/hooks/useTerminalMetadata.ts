import { useState, useCallback, useEffect, useRef } from "react";

import {
  listTabMetadata,
  putTabMetadata,
  patchTabMetadata,
  deleteTabMetadata,
} from "@/api/terminal";
import type { TabMetadata } from "@/api/terminal";
import type { MutationPayload } from "@/api/sse";

export interface UseTerminalMetadataReturn {
  tabs: TabMetadata[];
  isLoading: boolean;
  error: Error | null;
  createTab: (
    session: string,
    label: string,
    sortOrder: number,
  ) => Promise<void>;
  updateLabel: (session: string, label: string) => Promise<void>;
  updateNotes: (session: string, notes: string) => Promise<void>;
  reorderTabs: (orderedSessionNames: string[]) => Promise<void>;
  deleteTab: (session: string) => Promise<void>;
  linkToIssue: (session: string, issueId: string) => Promise<void>;
  unlinkFromIssue: (session: string) => Promise<void>;
  refetch: () => Promise<void>;
  /** Call this from an SSE onMutation handler to trigger debounced refetch */
  handleMutation: (mutation: MutationPayload) => void;
}

const DEBOUNCE_MS = 100;

export function useTerminalMetadata(
  workspace?: string,
): UseTerminalMetadataReturn {
  const [tabs, setTabs] = useState<TabMetadata[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<Error | null>(null);
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

  const fetchTabs = useCallback(async () => {
    setIsLoading(true);
    setError(null);
    try {
      const data = await listTabMetadata(workspace);
      if (mountedRef.current) {
        setTabs(data);
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
  }, [workspace]);

  // Re-fetch when workspace changes
  useEffect(() => {
    setTabs([]);
    setIsLoading(true);
    fetchTabs();
  }, [fetchTabs]);

  const createTab = useCallback(
    async (session: string, label: string, sortOrder: number) => {
      const now = new Date().toISOString();
      const optimistic: TabMetadata = {
        session_name: session,
        label,
        notes: "",
        sort_order: sortOrder,
        created_at: now,
        updated_at: now,
      };
      let prev: TabMetadata[] = [];
      setTabs((current) => {
        prev = current;
        return [...current, optimistic];
      });
      try {
        await putTabMetadata(
          session,
          { label, sort_order: sortOrder },
          workspace,
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
        await patchTabMetadata(session, { label }, workspace);
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
        await patchTabMetadata(session, { notes }, workspace);
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
            patchTabMetadata(name, { sort_order: i }, workspace),
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
        await deleteTabMetadata(session, workspace);
      } catch (err) {
        if (mountedRef.current) {
          setTabs(prev);
          setError(err instanceof Error ? err : new Error(String(err)));
        }
      }
    },
    [workspace],
  );

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
        await patchTabMetadata(session, { issue_id: issueId }, workspace);
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
        await patchTabMetadata(session, { issue_id: "" }, workspace);
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
      if (mutation.type !== "terminal_metadata") return;
      // Debounce refetch to collapse multiple rapid events (e.g., reorder)
      if (debounceRef.current) {
        clearTimeout(debounceRef.current);
      }
      debounceRef.current = setTimeout(() => {
        fetchTabs();
      }, DEBOUNCE_MS);
    },
    [fetchTabs],
  );

  return {
    tabs,
    isLoading,
    error,
    createTab,
    updateLabel,
    updateNotes,
    reorderTabs,
    deleteTab,
    linkToIssue,
    unlinkFromIssue,
    refetch: fetchTabs,
    handleMutation,
  };
}
