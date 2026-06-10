/**
 * IssueCard component for Kanban board.
 * Displays a single issue as a card with title, ID, priority badge, and optional blocked indicator.
 */

import { memo } from "react";

import { BlockedBadge } from "@/components/BlockedBadge";
import { HighlightText } from "@/components/HighlightText";
import { RepoBadge } from "@/components/RepoBadge";
import { TypeIcon } from "@/components/TypeIcon";
import { useHasActiveSession } from "@/contexts/IssueSessionContext";
import { useSearchTerm } from "@/contexts/SearchTermContext";

import { useWorkspaceContext } from "@/hooks/workspace";
import type { BlockerRef, Issue } from "@/types";
import { isKnownIssueType } from "@/types";
import {
  formatIssueId,
  getReviewType,
  isPRUrl,
  type ReviewType,
} from "@/utils/issue";

import styles from "./IssueCard.module.css";

/**
 * Review badge configuration by type — plain text labels per the Aether
 * design's plan-badge (no emoji adornments).
 */
const REVIEW_BADGE_CONFIG: Record<
  ReviewType,
  { label: string; className: string }
> = {
  plan: { label: "Plan", className: styles.reviewPlan ?? "" },
  code: { label: "Code", className: styles.reviewCode ?? "" },
  help: { label: "Help", className: styles.reviewHelp ?? "" },
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
  /** Details of blocking issues with titles (optional) */
  blockedByDetails?: BlockerRef[];
  /** Whether this card is in the Backlog column (dimmed appearance) */
  isBacklog?: boolean;
  /** Column ID this card is displayed in (for conditional rendering) */
  columnId?: string;
  /** Whether this issue has an active terminal session */
  hasActiveSession?: boolean;
}

/**
 * Get priority level, defaulting to 4 (backlog) if undefined or out of range.
 * Exposed on the card only as a data attribute (no visible badge — the Aether
 * design's tickets carry no priority chip).
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
 */
export const IssueCard = memo(function IssueCard({
  issue,
  onClick,
  className,
  blockedByCount,
  blockedBy,
  blockedByDetails,
  isBacklog = false,
  columnId,
  hasActiveSession,
}: IssueCardProps): JSX.Element {
  const { isMultiRepo, isAllSelected } = useWorkspaceContext();
  const searchTerm = useSearchTerm();
  const checkActiveSession = useHasActiveSession();
  const showSessionBadge =
    hasActiveSession !== undefined
      ? hasActiveSession
      : checkActiveSession(issue.id);

  const priority = getPriorityLevel(issue.priority);
  const displayId = formatIssueId(issue.id);
  const displayTitle = issue.title || "Untitled";
  const isBlocked = (blockedByCount ?? 0) > 0;
  const isDeferred = issue.is_deferred === true || issue.status === "deferred";
  const reviewType = getReviewType(issue);

  const rootClassName = className
    ? `${styles.issueCard} ${className}`
    : styles.issueCard;

  const handleClick = () => {
    if (onClick) {
      onClick(issue);
    }
  };

  const handleKeyDown = (event: React.KeyboardEvent) => {
    if (onClick && (event.key === "Enter" || event.key === " ")) {
      event.preventDefault();
      onClick(issue);
    }
  };

  return (
    <article
      className={rootClassName}
      data-priority={priority}
      data-column={columnId}
      data-blocked={isBlocked ? "true" : undefined}
      data-in-backlog={isBacklog ? "true" : undefined}
      onClick={handleClick}
      onKeyDown={handleKeyDown}
      tabIndex={onClick ? 0 : undefined}
      role={onClick ? "button" : undefined}
      aria-label={`Issue: ${displayTitle}${isBlocked ? " (blocked)" : ""}${isBacklog ? " (backlog)" : ""}`}
    >
      <header className={styles.header}>
        <span className={styles.id} title={issue.id}>
          {displayId}
        </span>
        {showSessionBadge && (
          <span
            className={styles.sessionBadge}
            aria-label="Active terminal session"
            title="Active terminal session"
          >
            <svg
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
              <rect x="2" y="3" width="20" height="18" rx="2" />
              <polyline points="8 10 12 14 8 18" />
              <line x1="16" y1="18" x2="16" y2="18.01" />
            </svg>
          </span>
        )}
        {issue.issue_type && isKnownIssueType(issue.issue_type) && (
          <TypeIcon
            type={issue.issue_type}
            size={14}
            className={styles.typeIcon ?? ""}
          />
        )}
        {reviewType && (
          <span
            className={`${styles.reviewTypeBadge} ${REVIEW_BADGE_CONFIG[reviewType].className}`}
            aria-label={`${REVIEW_BADGE_CONFIG[reviewType].label} review`}
          >
            {REVIEW_BADGE_CONFIG[reviewType].label}
          </span>
        )}
        {reviewType === "code" &&
          issue.external_ref &&
          isPRUrl(issue.external_ref) && (
            <a
              className={styles.prLink}
              href={issue.external_ref}
              target="_blank"
              rel="noopener noreferrer"
              onClick={(e) => e.stopPropagation()}
              aria-label="View pull request"
            >
              PR ↗
            </a>
          )}
        {isBlocked && (
          <BlockedBadge
            count={blockedByCount ?? 0}
            {...(blockedBy !== undefined && { issueIds: blockedBy })}
            {...(blockedByDetails !== undefined && {
              issueDetails: blockedByDetails,
            })}
          />
        )}
        {isDeferred && (
          <span className={styles.deferredBadge} aria-label="Deferred">
            Deferred
          </span>
        )}
      </header>
      <h3 className={styles.title}>
        <HighlightText text={displayTitle} searchTerm={searchTerm} />
      </h3>
      {issue.owner && (
        <span
          className={styles.ownerBadge}
          title={`Owner: ${issue.owner}`}
          data-testid="issue-card-owner"
        >
          {issue.owner
            .split(/\s+/)
            .map((w) => w[0])
            .join("")
            .toUpperCase()
            .slice(0, 2)}
        </span>
      )}
      {isMultiRepo && isAllSelected && issue.repo && (
        <div className={styles.cardFooter}>
          <RepoBadge repoName={issue.repo} />
        </div>
      )}
    </article>
  );
});
