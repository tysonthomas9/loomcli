/**
 * BlockedBadge component.
 * Shows a visual indicator for blocked/blocking issues with count and tooltip.
 * Supports two variants:
 * - "blockedBy": Shows "Blocked by X issues" (for Kanban cards)
 * - "blocks": Shows "Blocks X issues" (for Graph nodes)
 */

import { useState, useCallback, useRef, useLayoutEffect, useEffect, memo } from 'react';
import { createPortal } from 'react-dom';

import type { BlockerRef } from '@/types';

import styles from './BlockedBadge.module.css';

/**
 * Props for the BlockedBadge component.
 */
export interface BlockedBadgeProps {
  /** Number of issues (blocking this one or blocked by this one, depending on variant) */
  count: number;
  /** IDs of related issues (for tooltip) */
  issueIds?: string[];
  /** Detailed blocker info with titles (preferred over issueIds when available) */
  issueDetails?: BlockerRef[];
  /** Variant determines the semantic: "blockedBy" (default) or "blocks" */
  variant?: 'blockedBy' | 'blocks';
  /** Optional click handler */
  onClick?: () => void;
  /** Additional CSS class name */
  className?: string;
}

/**
 * Truncate a string to maxLen chars with ellipsis.
 */
function truncate(str: string, maxLen: number): string {
  if (str.length <= maxLen) return str;
  return str.slice(0, maxLen) + '...';
}

/**
 * Format issue details for tooltip display.
 * Shows "id: title" when details are available, otherwise just IDs.
 * Shows first 5, then "and N more..." if there are more.
 */
function formatIssueList(issueIds: string[], issueDetails?: BlockerRef[]): string[] {
  const maxDisplay = 5;

  // Build display strings: prefer details with titles when available
  let items: string[];
  if (issueDetails && issueDetails.length > 0) {
    items = issueDetails.map((d) =>
      d.title ? `${d.id}: ${truncate(d.title, 45)}` : d.id
    );
  } else {
    items = issueIds;
  }

  if (items.length <= maxDisplay) {
    return items;
  }
  const displayed = items.slice(0, maxDisplay);
  const remaining = items.length - maxDisplay;
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
  issueDetails,
  variant = 'blockedBy',
  onClick,
  className,
}: BlockedBadgeProps): JSX.Element | null {
  const [showTooltip, setShowTooltip] = useState(false);
  const badgeRef = useRef<HTMLSpanElement>(null);
  const tooltipRef = useRef<HTMLDivElement>(null);
  const [tooltipPos, setTooltipPos] = useState({ top: 0, left: 0, arrowLeft: 0, ready: false });

  const handleShow = useCallback(() => {
    setShowTooltip(true);
  }, []);

  const handleHide = useCallback(() => {
    setShowTooltip(false);
    setTooltipPos((prev) => ({ ...prev, ready: false }));
  }, []);

  // Position the tooltip relative to the badge using fixed coordinates
  useLayoutEffect(() => {
    if (!showTooltip || !badgeRef.current || !tooltipRef.current) return;

    const badgeRect = badgeRef.current.getBoundingClientRect();
    const tooltipRect = tooltipRef.current.getBoundingClientRect();
    const viewportWidth = window.innerWidth;
    const margin = 8;
    const gap = 8;

    const top = badgeRect.bottom + gap;
    const badgeCenterX = badgeRect.left + badgeRect.width / 2;
    let left = badgeCenterX - tooltipRect.width / 2;

    // Clamp to viewport edges
    left = Math.max(margin, Math.min(left, viewportWidth - tooltipRect.width - margin));

    // Clamp arrow to stay within tooltip bounds
    const arrowPad = 12; // 6px border width on each side
    const arrowLeft = Math.max(arrowPad, Math.min(badgeCenterX - left, tooltipRect.width - arrowPad));

    setTooltipPos({ top, left, arrowLeft, ready: true });
  }, [showTooltip]);

  // Close tooltip on scroll/resize to avoid stale positions
  useEffect(() => {
    if (!showTooltip) return;

    const close = () => setShowTooltip(false);
    window.addEventListener('scroll', close, true);
    window.addEventListener('resize', close);
    return () => {
      window.removeEventListener('scroll', close, true);
      window.removeEventListener('resize', close);
    };
  }, [showTooltip]);

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

  const issueList = formatIssueList(issueIds, issueDetails);
  const isBlockedBy = variant === 'blockedBy';
  const ariaLabel = isBlockedBy
    ? `Blocked by ${count} issue${count === 1 ? '' : 's'}`
    : `Blocks ${count} issue${count === 1 ? '' : 's'}`;
  const tooltipHeader = isBlockedBy ? 'Blocked by:' : 'Blocks:';

  return (
    <span
      ref={badgeRef}
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

      {showTooltip && issueList.length > 0 && createPortal(
        <div
          ref={tooltipRef}
          className={styles.tooltip}
          role="tooltip"
          style={{
            top: tooltipPos.top,
            left: tooltipPos.left,
            visibility: tooltipPos.ready ? 'visible' : 'hidden',
            '--arrow-left': `${tooltipPos.arrowLeft}px`,
          } as React.CSSProperties}
        >
          <div className={styles.tooltipHeader}>{tooltipHeader}</div>
          <ul className={styles.tooltipList}>
            {issueList.map((id, index) => (
              <li key={index} className={styles.tooltipItem}>
                {id}
              </li>
            ))}
          </ul>
        </div>,
        document.body
      )}
    </span>
  );
}

/**
 * Memoized BlockedBadge component.
 */
export const BlockedBadge = memo(BlockedBadgeComponent);
