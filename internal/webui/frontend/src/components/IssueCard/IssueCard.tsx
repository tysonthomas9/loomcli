/**
 * IssueCard component for Kanban board.
 * Displays a single issue as a card with title, ID, priority badge, and optional blocked indicator.
 */

import {
  getAvatarColor,
  getStatusDotColor,
  getStatusLabel,
} from "@/components/AgentCard";
import { BlockedBadge } from "@/components/BlockedBadge";
import { useAgentContext } from "@/hooks";
import type { BlockerRef, Issue } from "@/types";
import { parseLoomStatus } from "@/types";
import { formatIssueId } from "@/utils/formatIssueId";
import { getOpenStatus, getReviewType } from "@/utils/issueCategory";
import type { OpenStatus, ReviewType } from "@/utils/issueCategory";

import { AgentRow } from "./AgentRow";
import styles from "./IssueCard.module.css";

/**
 * Review badge configuration by type.
 */
const REVIEW_BADGE_CONFIG: Record<
  ReviewType,
  { icon: string; label: string; className: string }
> = {
  plan: { icon: "📝", label: "Plan", className: styles.reviewPlan ?? "" },
  code: { icon: "🔍", label: "Code", className: styles.reviewCode ?? "" },
  help: { icon: "❓", label: "Help", className: styles.reviewHelp ?? "" },
};

/**
 * Open status badge configuration by type.
 */
const OPEN_BADGE_CONFIG: Record<
  OpenStatus,
  { icon: string; label: string; className: string }
> = {
  needs_plan: {
    icon: "📋",
    label: "Needs Plan",
    className: styles.openNeedsPlan ?? "",
  },
  ready: { icon: "✅", label: "Ready", className: styles.openReady ?? "" },
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
 */
export function IssueCard({
  issue,
  onClick,
  className,
  blockedByCount,
  blockedBy,
  blockedByDetails,
  isBacklog = false,
  columnId,
}: IssueCardProps): JSX.Element {
  const { getAgentByName } = useAgentContext();

  const priority = getPriorityLevel(issue.priority);
  const displayId = formatIssueId(issue.id);
  const displayTitle = issue.title || "Untitled";
  const isBlocked = (blockedByCount ?? 0) > 0;
  const reviewType = getReviewType(issue);
  const openStatus = columnId === "ready" ? getOpenStatus(issue) : null;

  // Compute agent row data for in_progress cards with an assignee
  const showAgentRow = columnId === "in_progress" && !!issue.assignee;
  const assignedAgent = issue.assignee
    ? getAgentByName(issue.assignee)
    : undefined;
  const agentParsedStatus = assignedAgent
    ? parseLoomStatus(assignedAgent.status)
    : null;

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
        {reviewType === "code" && issue.external_ref && (
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
        {openStatus && (
          <span
            className={`${styles.openStatusBadge} ${OPEN_BADGE_CONFIG[openStatus].className}`}
            aria-label={OPEN_BADGE_CONFIG[openStatus].label}
          >
            <span className={styles.openStatusIcon} aria-hidden="true">
              {OPEN_BADGE_CONFIG[openStatus].icon}
            </span>
            {OPEN_BADGE_CONFIG[openStatus].label}
          </span>
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
        {issue.status === "deferred" && (
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
      {showAgentRow && issue.assignee && (
        <AgentRow
          agentName={issue.assignee}
          status={agentParsedStatus}
          avatarColor={getAvatarColor(issue.assignee.replace(/^\[H\]\s*/, ""))}
          dotColor={
            agentParsedStatus
              ? getStatusDotColor(agentParsedStatus.type)
              : undefined
          }
          activity={
            agentParsedStatus && assignedAgent
              ? getStatusLabel(agentParsedStatus)
              : undefined
          }
        />
      )}
    </article>
  );
}
