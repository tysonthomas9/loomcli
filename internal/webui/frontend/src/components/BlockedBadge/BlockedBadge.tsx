/**
 * BlockedBadge component.
 * Shows a visual indicator for blocked/blocking issues with count and tooltip.
 * Supports two variants:
 * - "blockedBy": Shows "Blocked by X issues" (for Kanban cards)
 * - "blocks": Shows "Blocks X issues" (for Graph nodes)
 */

import { useState, useCallback, memo } from 'react';

import styles from './BlockedBadge.module.css';

/**
 * Props for the BlockedBadge component.
 */
export interface BlockedBadgeProps {
  /** Number of issues (blocking this one or blocked by this one, depending on variant) */
  count: number;
  /** IDs of related issues (for tooltip) */
  issueIds?: string[];
  /** Variant determines the semantic: "blockedBy" (default) or "blocks" */
  variant?: 'blockedBy' | 'blocks';
  /** Optional click handler */
  onClick?: () => void;
  /** Additional CSS class name */
  className?: string;
}

/**
 * Format issue IDs for tooltip display.
 * Shows first 5, then "and N more..." if there are more.
 */
function formatIssueList(issueIds: string[]): string[] {
  const maxDisplay = 5;
  if (issueIds.length <= maxDisplay) {
    return issueIds;
  }
  const displayed = issueIds.slice(0, maxDisplay);
  const remaining = issueIds.length - maxDisplay;
  return [...displayed, `and ${remaining} more...`];
}

/**
 * BlockedBadge displays a blocked/blocking indicator badge.
 * Shows a red pill with block icon and count.
 * Tooltip shows the list of related issues on hover.
 */
function BlockedBadgeComponent({
  count,
  issueIds = [],
  variant = 'blockedBy',
  onClick,
  className,
}: BlockedBadgeProps): JSX.Element | null {
  const [showTooltip, setShowTooltip] = useState(false);

  const handleShow = useCallback(() => {
    setShowTooltip(true);
  }, []);

  const handleHide = useCallback(() => {
    setShowTooltip(false);
  }, []);

  // Keyboard handler for accessibility
  const handleKeyDown = useCallback(
    (event: React.KeyboardEvent) => {
      if (onClick && (event.key === 'Enter' || event.key === ' ')) {
        event.preventDefault();
        onClick();
      }
    },
    [onClick]
  );

  // Don't render if count is 0
  if (count === 0) {
    return null;
  }

  const rootClassName = className ? `${styles.blockedBadge} ${className}` : styles.blockedBadge;

  const issueList = formatIssueList(issueIds);
  const isBlockedBy = variant === 'blockedBy';
  const ariaLabel = isBlockedBy
    ? `Blocked by ${count} issue${count === 1 ? '' : 's'}`
    : `Blocks ${count} issue${count === 1 ? '' : 's'}`;
  const tooltipHeader = isBlockedBy ? 'Blocked by:' : 'Blocks:';

  return (
    <span
      className={rootClassName}
      onMouseEnter={handleShow}
      onMouseLeave={handleHide}
      onFocus={handleShow}
      onBlur={handleHide}
      onClick={onClick}
      onKeyDown={onClick ? handleKeyDown : undefined}
      tabIndex={0}
      role="button"
      aria-label={ariaLabel}
      data-testid="blocked-badge"
    >
      <span className={styles.icon} aria-hidden="true">
        ⛔
      </span>
      <span className={styles.count}>{count}</span>

      {showTooltip && issueList.length > 0 && (
        <div className={styles.tooltip} role="tooltip">
          <div className={styles.tooltipHeader}>{tooltipHeader}</div>
          <ul className={styles.tooltipList}>
            {issueList.map((id, index) => (
              <li key={index} className={styles.tooltipItem}>
                {id}
              </li>
            ))}
          </ul>
        </div>
      )}
    </span>
  );
}

/**
 * Memoized BlockedBadge component.
 */
export const BlockedBadge = memo(BlockedBadgeComponent);
