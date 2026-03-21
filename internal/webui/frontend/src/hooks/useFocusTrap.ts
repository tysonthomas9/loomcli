import { useEffect, useRef } from "react";
import { getFocusableElements } from "../utils/focusUtils";

export interface UseFocusTrapOptions {
  /** Element to focus initially when trap activates. If not provided, focuses the first focusable element or the container itself. */
  initialFocus?: React.RefObject<HTMLElement | null>;
  /** Delay before activating trap (ms). Default: 0 */
  activationDelay?: number;
}

/**
 * Traps Tab/Shift+Tab focus cycling within a container element.
 * Re-queries focusable elements on each Tab press to handle dynamic content.
 */
export function useFocusTrap(
  containerRef: React.RefObject<HTMLElement | null>,
  isActive: boolean,
  options: UseFocusTrapOptions = {},
): void {
  const { initialFocus, activationDelay = 0 } = options;
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

  // Handle Tab/Shift+Tab key trapping
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

    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [isActive, containerRef]);
}
