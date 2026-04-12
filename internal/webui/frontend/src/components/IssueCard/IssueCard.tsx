/**
 * IssueCard component for Kanban board.
 * Displays a single issue as a card with title, ID, priority badge, and optional blocked indicator.
 */

import { memo } from "react";

import {
  getAvatarColor,
  getStatusDotColor,
  getStatusLabel,
} from "@/components/AgentCard";
import { BlockedBadge } from "@/components/BlockedBadge";
import { HighlightText } from "@/components/HighlightText";
import { RepoBadge } from "@/components/RepoBadge";
import { TypeIcon } from "@/components/TypeIcon";
import { useHasActiveSession } from "@/contexts/IssueSessionContext";
import { useSearchTerm } from "@/contexts/SearchTermContext";
import { useStore } from "zustand";

import { useAgentStoreInstance } from "@/hooks";
import { useWorkspaceContext } from "@/hooks/workspace";
import type { BlockerRef, Issue } from "@/types";
import { isKnownIssueType, parseLoomStatus } from "@/types";
import {
  formatIssueId,
  getOpenStatus,
  getReviewType,
  isPRUrl,
  type OpenStatus,
  type ReviewType,
} from "@/utils/issue";

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
  /** Whether this issue has an active terminal session */
  hasActiveSession?: boolean;
}

const PRIORITY_LABELS: Record<number, string> = {
  0: "Critical",
  1: "High",
  2: "Medium",
  3: "Normal",
  4: "Backlog",
};

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
  const agentStore = useAgentStoreInstance();
  const agents = useStore(agentStore, (s) => s.agents);
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
  const reviewType = getReviewType(issue);
  const openStatus = columnId === "ready" ? getOpenStatus(issue) : null;

  // Compute agent row data for in_progress and review cards with an assignee
  const showAgentRow =
    (columnId === "in_progress" || columnId === "review") && !!issue.assignee;
  const isReviewColumn = columnId === "review";
  const assignedAgent = issue.assignee
    ? agents.find((a) => a.name === issue.assignee)
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
            <span className={styles.reviewIcon} aria-hidden="true">
              {REVIEW_BADGE_CONFIG[reviewType].icon}
            </span>
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
          aria-label={`Priority: P${priority} - ${PRIORITY_LABELS[priority] ?? "Unknown"}`}
        >
          P{priority}
        </span>
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
      {showAgentRow && issue.assignee && (
        <AgentRow
          agentName={issue.assignee}
          status={isReviewColumn ? null : agentParsedStatus}
          avatarColor={getAvatarColor(issue.assignee.replace(/^\[H\]\s*/, ""))}
          dotColor={
            isReviewColumn
              ? undefined
              : agentParsedStatus
                ? getStatusDotColor(agentParsedStatus.type)
                : undefined
          }
          activity={
            isReviewColumn
              ? "Submitted for review"
              : agentParsedStatus && assignedAgent
                ? getStatusLabel(agentParsedStatus)
                : undefined
          }
        />
      )}
    </article>
  );
});
