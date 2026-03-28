import { useEffect, useRef } from "react";
import { getFocusableElements } from "../utils/focusUtils";

export interface UseFocusTrapOptions {
  /** Element to focus initially when trap activates. If not provided, focuses the first focusable element or the container itself. */
  initialFocus?: React.RefObject<HTMLElement | null>;
  /** Delay before activating trap (ms). Default: 0 */
  activationDelay?: number;
  /** Priority for concurrent trap resolution. Higher priority wins Tab handling. Default: 0 */
  priority?: number;
}

// ---------------------------------------------------------------------------
// Self-contained focus-trap registry
//
// Manages a single document keydown listener for Tab. When multiple traps are
// simultaneously active, only the highest-priority trap intercepts Tab.
// Mirrors the escape layer registry in useKeyboardShortcuts.tsx.
// ---------------------------------------------------------------------------

interface FocusTrap {
  id: number;
  priority: number;
  containerRef: React.RefObject<HTMLElement | null>;
  handler: (event: KeyboardEvent) => void;
}

let trapIdCounter = 0;
let focusTrapListenerAttached = false;
const traps: FocusTrap[] = [];

function handleFocusTrapKeyDown(event: KeyboardEvent) {
  if (event.key !== "Tab") return;
  if (traps.length === 0) return;

  // Find the top trap whose container contains the active element
  for (const trap of traps) {
    const container = trap.containerRef.current;
    if (!container) continue;

    // Only let this trap handle Tab if focus is inside its container
    // (Node.contains returns true for the node itself)
    if (container.contains(document.activeElement)) {
      trap.handler(event);
      return;
    }
  }
  // Focus is outside all registered traps — let the event propagate naturally
}

function attachFocusTrapListener() {
  if (!focusTrapListenerAttached) {
    document.addEventListener("keydown", handleFocusTrapKeyDown);
    focusTrapListenerAttached = true;
  }
}

function detachFocusTrapListener() {
  if (focusTrapListenerAttached && traps.length === 0) {
    document.removeEventListener("keydown", handleFocusTrapKeyDown);
    focusTrapListenerAttached = false;
  }
}

export function registerFocusTrap(
  priority: number,
  containerRef: React.RefObject<HTMLElement | null>,
  handler: (event: KeyboardEvent) => void,
): number {
  const id = ++trapIdCounter;
  traps.push({ id, priority, containerRef, handler });
  // Sort descending by priority; equal priority: later registered (higher id) first
  traps.sort((a, b) => b.priority - a.priority || b.id - a.id);
  attachFocusTrapListener();
  return id;
}

export function unregisterFocusTrap(id: number): void {
  const idx = traps.findIndex((t) => t.id === id);
  if (idx !== -1) traps.splice(idx, 1);
  detachFocusTrapListener();
}

/** Reset registry state. Exported for test cleanup only. */
export function resetFocusTrapRegistry(): void {
  traps.length = 0;
  detachFocusTrapListener();
}

/**
 * Traps Tab/Shift+Tab focus cycling within a container element.
 * Re-queries focusable elements on each Tab press to handle dynamic content.
 *
 * When multiple traps are simultaneously active, only the highest-priority
 * trap (whose container contains the active element) intercepts Tab.
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

  // Handle Tab/Shift+Tab key trapping via the focus-trap registry
  useEffect(() => {
    if (!isActive || !containerRef.current) return;

    const handleKeyDown = (event: KeyboardEvent) => {
      if (!containerRef.current) return;

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
