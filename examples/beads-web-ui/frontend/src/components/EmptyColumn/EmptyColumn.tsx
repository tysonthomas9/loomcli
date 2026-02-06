/**
 * EmptyColumn component for Kanban board.
 * Displays a centered message with an icon when a StatusColumn has no issues.
 */

import type { Status } from '@/types';

import styles from './EmptyColumn.module.css';

/**
 * Props for the EmptyColumn component.
 */
export interface EmptyColumnProps {
  /** Status or column ID for contextual messaging (optional) */
  status?: Status | string;
  /** Custom message override */
  message?: string;
  /** Whether to show an icon */
  showIcon?: boolean;
  /** Additional CSS class */
  className?: string;
}

/**
 * Get the default empty state message based on status or column ID.
 * Supports both traditional status values and new column IDs from 5-column layout.
 */
function getDefaultMessage(status?: Status | string): string {
  switch (status) {
    case 'open':
      return 'No open issues';
    case 'in_progress':
      return 'No issues in progress';
    case 'closed':
      return 'No closed issues';
    case 'blocked':
      return 'No blocked issues';
    case 'deferred':
      return 'No deferred issues';
    case 'tombstone':
      return 'No archived issues';
    case 'pinned':
      return 'No pinned issues';
    case 'hooked':
      return 'No hooked issues';
    case 'review':
      return 'No issues in review';
    // New 5-column layout column IDs
    case 'ready':
      return 'No ready issues';
    case 'backlog':
      return 'No blocked or deferred issues';
    case 'done':
      return 'No completed issues';
    default:
      return 'No issues';
  }
}

/**
 * EmptyColumn displays a visual indicator when a StatusColumn contains no issues.
 * Shows a centered icon and message to provide clear feedback about the empty state.
 */
export function EmptyColumn({
  status,
  message,
  showIcon = true,
  className,
}: EmptyColumnProps): JSX.Element {
  const displayMessage = message ?? getDefaultMessage(status);

  const rootClassName = className ? `${styles.emptyColumn} ${className}` : styles.emptyColumn;

  return (
    <div className={rootClassName} role="status" aria-label={displayMessage}>
      {showIcon && (
        <svg className={styles.icon} viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <rect x="3" y="5" width="18" height="14" rx="2" stroke="currentColor" strokeWidth="1.5" />
          <path d="M3 10h18" stroke="currentColor" strokeWidth="1.5" />
        </svg>
      )}
      <p className={styles.message}>{displayMessage}</p>
    </div>
  );
}
