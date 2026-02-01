/**
 * BlockedCell component for rendering the blocked column cell content.
 * Shows "⛔ N" badge when blocked, "—" when not blocked.
 * Includes hover tooltip showing first 5 blocker IDs.
 */

import type { MouseEvent } from 'react';

/**
 * Props for BlockedCell component.
 */
export interface BlockedCellProps {
  /** Number of issues blocking this issue */
  blockedByCount: number;
  /** Array of blocking issue IDs */
  blockedBy?: string[] | undefined;
  /** Click handler for opening detail panel */
  onClick?: (() => void) | undefined;
}

/**
 * BlockedCell renders the blocked column cell content.
 * Shows a badge with count when blocked, dash when not.
 */
export function BlockedCell({
  blockedByCount,
  blockedBy = [],
  onClick,
}: BlockedCellProps) {
  // Not blocked - show dash
  if (blockedByCount === 0) {
    return <span className="issue-table__blocked issue-table__blocked--none">—</span>;
  }

  // Build tooltip text (defensive for undefined blockedBy)
  const maxTooltipItems = 5;
  const blockerList = blockedBy ?? [];
  const displayedBlockers = blockerList.slice(0, maxTooltipItems);
  const remaining = blockerList.length - maxTooltipItems;

  let tooltipText = 'Blocked by:\n';
  tooltipText += displayedBlockers.map((id) => `• ${id}`).join('\n');
  if (remaining > 0) {
    tooltipText += `\nand ${remaining} more...`;
  }

  const handleClick = (e: MouseEvent<HTMLButtonElement>) => {
    e.stopPropagation(); // Prevent row click
    onClick?.();
  };

  // Format count (show 99+ for large numbers)
  const displayCount = blockedByCount > 99 ? '99+' : blockedByCount;

  return (
    <button
      type="button"
      className="issue-table__blocked issue-table__blocked--active"
      title={tooltipText}
      onClick={handleClick}
      aria-label={`Blocked by ${blockedByCount} issue${blockedByCount === 1 ? '' : 's'}`}
    >
      <span className="issue-table__blocked-icon" aria-hidden="true">⛔</span>
      <span className="issue-table__blocked-count">{displayCount}</span>
    </button>
  );
}

export default BlockedCell;
