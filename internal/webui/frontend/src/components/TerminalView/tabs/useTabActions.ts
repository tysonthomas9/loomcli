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

import type {
  ConnectionState,
  TerminalInstanceHandle,
} from "@/components/TerminalView/instances";
import { DEFAULT_TERMINAL_WORKTREE_GROUP_ID } from "@/utils/terminalSidebarBridge";

import { type TabState, getNextDuplicateName } from "./terminalTabUtils";

interface UseTabActionsOptions {
  workspaceId: string;
  tabs: TabState[];
  setTabs: Dispatch<SetStateAction<TabState[]>>;
  setActiveTabId: Dispatch<SetStateAction<string>>;
  activeTabIdRef: MutableRefObject<string>;
  instanceRefs: MutableRefObject<Map<string, TerminalInstanceHandle>>;
  createTab: (
    session: string,
    label: string,
    sortOrder: number,
    worktreeGroupId?: string,
  ) => Promise<void>;
  tabMetadata?: Array<{ session_name: string; worktree_group_id?: string }>;
  updateLabel: (session: string, label: string) => Promise<void>;
  deleteTab: (session: string) => Promise<void>;
}

// workspaceId is retained in the options for symmetry with sibling hooks
// even though the tmux-kill API path that consumed it is gone — keeping the
// signature stable minimizes churn at call sites and leaves a clean slot if
// per-close server work needs to be reintroduced.

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
  tabMetadata = [],
  updateLabel,
  deleteTab,
}: UseTabActionsOptions): UseTabActionsReturn {
  const handleTabClose = useCallback(
    (tabId: string) => {
      const sourceTab = tabs.find((t) => t.id === tabId);
      if (!sourceTab || tabs.length <= 1) return;
      const sessionNameToDelete = sourceTab.sessionName;

      const handle = instanceRefs.current.get(tabId);

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

      deleteTab(sessionNameToDelete).catch((err) =>
        console.error(
          `Failed to delete tab metadata ${sessionNameToDelete}:`,
          err,
        ),
      );

      // Close the WebSocket. On the server side, closing the WS kills the
      // PTY — there's no separate server-side "kill session" RPC anymore.
      handle?.disconnect?.().catch(() => {});
    },
    [deleteTab, tabs, setTabs, setActiveTabId, activeTabIdRef, instanceRefs],
  );

  const handleDuplicateTab = useCallback(
    async (tabId: string) => {
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
      const sourceMeta = tabMetadata.find(
        (meta) => meta.session_name === sourceTab.sessionName,
      );
      const groupId = sourceMeta?.worktree_group_id;
      const isGroupedTab = Boolean(
        groupId && groupId !== DEFAULT_TERMINAL_WORKTREE_GROUP_ID,
      );
      if (isGroupedTab) {
        // Same rule as new group tabs: the PUT must land before the tab
        // mounts, or the first WS attach spawns the PTY at the workspace
        // root instead of the group root.
        try {
          await createTab(
            result.sessionName,
            result.label,
            tabs.length,
            groupId,
          );
        } catch (err) {
          console.error(
            `Failed to persist duplicated tab ${result.sessionName}:`,
            err,
          );
          return;
        }
      } else {
        createTab(result.sessionName, result.label, tabs.length, groupId).catch(
          (err) =>
            console.error(
              `Failed to persist duplicated tab ${result.sessionName}:`,
              err,
            ),
        );
      }
      setTabs((prev) => [...prev, newTab]);
      setActiveTabId(newTab.id);
    },
    [createTab, tabMetadata, tabs, setTabs, setActiveTabId],
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
