/**
 * useWorkspaceState - React hook for managing workspace state with snapshot/restore.
 * Captures and restores view state (view mode, filters, search, selection, scroll)
 * when switching between workspaces.
 */

import { useState, useCallback, useRef, useEffect } from "react";

import type { ViewMode } from "@/components/ViewSwitcher";

/**
 * Snapshot of workspace state for save/restore on workspace switch.
 */
export interface WorkspaceSnapshot {
  view: ViewMode;
  filters: Record<string, unknown>;
  searchValue: string;
  selectedIssueId: string | null;
  scrollTop: number;
}

/**
 * Current workspace UI state tracked via ref (avoids re-renders on every scroll).
 */
export interface WorkspaceUIState {
  view: ViewMode;
  filters: Record<string, unknown>;
  searchValue: string;
  selectedIssueId: string | null;
}

/**
 * Callbacks for applying restored state.
 */
export interface WorkspaceStateCallbacks {
  setView: (view: ViewMode) => void;
  setFilters: (filters: Record<string, unknown>) => void;
  clearAll: () => void;
  setSearchValue: (value: string) => void;
  setSelectedIssueId: (id: string | null) => void;
}

/**
 * Options for the useWorkspaceState hook.
 */
export interface UseWorkspaceStateOptions {
  /** Ref to current workspace UI state (updated by parent without re-render) */
  stateRef: React.RefObject<WorkspaceUIState | null>;
  /** Callbacks to apply restored snapshot */
  callbacks: WorkspaceStateCallbacks;
}

/**
 * Return type for the useWorkspaceState hook.
 */
export interface UseWorkspaceStateReturn {
  /** Current workspace ID */
  currentWorkspaceId: string | null;
  /** Switch to a different workspace (captures current, restores target) */
  switchWorkspace: (newWorkspaceId: string) => void;
  /** Capture snapshot of current state (for external use) */
  captureSnapshot: () => WorkspaceSnapshot;
}

/**
 * Default snapshot used when stateRef is not yet populated (pre-mount race).
 */
const DEFAULT_SNAPSHOT: WorkspaceSnapshot = {
  view: "kanban",
  filters: {},
  searchValue: "",
  selectedIssueId: null,
  scrollTop: 0,
};

/**
 * URL parameter name for workspace.
 */
const WORKSPACE_PARAM = "workspace";

/**
 * Parse workspace ID from URL.
 */
function parseWorkspaceFromUrl(): string | null {
  if (typeof window === "undefined") return null;
  const params = new URLSearchParams(window.location.search);
  return params.get(WORKSPACE_PARAM);
}

/**
 * React hook for managing workspace state with snapshot/restore.
 *
 * Captures the current UI state (view, filters, search, selection, scroll position)
 * as a snapshot when leaving a workspace, and restores the saved snapshot when
 * returning to that workspace.
 */
export function useWorkspaceState(
  options: UseWorkspaceStateOptions,
): UseWorkspaceStateReturn {
  const { stateRef, callbacks } = options;

  // Initialize workspace ID from URL
  const [currentWorkspaceId, setCurrentWorkspaceId] = useState<string | null>(
    () => parseWorkspaceFromUrl(),
  );

  // Track snapshots per workspace
  const snapshotsRef = useRef<Map<string, WorkspaceSnapshot>>(new Map());

  // Track current workspace ID in a ref for use in callbacks
  const currentWorkspaceIdRef = useRef<string | null>(currentWorkspaceId);
  currentWorkspaceIdRef.current = currentWorkspaceId;

  /**
   * Capture the current workspace state as a snapshot.
   * Returns a default snapshot if stateRef is not yet populated (pre-mount race guard).
   */
  const captureSnapshot = useCallback((): WorkspaceSnapshot => {
    const current = stateRef.current;
    if (!current) {
      return { ...DEFAULT_SNAPSHOT };
    }
    const mainEl = document.getElementById("main-content");
    return {
      view: current.view,
      filters: { ...current.filters },
      searchValue: current.searchValue,
      selectedIssueId: current.selectedIssueId,
      scrollTop: mainEl?.scrollTop ?? 0,
    };
  }, [stateRef]);

  /**
   * Restore a snapshot by applying its state via callbacks.
   */
  const restoreSnapshot = useCallback(
    (snapshot: WorkspaceSnapshot) => {
      callbacks.setView(snapshot.view);
      callbacks.setFilters(snapshot.filters);
      callbacks.setSearchValue(snapshot.searchValue);
      callbacks.setSelectedIssueId(snapshot.selectedIssueId);

      // Restore scroll position after React renders
      requestAnimationFrame(() => {
        const mainEl = document.getElementById("main-content");
        if (mainEl) {
          mainEl.scrollTop = snapshot.scrollTop;
        }
      });
    },
    [callbacks],
  );

  /**
   * Switch to a new workspace. Captures current state, restores target state.
   */
  const switchWorkspace = useCallback(
    (newWorkspaceId: string) => {
      const prevId = currentWorkspaceIdRef.current;

      // Save current workspace's snapshot
      if (prevId !== null) {
        const snapshot = captureSnapshot();
        snapshotsRef.current.set(prevId, snapshot);
      }

      // Update current workspace
      setCurrentWorkspaceId(newWorkspaceId);

      // Update URL
      if (typeof window !== "undefined") {
        const params = new URLSearchParams(window.location.search);
        params.set(WORKSPACE_PARAM, newWorkspaceId);
        const newUrl = `${window.location.pathname}?${params.toString()}`;
        window.history.pushState(null, "", newUrl);
      }

      // Restore target workspace's snapshot, or apply defaults
      const savedSnapshot = snapshotsRef.current.get(newWorkspaceId);
      if (savedSnapshot) {
        restoreSnapshot(savedSnapshot);
      } else {
        // Apply defaults for a workspace visited for the first time
        callbacks.setView("kanban");
        callbacks.clearAll();
        callbacks.setSearchValue("");
        callbacks.setSelectedIssueId(null);
      }
    },
    [captureSnapshot, restoreSnapshot, callbacks],
  );

  // Handle popstate (browser back/forward)
  useEffect(() => {
    if (typeof window === "undefined") return;

    const handlePopState = () => {
      const wsId = parseWorkspaceFromUrl();
      if (wsId && wsId !== currentWorkspaceIdRef.current) {
        switchWorkspace(wsId);
      }
    };

    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
  }, [switchWorkspace]);

  return {
    currentWorkspaceId,
    switchWorkspace,
    captureSnapshot,
  };
}
