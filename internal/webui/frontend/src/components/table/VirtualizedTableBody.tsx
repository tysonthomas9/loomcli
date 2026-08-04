/**
 * VirtualizedTableBody replaces the static <tbody> in IssueTable
 * for efficient rendering of large datasets (1000+ rows at 60fps).
 * Uses padding-based approach with spacer rows to maintain scroll height.
 */

import { useVirtualizer } from "@tanstack/react-virtual";
import type { RefObject, ReactNode } from "react";

export interface VirtualizedTableBodyProps {
  /** Total number of rows */
  count: number;
  /** Ref to the scroll container (the .issue-table__wrapper div) */
  scrollContainerRef: RefObject<HTMLElement | null>;
  /** Render function for each visible row — must return a <tr> element */
  renderRow: (index: number) => ReactNode;
  /** Number of columns for the spacer rows' colSpan */
  colSpan: number;
  /** CSS class for the tbody element */
  className?: string;
}

/** Estimated row height in pixels */
const ESTIMATED_ROW_HEIGHT = 45;

/**
 * VirtualizedTableBody renders only visible table rows using spacer rows
 * above/below the visible range to maintain correct scroll height.
 * The renderRow callback must return <tr> elements (e.g., IssueRow).
 */
export function VirtualizedTableBody({
  count,
  scrollContainerRef,
  renderRow,
  colSpan,
  className,
}: VirtualizedTableBodyProps): JSX.Element {
  const virtualizer = useVirtualizer({
    count,
    getScrollElement: () => scrollContainerRef.current,
    estimateSize: () => ESTIMATED_ROW_HEIGHT,
    overscan: 10,
  });

  const virtualItems = virtualizer.getVirtualItems();

  const firstItem = virtualItems[0];
  const lastItem = virtualItems[virtualItems.length - 1];
  const paddingTop = firstItem ? firstItem.start : 0;
  const paddingBottom = lastItem
    ? virtualizer.getTotalSize() - lastItem.end
    : 0;

  return (
    <tbody className={className}>
      {paddingTop > 0 && (
        <tr key="spacer-top" aria-hidden="true">
          <td
            colSpan={colSpan}
            style={{
              height: paddingTop,
              padding: 0,
              border: "none",
              lineHeight: 0,
            }}
          />
        </tr>
      )}
      {virtualItems.map((virtualItem) => renderRow(virtualItem.index))}
      {paddingBottom > 0 && (
        <tr key="spacer-bottom" aria-hidden="true">
          <td
            colSpan={colSpan}
            style={{
              height: paddingBottom,
              padding: 0,
              border: "none",
              lineHeight: 0,
            }}
          />
        </tr>
      )}
    </tbody>
  );
}
