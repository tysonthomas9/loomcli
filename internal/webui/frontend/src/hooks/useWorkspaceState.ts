/**
 * useWorkspaceState - Saves and restores per-workspace UI state on switch.
 * State is stored in-memory only (resets on page reload).
 * Captures: active view, filters, search, selected issue, scroll position.
 * On switch: closes panels, restores saved state (or defaults), updates URL.
 */

import { useState, useCallback, useEffect, useRef } from "react";

import type { ViewMode } from "@/components/ViewSwitcher";
import type { FilterState, FilterActions } from "./useFilterState";

/**
 * Per-workspace snapshot of UI state.
 */
export interface WorkspaceSnapshot {
  view: ViewMode;
  filters: FilterState;
  searchValue: string;
  selectedIssueId: string | null;
  scrollTop: number;
}

/**
 * Parameters for useWorkspaceState hook.
 */
export interface UseWorkspaceStateParams {
  /** Ref tracking latest state values (avoids stale closures) */
  stateRef: React.RefObject<{
    view: ViewMode;
    filters: FilterState;
    searchValue: string;
    selectedIssueId: string | null;
  }>;
  /** Set the active view (clears issue ID when not issue-detail) */
  setView: (view: ViewMode) => void;
  filterActions: FilterActions;
  setSearchValue: (value: string) => void;
  /** Synchronously close all panels without animation */
  closeAllPanels: () => void;
}

/**
 * Return type for useWorkspaceState hook.
 */
export interface UseWorkspaceStateReturn {
  currentWorkspaceId: string | null;
  switchWorkspace: (newId: string | null) => void;
}

const WORKSPACE_PARAM = "workspace";

/**
 * Read workspace param from current URL.
 */
function parseWorkspaceFromUrl(): string | null {
  if (typeof window === "undefined") return null;
  const params = new URLSearchParams(window.location.search);
  return params.get(WORKSPACE_PARAM);
}

/**
 * Update workspace param in URL via replaceState.
 */
function updateWorkspaceUrl(workspaceId: string | null): void {
  if (typeof window === "undefined") return;
  const params = new URLSearchParams(window.location.search);
  if (workspaceId) {
    params.set(WORKSPACE_PARAM, workspaceId);
  } else {
    params.delete(WORKSPACE_PARAM);
  }
  const queryString = params.toString();
  const newUrl = queryString
    ? `${window.location.pathname}?${queryString}`
    : window.location.pathname;
  window.history.replaceState(null, "", newUrl);
}

export function useWorkspaceState(
  params: UseWorkspaceStateParams,
): UseWorkspaceStateReturn {
  const { stateRef, setView, filterActions, setSearchValue, closeAllPanels } =
    params;

  const [currentWorkspaceId, setCurrentWorkspaceId] = useState<string | null>(
    () => parseWorkspaceFromUrl(),
  );

  // Ref mirrors currentWorkspaceId to keep switchWorkspace stable across renders
  const currentWorkspaceIdRef = useRef(currentWorkspaceId);
  useEffect(() => {
    currentWorkspaceIdRef.current = currentWorkspaceId;
  });

  const snapshotsRef = useRef<Map<string, WorkspaceSnapshot>>(new Map());
  const rafIdRef = useRef<number | null>(null);

  // Capture snapshot from current state
  const captureSnapshot = useCallback((): WorkspaceSnapshot => {
    const current = stateRef.current!;
    const mainEl = document.getElementById("main-content");
    return {
      view: current.view,
      filters: { ...current.filters },
      searchValue: current.searchValue,
      selectedIssueId: current.selectedIssueId,
      scrollTop: mainEl?.scrollTop ?? 0,
    };
  }, [stateRef]);

  // Restore snapshot or apply defaults
  const restoreSnapshot = useCallback(
    (snapshot: WorkspaceSnapshot | undefined) => {
      if (snapshot) {
        // setView clears issue ID when view !== "issue-detail"
        setView(snapshot.view);
        filterActions.clearAll();
        if (snapshot.filters.priority !== undefined)
          filterActions.setPriority(snapshot.filters.priority);
        if (snapshot.filters.type !== undefined)
          filterActions.setType(snapshot.filters.type);
        if (snapshot.filters.labels !== undefined)
          filterActions.setLabels(snapshot.filters.labels);
        if (snapshot.filters.search !== undefined)
          filterActions.setSearch(snapshot.filters.search);
        if (snapshot.filters.showBlocked !== undefined)
          filterActions.setShowBlocked(snapshot.filters.showBlocked);
        if (snapshot.filters.groupBy !== undefined)
          filterActions.setGroupBy(snapshot.filters.groupBy);
        setSearchValue(snapshot.searchValue);
      } else {
        // First visit defaults
        setView("kanban");
        filterActions.clearAll();
        setSearchValue("");
      }

      // Schedule scroll restore via rAF
      const scrollTarget = snapshot?.scrollTop ?? 0;
      rafIdRef.current = requestAnimationFrame(() => {
        const mainEl = document.getElementById("main-content");
        if (mainEl) {
          mainEl.scrollTop = scrollTarget;
        }
        rafIdRef.current = null;
      });
    },
    [setView, filterActions, setSearchValue],
  );

  // switchWorkspace uses ref for currentWorkspaceId so the callback is stable
  // and the popstate effect doesn't re-register on every workspace change.
  // Accepts skipUrlUpdate to avoid redundant replaceState during popstate.
  const switchWorkspaceInternal = useCallback(
    (newId: string | null, skipUrlUpdate: boolean) => {
      const prevId = currentWorkspaceIdRef.current;

      // Capture current workspace state before switching
      if (prevId !== null) {
        const snapshot = captureSnapshot();
        snapshotsRef.current.set(prevId, snapshot);
      }

      // Cancel any pending scroll restore from prior switch
      if (rafIdRef.current !== null) {
        cancelAnimationFrame(rafIdRef.current);
        rafIdRef.current = null;
      }

      // Close all panels synchronously
      closeAllPanels();

      // Look up and restore snapshot for new workspace
      const savedSnapshot =
        newId !== null ? snapshotsRef.current.get(newId) : undefined;
      restoreSnapshot(savedSnapshot);

      // Update URL workspace param (skip during popstate — URL already correct)
      if (!skipUrlUpdate) {
        updateWorkspaceUrl(newId);
      }

      // Update current workspace
      currentWorkspaceIdRef.current = newId;
      setCurrentWorkspaceId(newId);
    },
    [captureSnapshot, closeAllPanels, restoreSnapshot],
  );

  const switchWorkspace = useCallback(
    (newId: string | null) => switchWorkspaceInternal(newId, false),
    [switchWorkspaceInternal],
  );

  // Handle popstate for browser back/forward
  useEffect(() => {
    const handlePopState = () => {
      const urlWorkspace = parseWorkspaceFromUrl();
      if (urlWorkspace !== currentWorkspaceIdRef.current) {
        switchWorkspaceInternal(urlWorkspace, true);
      }
    };

    window.addEventListener("popstate", handlePopState);
    return () => window.removeEventListener("popstate", handlePopState);
  }, [switchWorkspaceInternal]);

  // Cleanup pending rAF on unmount
  useEffect(() => {
    return () => {
      if (rafIdRef.current !== null) {
        cancelAnimationFrame(rafIdRef.current);
      }
    };
  }, []);

  return { currentWorkspaceId, switchWorkspace };
}
