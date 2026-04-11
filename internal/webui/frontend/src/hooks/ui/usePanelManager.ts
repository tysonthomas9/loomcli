/**
 * Centralized panel mutual exclusivity and transition choreography.
 *
 * Manages a single active panel at a time (issue or agent), enforcing
 * 300ms close-then-open transitions between panel types, same-panel
 * no-op, same-type content swap, and rapid-click debouncing.
 */

import { useState, useCallback, useRef, useEffect } from "react";

/** Duration of CSS slide-out animation (must match panel CSS transition). */
const TRANSITION_MS = 300;

/** Discriminated union for the active panel state. */
export type PanelState =
  | { type: "issue"; id: string }
  | { type: "agent"; name: string }
  | null;

/** The non-null panel variant, used by openPanel. */
type PanelTarget = NonNullable<PanelState>;

/** Extracts the panel type string union. */
export type PanelType = PanelTarget["type"];

export interface UsePanelManagerReturn {
  /** Currently open panel (null = none). */
  activePanel: PanelState;
  /**
   * Panel queued to open after the current close animation (null = none).
   * NOTE: This is a ref snapshot read at render time, NOT reactive state.
   * It will not trigger a re-render on its own — it is always read alongside
   * an `activePanel` state change that triggers the render.
   */
  pendingPanel: PanelState;
  /** Open a panel. Closes current first if different type. Same panel = no-op. */
  openPanel: (panel: PanelTarget) => void;
  /** Close the active panel (no-op if already closed). */
  closePanel: () => void;
  /** Convenience: check if a specific panel type (and optionally id) is active. */
  isOpen: (type: PanelType, id?: string) => boolean;
}

/** Returns true when two non-null panel targets refer to the same panel. */
function isSamePanel(a: PanelTarget, b: PanelTarget): boolean {
  if (a.type !== b.type) return false;
  if (a.type === "issue" && b.type === "issue") return a.id === b.id;
  if (a.type === "agent" && b.type === "agent") return a.name === b.name;
  return false;
}

export function usePanelManager(): UsePanelManagerReturn {
  const [activePanel, setActivePanel] = useState<PanelState>(null);
  const pendingPanelRef = useRef<PanelState>(null);
  const timeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const mountedRef = useRef(true);

  // Mount/unmount lifecycle — clean up pending timeout on unmount.
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
      if (timeoutRef.current !== null) {
        clearTimeout(timeoutRef.current);
      }
    };
  }, []);

  const openPanel = useCallback(
    (panel: PanelTarget) => {
      // Cancel any in-flight transition timeout.
      if (timeoutRef.current !== null) {
        clearTimeout(timeoutRef.current);
        timeoutRef.current = null;
      }

      if (activePanel !== null) {
        // Same type + same id → no-op.
        if (isSamePanel(activePanel, panel)) {
          return;
        }

        // Same type, different id → swap content without animation.
        if (activePanel.type === panel.type) {
          pendingPanelRef.current = null;
          setActivePanel(panel);
          return;
        }

        // Different type → close current, then open new after transition.
        pendingPanelRef.current = panel;
        setActivePanel(null);
        timeoutRef.current = setTimeout(() => {
          if (!mountedRef.current) return;
          const pending = pendingPanelRef.current;
          pendingPanelRef.current = null;
          timeoutRef.current = null;
          setActivePanel(pending);
        }, TRANSITION_MS);
        return;
      }

      // No panel active → open immediately.
      pendingPanelRef.current = null;
      setActivePanel(panel);
    },
    [activePanel],
  );

  const closePanel = useCallback(() => {
    if (timeoutRef.current !== null) {
      clearTimeout(timeoutRef.current);
      timeoutRef.current = null;
    }
    pendingPanelRef.current = null;
    setActivePanel(null);
  }, []);

  const isOpen = useCallback(
    (type: PanelType, id?: string): boolean => {
      if (activePanel === null || activePanel.type !== type) return false;
      if (id === undefined) return true;
      return activePanel.type === "issue"
        ? activePanel.id === id
        : activePanel.name === id;
    },
    [activePanel],
  );

  return {
    activePanel,
    pendingPanel: pendingPanelRef.current,
    openPanel,
    closePanel,
    isOpen,
  };
}
