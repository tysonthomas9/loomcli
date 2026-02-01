/**
 * NodeTooltip component for displaying issue preview on graph node hover.
 *
 * Shows issue ID, title, status, priority, description preview, and assignee
 * when hovering over nodes in the dependency graph.
 */

import { memo, useMemo } from 'react';
import type { Issue } from '@/types';
import styles from './NodeTooltip.module.css';

/**
 * Position for the tooltip relative to the viewport.
 */
export interface TooltipPosition {
  x: number;
  y: number;
}

/**
 * Props for the NodeTooltip component.
 */
export interface NodeTooltipProps {
  /** Issue data to display (null hides tooltip) */
  issue: Issue | null;
  /** Screen coordinates for positioning */
  position: TooltipPosition | null;
  /** Additional CSS class name */
  className?: string;
}

/**
 * Format issue ID for display (last 7 chars if long).
 */
function formatIssueId(id: string): string {
  if (!id) return 'unknown';
  if (id.length <= 10) return id;
  return id.slice(-7);
}

/**
 * Get priority label from numeric value.
 */
function getPriorityLabel(priority: number | undefined): string {
  if (priority === undefined || priority === null) return 'P4';
  if (priority < 0 || priority > 4) return 'P4';
  return `P${priority}`;
}

/**
 * Get status display text.
 */
function getStatusDisplay(status: string | undefined): string {
  if (!status) return 'Open';
  switch (status) {
    case 'in_progress':
      return 'In Progress';
    case 'open':
      return 'Open';
    case 'closed':
      return 'Closed';
    case 'blocked':
      return 'Blocked';
    default:
      return status.charAt(0).toUpperCase() + status.slice(1);
  }
}

/**
 * Calculate adjusted tooltip position to keep within viewport bounds.
 */
function calculateAdjustedPosition(
  position: TooltipPosition
): { x: number; y: number; flipX: boolean; flipY: boolean } {
  if (typeof window === 'undefined') {
    return { x: position.x, y: position.y, flipX: false, flipY: false };
  }

  const TOOLTIP_WIDTH = 280;
  const TOOLTIP_HEIGHT = 200; // Approximate max height
  const OFFSET = 16;
  const MARGIN = 8;

  // Check if tooltip would go off right edge
  const flipX = position.x + TOOLTIP_WIDTH + OFFSET + MARGIN > window.innerWidth;
  // Check if tooltip would go off bottom edge
  const flipY = position.y + TOOLTIP_HEIGHT + OFFSET + MARGIN > window.innerHeight;

  const x = flipX ? position.x - OFFSET : position.x + OFFSET;
  const y = flipY ? position.y - OFFSET : position.y + OFFSET;

  return { x, y, flipX, flipY };
}

/**
 * NodeTooltip renders a preview of issue information on hover.
 */
function NodeTooltipComponent({
  issue,
  position,
  className,
}: NodeTooltipProps): JSX.Element | null {
  // Calculate adjusted position with memoization
  const adjustedPosition = useMemo(() => {
    if (!position) return null;
    return calculateAdjustedPosition(position);
  }, [position]);

  // Don't render if no issue or position
  if (!issue || !position || !adjustedPosition) {
    return null;
  }

  const displayId = formatIssueId(issue.id);
  const displayTitle = issue.title || 'Untitled';
  const priorityLabel = getPriorityLabel(issue.priority);
  const statusDisplay = getStatusDisplay(issue.status);
  // Ensure priority is in valid range (0-4) for CSS data-priority attribute
  const rawPriority = issue.priority ?? 4;
  const priority = rawPriority < 0 || rawPriority > 4 ? 4 : rawPriority;

  const rootClassName = className
    ? `${styles.nodeTooltip} ${className}`
    : styles.nodeTooltip;

  return (
    <div
      className={rootClassName}
      style={{
        left: adjustedPosition.x,
        top: adjustedPosition.y,
        transform: `translate(${adjustedPosition.flipX ? '-100%' : '0'}, ${adjustedPosition.flipY ? '-100%' : '0'})`,
      }}
      data-testid="node-tooltip"
      role="tooltip"
      aria-hidden="true"
    >
      {/* Issue ID */}
      <span className={styles.issueId}>{displayId}</span>

      {/* Title */}
      <h4 className={styles.title}>{displayTitle}</h4>

      {/* Status and Priority badges */}
      <div className={styles.badges}>
        <span className={styles.statusBadge} data-status={issue.status || 'open'}>
          {statusDisplay}
        </span>
        <span
          className={styles.priorityBadge}
          data-priority={priority}
        >
          {priorityLabel}
        </span>
      </div>

      {/* Description preview */}
      <p className={styles.description}>
        {issue.description || <span className={styles.noDescription}>No description</span>}
      </p>

      {/* Assignee */}
      {issue.assignee && (
        <div className={styles.assignee}>
          <span className={styles.assigneeLabel}>Assignee:</span>
          <span className={styles.assigneeValue}>{issue.assignee}</span>
        </div>
      )}
    </div>
  );
}

/**
 * Memoized NodeTooltip to prevent re-renders during pan/zoom.
 */
export const NodeTooltip = memo(NodeTooltipComponent);
