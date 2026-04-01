/**
 * useTabActions hook.
 * Provides tab close, duplicate, and rename handlers with metadata persistence.
 */

import {
  useCallback,
  type Dispatch,
  type SetStateAction,
  type MutableRefObject,
} from "react";

import { scheduleSessionKill } from "@/hooks/api";

import type {
  ConnectionState,
  TerminalInstanceHandle,
} from "@/components/TerminalView/instances";
import { type TabState, getNextDuplicateName } from "./terminalTabUtils";

interface UseTabActionsOptions {
  tabs: TabState[];
  setTabs: Dispatch<SetStateAction<TabState[]>>;
  setActiveTabId: Dispatch<SetStateAction<string>>;
  activeTabIdRef: MutableRefObject<string>;
  instanceRefs: MutableRefObject<Map<string, TerminalInstanceHandle>>;
  createTab: (
    session: string,
    label: string,
    sortOrder: number,
  ) => Promise<void>;
  updateLabel: (session: string, label: string) => Promise<void>;
  deleteTab: (session: string) => Promise<void>;
}

interface UseTabActionsReturn {
  handleTabClose: (tabId: string) => void;
  handleDuplicateTab: (tabId: string) => void;
  handleTabRename: (tabId: string, newLabel: string) => void;
}

export function useTabActions({
  tabs,
  setTabs,
  setActiveTabId,
  activeTabIdRef,
  instanceRefs,
  createTab,
  updateLabel,
  deleteTab,
}: UseTabActionsOptions): UseTabActionsReturn {
  const handleTabClose = useCallback(
    (tabId: string) => {
      const sourceTab = tabs.find((t) => t.id === tabId);
      if (!sourceTab || tabs.length <= 1) return;
      const sessionNameToDelete = sourceTab.sessionName;

      // Get handle before removing from refs — we need it for disconnect().
      const handle = instanceRefs.current.get(tabId);

      // Immediately update UI — tab disappears.
      setTabs((prev) => {
        if (prev.length <= 1) return prev;
        const idx = prev.findIndex((t) => t.id === tabId);
        if (idx === -1) return prev;
        const next = prev.filter((t) => t.id !== tabId);

        if (tabId === activeTabIdRef.current) {
          const newActiveIdx = idx > 0 ? idx - 1 : 0;
          const newActive = next[newActiveIdx];
          if (newActive) {
            setActiveTabId(newActive.id);
          }
        }

        return next;
      });
      instanceRefs.current.delete(tabId);

      // Delete tab metadata from Redis.
      deleteTab(sessionNameToDelete).catch((err) =>
        console.error(
          `Failed to delete tab metadata ${sessionNameToDelete}:`,
          err,
        ),
      );

      // Gracefully disconnect WS, then kill the tmux session.
      // disconnect() sets beingKilledRef to block reconnect, closes the WS,
      // and resolves when ws.onclose fires (or after 2s timeout).
      // The backend tombstone (killingSet) prevents any stray reconnect from
      // recreating the session.
      const doKill = () =>
        scheduleSessionKill("", sessionNameToDelete, true).catch((err) =>
          console.error(`Failed to kill session ${sessionNameToDelete}:`, err),
        );
      if (handle?.disconnect) {
        handle.disconnect().then(doKill);
      } else {
        doKill();
      }
    },
    [deleteTab, tabs, setTabs, setActiveTabId, activeTabIdRef, instanceRefs],
  );

  const handleDuplicateTab = useCallback(
    (tabId: string) => {
      const sourceTab = tabs.find((t) => t.id === tabId);
      if (!sourceTab) return;
      const result = getNextDuplicateName(sourceTab.label, tabs);
      if (!result) return; // MAX_TABS reached
      const newTab: TabState = {
        id: result.sessionName,
        label: result.label,
        sessionName: result.sessionName,
        connectionState: "disconnected" as ConnectionState,
        backendName: sourceTab.backendName,
      };
      createTab(result.sessionName, result.label, tabs.length).catch((err) =>
        console.error(
          `Failed to persist duplicated tab ${result.sessionName}:`,
          err,
        ),
      );
      setTabs((prev) => [...prev, newTab]);
      setActiveTabId(newTab.id);
    },
    [createTab, tabs, setTabs, setActiveTabId],
  );

  const handleTabRename = useCallback(
    (tabId: string, newLabel: string) => {
      const tab = tabs.find((t) => t.id === tabId);
      if (!tab) return;
      // Check uniqueness: if another tab already has this label, append counter
      const otherLabels = tabs
        .filter((t) => t.id !== tabId)
        .map((t) => t.label);
      let finalLabel = newLabel;
      if (otherLabels.includes(finalLabel)) {
        let counter = 2;
        while (otherLabels.includes(`${finalLabel} (${counter})`)) counter++;
        finalLabel = `${finalLabel} (${counter})`;
      }
      setTabs((prev) =>
        prev.map((t) => (t.id !== tabId ? t : { ...t, label: finalLabel })),
      );
      updateLabel(tab.sessionName, finalLabel).catch((err) =>
        console.error(`Failed to persist tab rename ${tab.sessionName}:`, err),
      );
    },
    [updateLabel, tabs, setTabs],
  );

  return { handleTabClose, handleDuplicateTab, handleTabRename };
}
