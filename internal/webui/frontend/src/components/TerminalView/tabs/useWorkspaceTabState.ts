/**
 * Hook to manage workspace-scoped tab state.
 * Resolves the active workspace from WorkspaceContext, saves/restores tab sets
 * when switching workspaces, and returns the resolved workspace name and ID.
 *
 * Keyed on the raw `:workspaceId` route param rather than the polled workspace
 * data, so there is no transient "__unresolved__" hop while that data catches
 * up. SPA navigation always writes the canonical workspace UUID into the URL,
 * so in practice the key is the stable id and a rename does not clear tabs.
 * The server also accepts a workspace *name* in that route segment, so a
 * hand-typed `/ws/<name>/` URL keys the cache on the alias it was typed with —
 * a separate entry from the same workspace's UUID, and one that stops matching
 * if the workspace is renamed. Both are per-session in-memory caches, so the
 * only cost is tabs re-hydrating from the server.
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
  const { activeWorkspaceName, workspaceId } = useWorkspaceContext();
  const workspace = activeWorkspaceName || "default";

  const stateMapRef = useRef<
    Map<string, { tabs: TabState[]; activeTabId: string }>
  >(new Map());
  const cacheKey = workspaceId || "__unresolved__";
  const prevWorkspaceIdRef = useRef(cacheKey);

  useEffect(() => {
    if (prevWorkspaceIdRef.current === cacheKey) return;
    if (
      prevWorkspaceIdRef.current &&
      prevWorkspaceIdRef.current !== "__unresolved__" &&
      tabs.length > 0
    ) {
      stateMapRef.current.set(prevWorkspaceIdRef.current, {
        tabs: [...tabs],
        activeTabId,
      });
    }
    const saved = stateMapRef.current.get(cacheKey);
    if (saved) {
      setTabs(saved.tabs);
      setActiveTabId(saved.activeTabId);
    } else {
      setTabs([]);
      setActiveTabId("");
    }
    initializedRef.current = false;
    prevWorkspaceIdRef.current = cacheKey;
  }, [cacheKey]); // eslint-disable-line react-hooks/exhaustive-deps

  return { name: workspace, id: workspaceId };
}
