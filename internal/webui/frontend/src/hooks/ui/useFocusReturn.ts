import { useEffect, useRef } from "react";

export interface UseFocusReturnOptions {
  /** Element to focus when panel opens. If not provided, no focus-on-open occurs. */
  focusTarget?: React.RefObject<HTMLElement | null>;
  /** Delay before focusing (ms), to wait for CSS transitions. Default: 0 */
  focusDelay?: number;
  /** Fallback element if trigger is removed from DOM. Default: document.body */
  fallbackRef?: React.RefObject<HTMLElement | null>;
}

/**
 * Captures the active element when `isOpen` becomes true and restores focus
 * to it when `isOpen` becomes false. Handles the edge case where the trigger
 * has been removed from the DOM.
 */
export function useFocusReturn(
  isOpen: boolean,
  options: UseFocusReturnOptions = {},
): void {
  const { focusTarget, focusDelay = 0, fallbackRef } = options;
  const triggerRef = useRef<HTMLElement | null>(null);
  const generationRef = useRef(0);

  useEffect(() => {
    if (isOpen) {
      generationRef.current += 1;
      const gen = generationRef.current;

      // Capture the element that had focus before the panel opened
      triggerRef.current = document.activeElement as HTMLElement | null;

      // Optionally focus the target element on open
      if (focusTarget?.current) {
        if (focusDelay > 0) {
          const timer = setTimeout(() => {
            if (generationRef.current === gen) {
              focusTarget.current?.focus();
            }
          }, focusDelay);
          return () => clearTimeout(timer);
        } else {
          focusTarget.current.focus();
        }
      }
    } else {
      // Restore focus on close
      const trigger = triggerRef.current;
      if (!trigger) return;

      if (document.contains(trigger) && trigger.focus) {
        trigger.focus();
      } else {
        // Walk up the parent chain to find a still-mounted focusable element
        let parent = trigger.parentElement;
        while (parent) {
          if (document.contains(parent) && parent.focus) {
            parent.focus();
            triggerRef.current = null;
            return;
          }
          parent = parent.parentElement;
        }
        // Ultimate fallback
        const fallback = fallbackRef?.current ?? document.body;
        fallback.focus();
      }
      triggerRef.current = null;
    }
  }, [isOpen, focusTarget, focusDelay, fallbackRef]);
}
