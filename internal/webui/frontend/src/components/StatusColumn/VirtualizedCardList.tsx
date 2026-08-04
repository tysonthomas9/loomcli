/**
 * VirtualizedCardList renders a windowed list of cards within a StatusColumn.
 * Used when a column has >50 cards to maintain smooth scrolling performance.
 * Uses @tanstack/react-virtual for efficient DOM rendering.
 */

import { useVirtualizer } from "@tanstack/react-virtual";
import type { RefObject, ReactNode } from "react";

export interface VirtualizedCardListProps {
  /** Total number of items to virtualize */
  count: number;
  /** Ref to the scroll container (StatusColumn's .content div) */
  scrollContainerRef: RefObject<HTMLElement | null>;
  /** Render function for each visible item */
  renderItem: (index: number) => ReactNode;
  /** Gap between cards in pixels (should match StatusColumn's gap) */
  gap?: number;
}

/** Estimated height of a single IssueCard including gap (px) */
const ESTIMATED_CARD_HEIGHT = 97;

/**
 * VirtualizedCardList renders only the visible cards (plus overscan) using
 * a spacer div for total height and absolute positioning for each card.
 * Gap is folded into each item's measured size so totalSize is accurate.
 */
export function VirtualizedCardList({
  count,
  scrollContainerRef,
  renderItem,
  gap = 16,
}: VirtualizedCardListProps): JSX.Element {
  const virtualizer = useVirtualizer({
    count,
    getScrollElement: () => scrollContainerRef.current,
    estimateSize: () => ESTIMATED_CARD_HEIGHT,
    overscan: 5,
    // Include gap in measured size so totalSize accounts for all gaps
    measureElement: (el: Element) => el.getBoundingClientRect().height + gap,
  });

  const virtualItems = virtualizer.getVirtualItems();
  // Subtract the trailing gap from the last item since there's no card after it
  const totalSize = count > 0 ? virtualizer.getTotalSize() - gap : 0;

  return (
    <div
      style={{
        height: totalSize,
        width: "100%",
        position: "relative",
      }}
    >
      {virtualItems.map((virtualItem) => (
        <div
          key={virtualItem.key}
          ref={(el) => {
            if (el) virtualizer.measureElement(el);
          }}
          data-index={virtualItem.index}
          style={{
            position: "absolute",
            top: 0,
            left: 0,
            width: "100%",
            transform: `translateY(${virtualItem.start}px)`,
          }}
          role="listitem"
        >
          {renderItem(virtualItem.index)}
        </div>
      ))}
    </div>
  );
}
