/**
 * IssueHeader component.
 * Header area with ID, status badge, priority, and close button for IssueDetailPanel.
 */

import type { Issue, IssueDetails, Priority } from '@/types';
import type { Status } from '@/types/status';
import { EditableTitle } from '../EditableTitle';
import { StatusDropdown } from '../StatusDropdown';
import { formatStatusLabel } from '../StatusColumn/utils';
import styles from './IssueHeader.module.css';

/**
 * Priority display info.
 */
const PRIORITY_LABELS: Record<number, { short: string; full: string }> = {
  0: { short: 'P0', full: 'Critical' },
  1: { short: 'P1', full: 'High' },
  2: { short: 'P2', full: 'Medium' },
  3: { short: 'P3', full: 'Normal' },
  4: { short: 'P4', full: 'Backlog' },
};

/**
 * Props for the IssueHeader component.
 */
export interface IssueHeaderProps {
  /** The issue to display */
  issue: Issue | IssueDetails;
  /** Callback when close button is clicked */
  onClose: () => void;
  /** Callback when title is saved */
  onTitleSave?: (newTitle: string) => Promise<void>;
  /** Whether title is being saved */
  isSavingTitle?: boolean;
  /** Callback when status changes (enables interactive dropdown) */
  onStatusChange?: (status: Status) => Promise<void>;
  /** Whether status is being saved */
  isSavingStatus?: boolean;
  /** Whether to show priority badge in header */
  showPriority?: boolean;
  /** Callback when priority badge is clicked */
  onPriorityClick?: () => void;
  /** Enable sticky mode styling */
  sticky?: boolean;
  /** Additional CSS class name */
  className?: string;
  /** Whether this issue is a review item (shows approve/reject buttons) */
  isReviewItem?: boolean;
  /** Callback when approve button is clicked */
  onApprove?: () => void;
  /** Callback when reject button is clicked */
  onReject?: () => void;
  /** Whether approve action is in progress */
  isApproving?: boolean;
  /** Whether the panel is in fullscreen mode */
  isFullscreen?: boolean;
  /** Callback to toggle fullscreen mode */
  onToggleFullscreen?: () => void;
}

/**
 * Format status with fallback to 'Open'.
 */
function formatStatus(status?: string): string {
  if (!status) return 'Open';
  return formatStatusLabel(status);
}

/**
 * IssueHeader displays the issue identification elements in a cohesive header.
 * Contains:
 * - Issue ID
 * - Status badge with semantic colors
 * - Priority badge (optional)
 * - Close button
 * - Title (editable when onTitleSave provided)
 */
export function IssueHeader({
  issue,
  onClose,
  onTitleSave,
  isSavingTitle,
  onStatusChange,
  isSavingStatus,
  showPriority,
  onPriorityClick,
  sticky,
  className,
  isReviewItem,
  onApprove,
  onReject,
  isApproving,
  isFullscreen,
  onToggleFullscreen,
}: IssueHeaderProps): JSX.Element {
  const rootClassName = [
    styles.issueHeader,
    sticky && styles.sticky,
    className,
  ].filter(Boolean).join(' ');

  const priority = issue.priority as Priority;
  const defaultPriorityInfo = { short: 'P2', full: 'Medium' };
  const priorityInfo = PRIORITY_LABELS[priority] ?? defaultPriorityInfo;

  return (
    <header className={rootClassName} data-testid="issue-header">
      <div className={styles.topRow}>
        <span className={styles.issueId} data-testid="issue-id">
          {issue.id}
        </span>
        {onStatusChange ? (
          <StatusDropdown
            status={issue.status ?? 'open'}
            onStatusChange={onStatusChange}
            isSaving={isSavingStatus ?? false}
          />
        ) : (
          <span
            className={styles.statusBadge}
            data-status={issue.status ?? 'open'}
            role="status"
            data-testid="issue-status-badge"
          >
            {formatStatus(issue.status)}
          </span>
        )}
        {showPriority && (
          <button
            type="button"
            className={styles.priorityBadge}
            data-priority={priority}
            onClick={onPriorityClick}
            aria-label={`Priority: ${priorityInfo.short} - ${priorityInfo.full}`}
            data-testid="header-priority-badge"
          >
            {priorityInfo.short}
          </button>
        )}
        {isReviewItem && (onApprove || onReject) && (
          <div className={styles.reviewActions} data-testid="header-review-actions">
            {onApprove && (
              <button
                type="button"
                className={styles.approveButton}
                onClick={onApprove}
                disabled={isApproving}
                aria-label="Approve"
                data-testid="header-approve-button"
              >
                {isApproving ? '...' : '\u2713'}
              </button>
            )}
            {onReject && (
              <button
                type="button"
                className={styles.rejectButton}
                onClick={onReject}
                aria-label="Reject"
                data-testid="header-reject-button"
              >
                {'\u2717'}
              </button>
            )}
          </div>
        )}
        {onToggleFullscreen && (
          <button
            type="button"
            className={styles.fullscreenButton}
            onClick={onToggleFullscreen}
            aria-label={isFullscreen ? 'Collapse to panel' : 'Expand to fullscreen'}
            data-testid="header-fullscreen-button"
          >
            {isFullscreen ? (
              <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
                <path d="M10 2v4h4M6 14v-4H2M10 6L14 2M6 10l-4 4" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
            ) : (
              <svg width="16" height="16" viewBox="0 0 16 16" fill="none" aria-hidden="true">
                <path d="M14 2h-4M14 2v4M14 2l-4 4M2 14h4M2 14v-4M2 14l4-4" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
              </svg>
            )}
          </button>
        )}
        <button
          className={styles.closeButton}
          onClick={onClose}
          aria-label="Close panel"
          data-testid="header-close-button"
        >
          <svg width="20" height="20" viewBox="0 0 20 20" fill="none" aria-hidden="true">
            <path
              d="M15 5L5 15M5 5l10 10"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
            />
          </svg>
        </button>
      </div>
      {onTitleSave ? (
        <EditableTitle
          title={issue.title}
          onSave={onTitleSave}
          isSaving={isSavingTitle ?? false}
        />
      ) : (
        <h2 className={styles.title} data-testid="issue-title">
          {issue.title}
        </h2>
      )}
    </header>
  );
}
