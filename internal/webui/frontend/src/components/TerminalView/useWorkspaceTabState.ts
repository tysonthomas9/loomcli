/**
 * Hook to manage workspace-scoped tab state.
 * Resolves the active workspace from WorkspaceContext, saves/restores tab sets
 * when switching workspaces, and returns the resolved workspace name.
 */

import { useEffect, useRef, type MutableRefObject } from "react";

import { useWorkspaceContext } from "@/hooks";

import type { TabState } from "./terminalTabUtils";

interface WorkspaceTabStateArgs {
  tabs: TabState[];
  activeTabId: string;
  setTabs: React.Dispatch<React.SetStateAction<TabState[]>>;
  setActiveTabId: React.Dispatch<React.SetStateAction<string>>;
  initializedRef: MutableRefObject<boolean>;
}

interface WorkspaceTabStateReturn {
  /** Workspace name (for tab state keying and session naming) */
  name: string;
  /** Workspace stable ID (for API calls) */
  id: string;
}

export function useWorkspaceTabState(
  args: WorkspaceTabStateArgs,
): WorkspaceTabStateReturn {
  const { tabs, activeTabId, setTabs, setActiveTabId, initializedRef } = args;
  const { activeWorkspaceName, workspace: wsData } = useWorkspaceContext();
  const workspace = activeWorkspaceName || "default";
  const workspaceId = wsData?.id || "";

  const stateMapRef = useRef<
    Map<string, { tabs: TabState[]; activeTabId: string }>
  >(new Map());
  const prevWorkspaceRef = useRef(workspace);

  useEffect(() => {
    if (prevWorkspaceRef.current === workspace) return;
    if (prevWorkspaceRef.current && tabs.length > 0) {
      stateMapRef.current.set(prevWorkspaceRef.current, {
        tabs: [...tabs],
        activeTabId,
      });
    }
    const saved = stateMapRef.current.get(workspace);
    if (saved) {
      setTabs(saved.tabs);
      setActiveTabId(saved.activeTabId);
    } else {
      setTabs([]);
      setActiveTabId("");
    }
    initializedRef.current = false;
    prevWorkspaceRef.current = workspace;
  }, [workspace]); // eslint-disable-line react-hooks/exhaustive-deps

  return { name: workspace, id: workspaceId };
}
