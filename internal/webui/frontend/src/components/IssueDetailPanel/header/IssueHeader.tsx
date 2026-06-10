/**
 * IssueHeader component.
 * Header area with ID, status badge, priority, close button, and title for IssueDetailPanel.
 */

import type { Issue, IssueDetails, Priority } from "@/types";
import type { Status } from "@/types/issue";

import { EditableTitle } from "@/components/EditableTitle";
import { formatStatusLabel } from "@/utils/issue";
import { StatusDropdown } from "@/components/StatusDropdown";
import styles from "./IssueHeader.module.css";

/**
 * Priority display info.
 */
const PRIORITY_LABELS: Record<number, { short: string; full: string }> = {
  0: { short: "P0", full: "Critical" },
  1: { short: "P1", full: "High" },
  2: { short: "P2", full: "Medium" },
  3: { short: "P3", full: "Normal" },
  4: { short: "P4", full: "Backlog" },
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
  /** Callback when copy-link button is clicked */
  onCopyLink?: () => void;
  /** Callback when move button is clicked */
  onMove?: () => void;
  /** Full PR URL (e.g., https://github.com/owner/repo/pull/42) */
  prUrl?: string;
  /** Extracted PR number (e.g., "42") */
  prNumber?: string;
  /** Whether the panel is currently maximized to full-page */
  isMaximized?: boolean;
  /** Toggle the panel between slide-over and full-page (omit to hide control) */
  onToggleMaximize?: () => void;
  /** Enable sticky mode styling */
  sticky?: boolean;
  /** Additional CSS class name */
  className?: string;
}

/**
 * Format status with fallback to 'Open'.
 */
function formatStatus(status?: string): string {
  if (!status) return "Open";
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
  onCopyLink,
  onMove,
  prUrl,
  prNumber,
  isMaximized,
  onToggleMaximize,
  sticky,
  className,
}: IssueHeaderProps): JSX.Element {
  const rootClassName = [styles.issueHeader, sticky && styles.sticky, className]
    .filter(Boolean)
    .join(" ");

  const priority = issue.priority as Priority;
  const defaultPriorityInfo = { short: "P2", full: "Medium" };
  const priorityInfo = PRIORITY_LABELS[priority] ?? defaultPriorityInfo;

  return (
    <header className={rootClassName} data-testid="issue-header">
      <div className={styles.topRow}>
        <span className={styles.issueId} data-testid="issue-id">
          {issue.id}
        </span>
        {onStatusChange ? (
          <StatusDropdown
            status={issue.status ?? "open"}
            onStatusChange={onStatusChange}
            isSaving={isSavingStatus ?? false}
          />
        ) : (
          <span
            className={styles.statusBadge}
            data-status={issue.status ?? "open"}
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
        {prUrl && prNumber && (
          <>
            <a
              className={styles.prViewLink}
              href={prUrl}
              target="_blank"
              rel="noopener noreferrer"
              aria-label={`View pull request #${prNumber}`}
              data-testid="header-pr-view-link"
              onClick={(e) => e.stopPropagation()}
            >
              ↗ #{prNumber}
            </a>
            <a
              className={styles.prMergeLink}
              href={prUrl}
              target="_blank"
              rel="noopener noreferrer"
              aria-label={`Merge pull request #${prNumber}`}
              data-testid="header-pr-merge-link"
              onClick={(e) => e.stopPropagation()}
            >
              → merge #{prNumber}
            </a>
          </>
        )}
        {onCopyLink && (
          <button
            type="button"
            className={styles.copyLinkButton}
            onClick={onCopyLink}
            aria-label="Copy link"
            data-testid="header-copy-link-button"
          >
            <svg
              width="20"
              height="20"
              viewBox="0 0 20 20"
              fill="none"
              aria-hidden="true"
            >
              <path
                d="M8.5 11.5l3-3M12 8a2.75 2.75 0 0 1 0 3.89l-2 2A2.75 2.75 0 0 1 6.11 10M8 12a2.75 2.75 0 0 1 0-3.89l2-2A2.75 2.75 0 0 1 13.89 10"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            </svg>
          </button>
        )}
        {onMove && (
          <button
            type="button"
            className={styles.moveButton}
            onClick={onMove}
            aria-label="Move to workspace"
            data-testid="header-move-button"
          >
            <svg
              width="20"
              height="20"
              viewBox="0 0 20 20"
              fill="none"
              aria-hidden="true"
            >
              <path
                d="M4 10h12M12 6l4 4-4 4"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
                strokeLinejoin="round"
              />
            </svg>
          </button>
        )}
        {onToggleMaximize && (
          <button
            type="button"
            className={styles.maximizeButton}
            onClick={onToggleMaximize}
            aria-label={isMaximized ? "Exit full screen" : "Expand to full screen"}
            aria-pressed={isMaximized}
            data-testid="header-maximize-button"
          >
            {isMaximized ? (
              <svg
                width="18"
                height="18"
                viewBox="0 0 16 16"
                fill="none"
                aria-hidden="true"
              >
                <path
                  d="M4 10H2v4h4v-2H4v-2zM2 6h2V4h2V2H2v4zm10 6h-2v2h4v-4h-2v2zM10 2v2h2v2h2V2h-4z"
                  fill="currentColor"
                />
              </svg>
            ) : (
              <svg
                width="18"
                height="18"
                viewBox="0 0 16 16"
                fill="none"
                aria-hidden="true"
              >
                <path
                  d="M2 10h2v2h2v2H2v-4zm2-4H2V2h4v2H4v2zm8 6h-2v2h4v-4h-2v2zM10 2v2h2v2h2V2h-4z"
                  fill="currentColor"
                />
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
          <svg
            width="20"
            height="20"
            viewBox="0 0 20 20"
            fill="none"
            aria-hidden="true"
          >
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
