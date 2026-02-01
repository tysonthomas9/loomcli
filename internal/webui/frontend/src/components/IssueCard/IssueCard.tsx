/**
 * IssueCard component for Kanban board.
 * Displays a single issue as a card with title, ID, priority badge, and optional blocked indicator.
 * When in the Review column, shows Approve/Reject action buttons.
 */

import { useState, useCallback } from 'react';
import type { Issue } from '@/types';
import { BlockedBadge } from '@/components/BlockedBadge';
import { getReviewType } from '@/utils/reviewType';
import type { ReviewType } from '@/utils/reviewType';
import { RejectCommentForm } from './RejectCommentForm';
import styles from './IssueCard.module.css';

/**
 * Review badge configuration by type.
 */
const REVIEW_BADGE_CONFIG: Record<ReviewType, { icon: string; label: string; className: string }> =
  {
    plan: { icon: '📝', label: 'Plan', className: styles.reviewPlan ?? '' },
    code: { icon: '🔍', label: 'Code', className: styles.reviewCode ?? '' },
    help: { icon: '❓', label: 'Help', className: styles.reviewHelp ?? '' },
  };

/**
 * Props for the IssueCard component.
 */
export interface IssueCardProps {
  /** The issue to display */
  issue: Issue;
  /** Callback when card is clicked */
  onClick?: (issue: Issue) => void;
  /** Additional CSS class name */
  className?: string;
  /** Number of issues blocking this one (optional) */
  blockedByCount?: number;
  /** IDs of blocking issues (optional) */
  blockedBy?: string[];
  /** Whether this card is in the Backlog column (dimmed appearance) */
  isBacklog?: boolean;
  /** Column ID this card is displayed in (for conditional rendering) */
  columnId?: string;
  /** Callback when approve button is clicked (only shown in review column). Returns Promise for error handling. */
  onApprove?: (issue: Issue) => void | Promise<void>;
  /** Callback when reject is submitted with comment (only shown in review column). Returns Promise for error handling. */
  onReject?: (issue: Issue, comment: string) => void | Promise<void>;
}

/**
 * Format issue ID for display.
 * Shows last 7 characters of the ID for readability.
 */
function formatIssueId(id: string): string {
  if (!id) return 'unknown';
  // If ID is short enough, return as-is
  if (id.length <= 10) return id;
  // Otherwise show last 7 characters
  return id.slice(-7);
}

/**
 * Get priority level, defaulting to 4 (backlog) if undefined or out of range.
 */
function getPriorityLevel(priority: number | undefined): 0 | 1 | 2 | 3 | 4 {
  if (priority === undefined || priority === null) return 4;
  if (priority < 0) return 4;
  if (priority > 4) return 4;
  return priority as 0 | 1 | 2 | 3 | 4;
}

/**
 * IssueCard displays a single issue in the Kanban board.
 * Shows title, ID, priority badge, and optional blocked indicator.
 * When in the Review column, shows Approve/Reject action buttons.
 */
