import { useEffect, useRef } from "react";
import { getFocusableElements } from "../utils/focusUtils";

export interface UseFocusTrapOptions {
  /** Element to focus initially when trap activates. If not provided, focuses the first focusable element or the container itself. */
  initialFocus?: React.RefObject<HTMLElement | null>;
  /** Delay before activating trap (ms). Default: 0 */
  activationDelay?: number;
  /** Priority for concurrent trap resolution. Higher priority wins. Default: 0. */
  priority?: number;
}

// ============= Focus Trap Registry =============
// Module-level registry that mirrors the escape layer registry pattern.
// Only the top-priority trap intercepts Tab events.

interface FocusTrapEntry {
  id: string;
  seq: number; // registration sequence for tiebreaking equal priorities
  priority: number;
  containerRef: React.RefObject<HTMLElement | null>;
  handler: (event: KeyboardEvent) => void;
}

let traps: FocusTrapEntry[] = [];
let trapIdCounter = 0;
let focusTrapListenerAttached = false;

function handleFocusTrapKeyDown(event: KeyboardEvent): void {
  if (event.key !== "Tab") return;
  if (traps.length === 0) return;

  // Top trap is first in the sorted array (highest priority, latest registered for ties)
  const topTrap = traps[0];
  if (!topTrap) return;

  const container = topTrap.containerRef.current;
  if (!container) return;

  // Containment check: only intercept if focus is inside the top trap's container
  if (
    document.activeElement !== container &&
    !container.contains(document.activeElement)
  ) {
    return;
  }

  topTrap.handler(event);
}

export function registerFocusTrap(
  priority: number,
  containerRef: React.RefObject<HTMLElement | null>,
  handler: (event: KeyboardEvent) => void,
): string {
  const seq = ++trapIdCounter;
  const id = `focus-trap-${seq}`;
  traps.push({ id, seq, priority, containerRef, handler });
  // Sort: highest priority first; for equal priority, latest registered first (last-in wins)
  traps.sort((a, b) => {
    if (b.priority !== a.priority) return b.priority - a.priority;
    return b.seq - a.seq;
  });

  if (!focusTrapListenerAttached) {
    document.addEventListener("keydown", handleFocusTrapKeyDown);
    focusTrapListenerAttached = true;
  }
  return id;
}

export function unregisterFocusTrap(id: string): void {
  traps = traps.filter((t) => t.id !== id);
  if (traps.length === 0 && focusTrapListenerAttached) {
    document.removeEventListener("keydown", handleFocusTrapKeyDown);
    focusTrapListenerAttached = false;
  }
}

// Exported for testing only
export function _resetFocusTrapRegistry(): void {
  if (focusTrapListenerAttached) {
    document.removeEventListener("keydown", handleFocusTrapKeyDown);
    focusTrapListenerAttached = false;
  }
  traps = [];
  trapIdCounter = 0;
}

// ============= Hook =============

/**
 * Traps Tab/Shift+Tab focus cycling within a container element.
 * Re-queries focusable elements on each Tab press to handle dynamic content.
 * Uses a module-level registry so only the top-priority trap intercepts Tab
 * when multiple traps are active concurrently.
 */
export function useFocusTrap(
  containerRef: React.RefObject<HTMLElement | null>,
  isActive: boolean,
  options: UseFocusTrapOptions = {},
): void {
  const { initialFocus, activationDelay = 0, priority = 0 } = options;
  const generationRef = useRef(0);

  // Handle initial focus when trap activates
  useEffect(() => {
    if (!isActive || !containerRef.current) return;

    generationRef.current += 1;
    const gen = generationRef.current;

    const focusInitial = () => {
      if (generationRef.current !== gen || !containerRef.current) return;

      if (initialFocus?.current) {
        initialFocus.current.focus();
        return;
      }

      const focusable = getFocusableElements(containerRef.current);
      if (focusable.length > 0 && focusable[0]) {
        focusable[0].focus();
      } else {
        // Focus container itself (should have tabIndex={-1})
        containerRef.current.focus();
      }
    };

    if (activationDelay > 0) {
      const timer = setTimeout(focusInitial, activationDelay);
      return () => clearTimeout(timer);
    } else {
      focusInitial();
    }
  }, [isActive, containerRef, initialFocus, activationDelay]);

  // Handle Tab/Shift+Tab key trapping via registry
  useEffect(() => {
    if (!isActive || !containerRef.current) return;

    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Tab" || !containerRef.current) return;

      const focusable = getFocusableElements(containerRef.current);
      if (focusable.length === 0) return;

      const first = focusable[0];
      const last = focusable[focusable.length - 1];

      if (!first || !last) return;

      if (event.shiftKey) {
        // Shift+Tab: if on first element (or container), wrap to last
        if (
          document.activeElement === first ||
          document.activeElement === containerRef.current
        ) {
          event.preventDefault();
          last.focus();
        }
      } else {
        // Tab: if on last element, wrap to first
        if (document.activeElement === last) {
          event.preventDefault();
          first.focus();
        }
      }
    };

    const id = registerFocusTrap(priority, containerRef, handleKeyDown);
    return () => unregisterFocusTrap(id);
  }, [isActive, containerRef, priority]);
}
