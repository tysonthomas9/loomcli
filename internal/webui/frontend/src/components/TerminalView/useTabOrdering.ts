/**
 * useTabOrdering hook.
 * Provides tab management operations: close others, close all, reorder.
 * Pinned tabs are protected from mass-close operations.
 */

import { useCallback } from "react";

export interface TabState {
  id: string;
  label: string;
  sessionName: string;
  pinned?: boolean;
}

export interface UseTabOrderingOptions {
  tabs: TabState[];
  setTabs: React.Dispatch<React.SetStateAction<TabState[]>>;
  activeTabId: string;
  setActiveTabId: (id: string) => void;
  deleteTab?: (sessionName: string) => void;
}

export interface UseTabOrderingReturn {
  handleCloseOthers: (tabId: string) => void;
  handleCloseAll: () => void;
}

/**
 * Hook for managing tab ordering and mass-close operations.
 * Respects pinned tabs: they are not removed by handleCloseOthers.
 */
export function useTabOrdering({
  tabs,
  setTabs,
  setActiveTabId,
  deleteTab,
}: UseTabOrderingOptions): UseTabOrderingReturn {
  const handleCloseOthers = useCallback(
    (tabId: string) => {
      const others = tabs.filter((t) => t.id !== tabId && !t.pinned);
      if (others.length === 0) return;

      for (const tab of others) {
        deleteTab?.(tab.sessionName);
      }

      setTabs((prev) => prev.filter((t) => t.id === tabId || t.pinned));
      setActiveTabId(tabId);
    },
    [tabs, setTabs, setActiveTabId, deleteTab],
  );

  const handleCloseAll = useCallback(() => {
    const unpinned = tabs.filter((t) => !t.pinned);

    for (const tab of unpinned) {
      deleteTab?.(tab.sessionName);
    }

    setTabs((prev) => prev.filter((t) => t.pinned));

    const firstPinned = tabs.find((t) => t.pinned);
    if (firstPinned) {
      setActiveTabId(firstPinned.id);
    }
  }, [tabs, setTabs, setActiveTabId, deleteTab]);

  return { handleCloseOthers, handleCloseAll };
}