export function IssueCard({
  issue,
  onClick,
  className,
  blockedByCount,
  blockedBy,
  isBacklog = false,
  columnId,
  onApprove,
  onReject,
}: IssueCardProps): JSX.Element {
  const [showRejectForm, setShowRejectForm] = useState(false);
  const [isApproving, setIsApproving] = useState(false);
  const [isRejecting, setIsRejecting] = useState(false);
  const [rejectError, setRejectError] = useState<string | null>(null);

  const priority = getPriorityLevel(issue.priority);
  const displayId = formatIssueId(issue.id);
  const displayTitle = issue.title || 'Untitled';
  const isBlocked = (blockedByCount ?? 0) > 0;
  const reviewType = getReviewType(issue);

  // Show action buttons only in review column with callbacks provided
  const showReviewActions =
    columnId === 'review' && (onApprove !== undefined || onReject !== undefined);

  const rootClassName = className ? `${styles.issueCard} ${className}` : styles.issueCard;

  const handleClick = () => {
    if (onClick) {
      onClick(issue);
    }
  };

  const handleKeyDown = (event: React.KeyboardEvent) => {
    if (onClick && (event.key === 'Enter' || event.key === ' ')) {
      event.preventDefault();
      onClick(issue);
    }
  };

  const handleApproveClick = useCallback(
    async (e: React.MouseEvent) => {
      e.stopPropagation();
      if (!onApprove || isApproving) return;
      setIsApproving(true);
      try {
        // Call onApprove - if it returns a promise, await it for error handling
        await onApprove(issue);
        // On success, card will move/re-render due to status change
      } catch {
        // On error, reset approving state so user can retry
        setIsApproving(false);
      }
    },
    [onApprove, issue, isApproving]
  );

  const handleRejectClick = useCallback((e: React.MouseEvent) => {
    e.stopPropagation();
    setShowRejectForm(true);
    setRejectError(null);
  }, []);

  const handleRejectCancel = useCallback(() => {
    // Don't allow cancel while submitting
    if (isRejecting) return;
    setShowRejectForm(false);
    setRejectError(null);
  }, [isRejecting]);

  const handleRejectSubmit = useCallback(
    async (comment: string) => {
      if (!onReject || isRejecting) return;
      setIsRejecting(true);
      setRejectError(null);
      try {
        // Call onReject - if it returns a promise, await it for error handling
        await onReject(issue, comment);
        // On success, card will move/re-render due to status change
      } catch (err) {
        // On error, reset rejecting state and show error so user can retry
        setIsRejecting(false);
        const message = err instanceof Error ? err.message : 'Failed to reject';
        setRejectError(message);
      }
    },
    [onReject, issue, isRejecting]
  );

  return (
    <article
      className={rootClassName}
      data-priority={priority}
      data-column={columnId}
      data-blocked={isBlocked ? 'true' : undefined}
      data-in-backlog={isBacklog ? 'true' : undefined}
      onClick={handleClick}
      onKeyDown={handleKeyDown}
      tabIndex={onClick ? 0 : undefined}
      role={onClick ? 'button' : undefined}
      aria-label={`Issue: ${displayTitle}${isBlocked ? ' (blocked)' : ''}${isBacklog ? ' (backlog)' : ''}`}
    >
      <header className={styles.header}>
        <span className={styles.id}>{displayId}</span>
        {reviewType && (
          <span
            className={`${styles.reviewTypeBadge} ${REVIEW_BADGE_CONFIG[reviewType].className}`}
            aria-label={`${REVIEW_BADGE_CONFIG[reviewType].label} review`}
          >
            <span className={styles.reviewIcon} aria-hidden="true">
              {REVIEW_BADGE_CONFIG[reviewType].icon}
            </span>
            {REVIEW_BADGE_CONFIG[reviewType].label}
          </span>
        )}
        {isBlocked && (
          <BlockedBadge
            count={blockedByCount ?? 0}
            {...(blockedBy !== undefined && { issueIds: blockedBy })}
          />
        )}
        {issue.status === 'deferred' && (
          <span className={styles.deferredBadge} aria-label="Deferred">
            <span aria-hidden="true">⏸</span> Deferred
          </span>
        )}
        <span
          className={`${styles.priorityBadge} ${styles[`priority${priority}`]}`}
          data-priority={priority}
          aria-label={`Priority ${priority}`}
        >
          P{priority}
        </span>
      </header>
      <h3 className={styles.title}>{displayTitle}</h3>
      {showReviewActions && !showRejectForm && (
        <div className={styles.reviewActions}>
          {onApprove && (
            <button
              type="button"
              className={styles.approveButton}
              onClick={handleApproveClick}
              disabled={isApproving}
              aria-label="Approve"
              data-testid="approve-button"
            >
              {isApproving ? '...' : '✓'} Approve
            </button>
          )}
          {onReject && (
            <button
              type="button"
              className={styles.rejectButton}
              onClick={handleRejectClick}
              aria-label="Reject"
              data-testid="reject-button"
            >
              ✗ Reject
            </button>
          )}
        </div>
      )}
      {showRejectForm && onReject && (
        <RejectCommentForm
          issueId={issue.id}
          onSubmit={handleRejectSubmit}
          onCancel={handleRejectCancel}
          isSubmitting={isRejecting}
          error={rejectError}
        />
      )}
    </article>
  );
}
