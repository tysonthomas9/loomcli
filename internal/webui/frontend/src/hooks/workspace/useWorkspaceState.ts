/**
 * useWorkspaceState - Saves and restores per-workspace ephemeral UI state on switch.
 * State is stored in a module-level Map (survives remounts, resets on page reload).
 * Captures: scroll position and active panel state.
 *
 * URL-owned state (view mode, filters, search, selected issue) is NOT stored —
 * the URL is the single source of truth for those (per T12's URL/State Contract).
 *
 * T24: Rewritten for path-based routing. Module-level Map survives WorkspaceLayout
 * remounts. Accepts workspaceId as prop, returns void.
 */

import { useEffect, useRef, type RefObject } from "react";

import type { PanelState } from "@/hooks/ui";
import { useWorkspaceContext } from "./useWorkspaceContext";

// Module-level storage survives component remounts within the same session.
const workspaceSnapshots = new Map<string, WorkspaceSnapshot>();

/**
 * Clear all stored workspace snapshots (useful for testing).
 */
export function clearWorkspaceSnapshots(): void {
  workspaceSnapshots.clear();
}

/**
 * Per-workspace snapshot of ephemeral UI state.
 * Only stores state that is NOT URL-owned.
 */
export interface WorkspaceSnapshot {
  scrollTop: number;
  activePanel: PanelState;
}

/**
 * Parameters for useWorkspaceState hook.
 */
export interface UseWorkspaceStateParams {
  /** Ref to the main scrollable container element */
  scrollContainerRef: RefObject<HTMLElement | null>;
  /** Current active panel state from usePanelManager */
  activePanel: PanelState;
  /** Callback to restore a panel state (openPanel from usePanelManager) */
  restorePanel: (panel: NonNullable<PanelState>) => void;
  /** Synchronously close all panels without animation */
  closeAllPanels: () => void;
}

export function useWorkspaceState(params: UseWorkspaceStateParams): void {
  const { workspaceId } = useWorkspaceContext();
  const { scrollContainerRef, activePanel, restorePanel, closeAllPanels } =
    params;

  const rafIdRef = useRef<number | null>(null);
  const prevWorkspaceIdRef = useRef<string>(workspaceId);

  // Panel ref: updated in an effect (not during render) so that the
  // workspaceId-change effect — which runs first — still sees the
  // PREVIOUS render's panel value when capturing the old workspace.
  const activePanelRef = useRef<PanelState>(activePanel);

  const restorePanelRef = useRef(restorePanel);
  restorePanelRef.current = restorePanel;

  const closeAllPanelsRef = useRef(closeAllPanels);
  closeAllPanelsRef.current = closeAllPanels;

  // Capture snapshot for a workspace
  const captureSnapshot = (wsId: string): void => {
    const snapshot: WorkspaceSnapshot = {
      scrollTop: scrollContainerRef.current?.scrollTop ?? 0,
      activePanel: activePanelRef.current,
    };
    workspaceSnapshots.set(wsId, snapshot);
  };

  // Restore snapshot for a workspace (scroll via rAF, panel immediately)
  const restoreFromSnapshot = (wsId: string): void => {
    const snapshot = workspaceSnapshots.get(wsId);
    if (!snapshot) return;

    // Restore panel state
    if (snapshot.activePanel !== null) {
      restorePanelRef.current(snapshot.activePanel);
    }

    // Restore scroll position via rAF to let DOM settle
    rafIdRef.current = requestAnimationFrame(() => {
      const container = scrollContainerRef.current;
      if (container) {
        container.scrollTop = snapshot.scrollTop;
      }
      rafIdRef.current = null;
    });
  };

  // React to workspaceId changes (defense-in-depth for non-remount transitions)
  useEffect(() => {
    const prevId = prevWorkspaceIdRef.current;
    if (prevId === workspaceId) return;

    // Capture snapshot for previous workspace
    captureSnapshot(prevId);

    // Cancel pending scroll restore
    if (rafIdRef.current !== null) {
      cancelAnimationFrame(rafIdRef.current);
      rafIdRef.current = null;
    }

    // Close all panels before restoring new workspace state
    closeAllPanelsRef.current();

    // Restore snapshot for new workspace
    restoreFromSnapshot(workspaceId);

    prevWorkspaceIdRef.current = workspaceId;
  }, [workspaceId]); // eslint-disable-line react-hooks/exhaustive-deps

  // On mount: restore snapshot if available
  useEffect(() => {
    restoreFromSnapshot(workspaceId);

    // On unmount: capture snapshot for current workspace
    return () => {
      captureSnapshot(prevWorkspaceIdRef.current);

      // Cancel pending rAF
      if (rafIdRef.current !== null) {
        cancelAnimationFrame(rafIdRef.current);
        rafIdRef.current = null;
      }
    };
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  // Sync activePanelRef LAST — after the workspaceId effect has had a
  // chance to capture the old panel value for the previous workspace.
  useEffect(() => {
    activePanelRef.current = activePanel;
  });
}
