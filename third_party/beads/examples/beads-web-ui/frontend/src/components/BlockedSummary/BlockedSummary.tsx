/**
 * BlockedSummary component displays a header badge showing blocked issues count
 * with an expandable dropdown listing the blocked issues for quick navigation.
 */

import { useState, useEffect, useRef, useCallback } from 'react';

import { useBlockedIssues } from '@/hooks';
import type { Priority } from '@/types';

import styles from './BlockedSummary.module.css';

/**
 * Props for the BlockedSummary component.
 */
export interface BlockedSummaryProps {
  /** Callback when a blocked issue is clicked */
  onIssueClick?: (issueId: string) => void;
  /** Optional callback when badge is clicked (for analytics) */
  onBadgeClick?: () => void;
  /** Additional CSS class name */
  className?: string;
  /** Maximum issues to show in dropdown before "and N more" */
  maxDisplayed?: number;
}

/**
 * Priority badge component for displaying priority in dropdown items.
 */
function PriorityBadge({ priority }: { priority: Priority }): JSX.Element {
  return (
    <span className={styles.priorityBadge} data-priority={priority}>
      P{priority}
    </span>
  );
}

/**
 * BlockedSummary displays a header badge showing blocked issues count
 * with an expandable dropdown listing the blocked issues.
 */
export function BlockedSummary({
  onIssueClick,
  onBadgeClick,
  className,
  maxDisplayed = 10,
}: BlockedSummaryProps): JSX.Element {
  const [isOpen, setIsOpen] = useState(false);
  const containerRef = useRef<HTMLDivElement>(null);

  const {
    data: blockedIssues,
    loading,
    error,
  } = useBlockedIssues({
    pollInterval: 30000, // Poll every 30 seconds
  });

  const blockedCount = blockedIssues?.length ?? 0;
  const hasBlocked = blockedCount > 0;

  // Handle click outside to close dropdown
  useEffect(() => {
    if (!isOpen) return;

    function handleClickOutside(event: MouseEvent) {
      if (containerRef.current && !containerRef.current.contains(event.target as Node)) {
        setIsOpen(false);
      }
    }

    document.addEventListener('mousedown', handleClickOutside);
    return () => {
      document.removeEventListener('mousedown', handleClickOutside);
    };
  }, [isOpen]);

  // Handle keyboard navigation
  const handleKeyDown = useCallback(
    (event: React.KeyboardEvent) => {
      if (event.key === 'Escape') {
        setIsOpen(false);
      } else if (event.key === 'Enter' || event.key === ' ') {
        event.preventDefault();
        setIsOpen((prev) => !prev);
        onBadgeClick?.();
      }
    },
    [onBadgeClick]
  );

  const handleBadgeClick = useCallback(() => {
    setIsOpen((prev) => !prev);
    onBadgeClick?.();
  }, [onBadgeClick]);

  const handleIssueClick = useCallback(
    (issueId: string) => {
      onIssueClick?.(issueId);
      setIsOpen(false);
    },
    [onIssueClick]
  );

  // Truncate title for display
  const truncateTitle = (title: string, maxLength: number = 30): string => {
    if (title.length <= maxLength) return title;
    return title.slice(0, maxLength - 1) + '…';
  };

  const rootClassName = [styles.blockedSummary, className].filter(Boolean).join(' ');

  const displayedIssues = blockedIssues?.slice(0, maxDisplayed) ?? [];
  const remainingCount = blockedCount - maxDisplayed;

  return (
    <div ref={containerRef} className={rootClassName}>
      <button
        type="button"
        className={styles.badge}
        onClick={handleBadgeClick}
        onKeyDown={handleKeyDown}
        aria-expanded={isOpen}
        aria-haspopup="menu"
        aria-label={`${blockedCount} blocked issues`}
        data-has-blocked={hasBlocked ? 'true' : 'false'}
      >
        <svg
          className={styles.icon}
          width="14"
          height="14"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          strokeLinecap="round"
          strokeLinejoin="round"
          aria-hidden="true"
        >
          <circle cx="12" cy="12" r="10" />
          <path d="M4.93 4.93l14.14 14.14" />
        </svg>
        <span className={styles.count}>{blockedCount}</span>
      </button>

      {isOpen && (
        <div className={styles.dropdown} role="menu" aria-label="Blocked issues list">
          <div className={styles.header}>
            {blockedCount} Blocked Issue{blockedCount !== 1 ? 's' : ''}
          </div>

          <div className={styles.list}>
            {loading && !blockedIssues && <div className={styles.loading}>Loading...</div>}

            {error && <div className={styles.error}>Failed to load blocked issues</div>}

            {!loading && !error && blockedCount === 0 && (
              <div className={styles.empty}>No blocked issues</div>
            )}

            {displayedIssues.map((issue) => (
              <button
                key={issue.id}
                type="button"
                className={styles.issueItem}
                onClick={() => handleIssueClick(issue.id)}
                role="menuitem"
              >
                <div className={styles.issueHeader}>
                  <span className={styles.issueId}>{issue.id}</span>
                  <span className={styles.issueTitle}>{truncateTitle(issue.title)}</span>
                  <PriorityBadge priority={issue.priority} />
                </div>
                <div className={styles.blockedByText}>
                  Blocked by {issue.blocked_by_count} issue
                  {issue.blocked_by_count !== 1 ? 's' : ''}
                </div>
              </button>
            ))}

            {remainingCount > 0 && (
              <div className={styles.moreText}>and {remainingCount} more...</div>
            )}
          </div>

          {blockedCount > 0 && (
            <button
              type="button"
              className={styles.footer}
              onClick={() => {
                // This would typically trigger a filter to show all blocked
                onIssueClick?.('__show_all_blocked__');
                setIsOpen(false);
              }}
              role="menuitem"
            >
              → Show all blocked
            </button>
          )}
        </div>
      )}
    </div>
  );
}
