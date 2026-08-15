/**
 * Hook to manage workspace-scoped tab state.
 * Resolves the active workspace from WorkspaceContext, clears tab state when
 * the workspace changes, and returns the resolved workspace name and ID.
 *
 * It does NOT cache tab sets per workspace: server-side tab metadata is the
 * persistence layer, and TerminalView unmounts on every workspace switch
 * anyway (it renders below WorkspaceProvider's <PerWorkspacePrefsProvider
 * key={workspaceId}>). The reset below is what matters — it also covers the
 * case where the id changes without a remount.
 */

import { useEffect, useRef, type MutableRefObject } from "react";

import { useWorkspaceContext } from "@/hooks";

import type { TabState } from "./terminalTabUtils";

interface WorkspaceTabStateArgs {
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
  const { setTabs, setActiveTabId, initializedRef } = args;
  const {
    activeWorkspaceName,
    workspaceId: contextWorkspaceId,
    workspace: wsData,
  } = useWorkspaceContext();
  const workspace = activeWorkspaceName || "default";
  // Route-authoritative id (what WorkspaceLayout passes into WorkspaceProvider).
  // wsData comes from the polled store, which deliberately serves the previous
  // workspace's data while refetching, so it lags on a switch.
  const workspaceId = contextWorkspaceId || wsData?.id || "";

  const cacheKey = workspaceId || "__unresolved__";
  const prevWorkspaceIdRef = useRef(cacheKey);

  useEffect(() => {
    if (prevWorkspaceIdRef.current === cacheKey) return;
    setTabs([]);
    setActiveTabId("");
    initializedRef.current = false;
    prevWorkspaceIdRef.current = cacheKey;
  }, [cacheKey]); // eslint-disable-line react-hooks/exhaustive-deps

  return { name: workspace, id: workspaceId };
}
