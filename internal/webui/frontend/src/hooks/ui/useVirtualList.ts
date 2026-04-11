/**
 * Thin wrapper around @tanstack/react-virtual's useVirtualizer.
 * Provides consistent configuration for both Kanban column and table row virtualization.
 */

import { useVirtualizer, type VirtualItem } from "@tanstack/react-virtual";
import type { RefObject } from "react";

export interface UseVirtualListOptions {
  /** Total number of items */
  count: number;
  /** Ref to the scroll container element */
  scrollContainerRef: RefObject<HTMLElement | null>;
  /** Estimated height of each item in pixels */
  estimatedSize: number;
  /** Number of items to render above/below the visible area */
  overscan?: number;
  /** Whether to measure actual element sizes for variable height items */
  measureElements?: boolean;
}

export interface UseVirtualListReturn {
  /** The virtual items to render */
  virtualItems: VirtualItem[];
  /** Total height of all items (for the spacer element) */
  totalSize: number;
  /** Function to measure an element for dynamic sizing */
  measureElement: (el: HTMLElement | null) => void;
}

/**
 * useVirtualList provides windowed rendering for large lists.
 * Only items visible in the scroll container (plus overscan) are rendered.
 */
export function useVirtualList({
  count,
  scrollContainerRef,
  estimatedSize,
  overscan = 5,
  measureElements = false,
}: UseVirtualListOptions): UseVirtualListReturn {
  const virtualizer = useVirtualizer({
    count,
    getScrollElement: () => scrollContainerRef.current,
    estimateSize: () => estimatedSize,
    overscan,
    ...(measureElements && {
      measureElement: (el: Element) => el.getBoundingClientRect().height,
    }),
  });

  return {
    virtualItems: virtualizer.getVirtualItems(),
    totalSize: virtualizer.getTotalSize(),
    measureElement: measureElements
      ? (el: HTMLElement | null) => {
          if (el) virtualizer.measureElement(el);
        }
      : () => {},
  };
}
