import { useState, useCallback, useEffect, useRef } from "react";

import {
  listTabMetadata,
  patchTabMetadata,
  deleteTabMetadata,
} from "@/api/terminal";
import type { TabMetadata } from "@/api/terminal";
import type { MutationPayload } from "@/api/sse";

export interface UseTerminalMetadataReturn {
  tabs: TabMetadata[];
  isLoading: boolean;
  error: Error | null;
  updateLabel: (session: string, label: string) => Promise<void>;
  updateNotes: (session: string, notes: string) => Promise<void>;
  reorderTabs: (orderedSessionNames: string[]) => Promise<void>;
  deleteTab: (session: string) => Promise<void>;
  refetch: () => Promise<void>;
  /** Call this from an SSE onMutation handler to trigger debounced refetch */
  handleMutation: (mutation: MutationPayload) => void;
}

const DEBOUNCE_MS = 100;

export function useTerminalMetadata(): UseTerminalMetadataReturn {
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
      const data = await listTabMetadata();
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
  }, []);

  useEffect(() => {
    fetchTabs();
  }, [fetchTabs]);

  const updateLabel = useCallback(async (session: string, label: string) => {
    let prev: TabMetadata[] = [];
    setTabs((current) => {
      prev = current;
      return current.map((t) =>
        t.session_name === session ? { ...t, label } : t,
      );
    });
    try {
      await patchTabMetadata(session, { label });
    } catch (err) {
      if (mountedRef.current) {
        setTabs(prev);
        setError(err instanceof Error ? err : new Error(String(err)));
      }
    }
  }, []);

  const updateNotes = useCallback(async (session: string, notes: string) => {
    let prev: TabMetadata[] = [];
    setTabs((current) => {
      prev = current;
      return current.map((t) =>
        t.session_name === session ? { ...t, notes } : t,
      );
    });
    try {
      await patchTabMetadata(session, { notes });
    } catch (err) {
      if (mountedRef.current) {
        setTabs(prev);
        setError(err instanceof Error ? err : new Error(String(err)));
      }
    }
  }, []);

  const reorderTabs = useCallback(async (orderedSessionNames: string[]) => {
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
          patchTabMetadata(name, { sort_order: i }),
        ),
      );
    } catch (err) {
      if (mountedRef.current) {
        setTabs(prev);
        setError(err instanceof Error ? err : new Error(String(err)));
      }
    }
  }, []);

  const deleteTab = useCallback(async (session: string) => {
    let prev: TabMetadata[] = [];
    setTabs((current) => {
      prev = current;
      return current.filter((t) => t.session_name !== session);
    });
    try {
      await deleteTabMetadata(session);
    } catch (err) {
      if (mountedRef.current) {
        setTabs(prev);
        setError(err instanceof Error ? err : new Error(String(err)));
      }
    }
  }, []);

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
    updateLabel,
    updateNotes,
    reorderTabs,
    deleteTab,
    refetch: fetchTabs,
    handleMutation,
  };
}
