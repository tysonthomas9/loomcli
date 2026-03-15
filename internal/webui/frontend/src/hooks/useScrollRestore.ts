/**
 * Hook for preserving scroll position across view switches.
 * Stores scroll offsets keyed by view name in a module-level Map.
 * Restores position after a requestAnimationFrame delay to ensure DOM/virtualizer has settled.
 */

import { useEffect, useRef, type RefObject } from "react";

// Module-level storage persists across component mounts within the same session
const scrollPositions = new Map<string, number>();

export interface UseScrollRestoreOptions {
  /** Key identifying the current view (e.g., 'kanban', 'table') */
  viewKey: string;
  /** Ref to the scrollable container element. Must be a stable ref (e.g., from useRef). */
  scrollContainerRef: RefObject<HTMLElement | null>;
  /** Whether scroll restore is enabled (default: true) */
  enabled?: boolean;
}

/**
 * useScrollRestore captures scroll position on unmount and restores it on mount.
 */
export function useScrollRestore({
  viewKey,
  scrollContainerRef,
  enabled = true,
}: UseScrollRestoreOptions): void {
  const rafIdRef = useRef<number | null>(null);
  const viewKeyRef = useRef(viewKey);
  viewKeyRef.current = viewKey;

  // Restore scroll position on mount
  useEffect(() => {
    if (!enabled) return;

    const savedPosition = scrollPositions.get(viewKey);
    if (savedPosition === undefined) return;

    rafIdRef.current = requestAnimationFrame(() => {
      const container = scrollContainerRef.current;
      if (container) {
        container.scrollTop = savedPosition;
      }
      rafIdRef.current = null;
    });

    return () => {
      if (rafIdRef.current !== null) {
        cancelAnimationFrame(rafIdRef.current);
        rafIdRef.current = null;
      }
    };
  }, [viewKey, scrollContainerRef, enabled]);

  // Save scroll position on unmount.
  // Copy ref to local variable per react-hooks/exhaustive-deps rule.
  const scrollContainerRefCurrent = scrollContainerRef.current;
  useEffect(() => {
    if (!enabled) return;

    // Capture the ref value at effect time for cleanup
    const container = scrollContainerRefCurrent;
    return () => {
      if (container) {
        scrollPositions.set(viewKeyRef.current, container.scrollTop);
      }
    };
  }, [scrollContainerRefCurrent, enabled]);
}

/**
 * Clear all stored scroll positions (useful for testing).
 */
export function clearScrollPositions(): void {
  scrollPositions.clear();
}
