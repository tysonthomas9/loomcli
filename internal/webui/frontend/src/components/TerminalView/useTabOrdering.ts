/**
 * useTabOrdering — handles tab pinning, reordering, and close-others logic.
 */

import { useCallback } from "react";

import type { TabState } from "./terminalTabUtils";

interface TabOrderingArgs {
  tabs: TabState[];
  setTabs: React.Dispatch<React.SetStateAction<TabState[]>>;
  setActiveTabId: React.Dispatch<React.SetStateAction<string>>;
  handleTabClose: (tabId: string) => void;
  updatePinned: (session: string, pinned: boolean) => Promise<void>;
  reorderTabMeta: (orderedSessionNames: string[]) => Promise<void>;
  deleteTab: (session: string) => Promise<void>;
}

export function useTabOrdering({
  tabs,
  setTabs,
  setActiveTabId,
  updatePinned,
  reorderTabMeta,
  deleteTab,
}: TabOrderingArgs) {
  const handleTabPin = useCallback(
    (tabId: string, pinned: boolean) => {
      const tab = tabs.find((t) => t.id === tabId);
      if (!tab) return;
      setTabs((prev) => {
        const updated = prev.map((t) =>
          t.id === tabId ? { ...t, pinned } : t,
        );
        return [
          ...updated.filter((t) => t.pinned),
          ...updated.filter((t) => !t.pinned),
        ];
      });
      updatePinned(tab.sessionName, pinned);
    },
    [tabs, updatePinned, setTabs],
  );

  const handleCloseOthers = useCallback(
    (tabId: string) => {
      const others = tabs.filter((t) => t.id !== tabId);
      if (others.length === 0) return;
      setTabs((prev) => prev.filter((t) => t.id === tabId));
      setActiveTabId(tabId);
      for (const t of others) {
        deleteTab(t.sessionName);
      }
    },
    [tabs, deleteTab, setTabs, setActiveTabId],
  );

  const handleReorderTabs = useCallback(
    (orderedTabIds: string[]) => {
      setTabs((prev) => {
        const byId = new Map(prev.map((t) => [t.id, t]));
        return orderedTabIds
          .map((id) => byId.get(id))
          .filter((t): t is TabState => t != null);
      });
      const sessionNames = orderedTabIds
        .map((id) => tabs.find((t) => t.id === id)?.sessionName)
        .filter((n): n is string => n != null);
      reorderTabMeta(sessionNames);
    },
    [tabs, reorderTabMeta, setTabs],
  );

  return { handleTabPin, handleCloseOthers, handleReorderTabs };
}
