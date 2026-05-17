/**
 * Default column configurations for the 6-column kanban layout.
 *
 * Columns (in display order):
 * - Backlog: Deferred issues not yet ready for work
 * - Open: Open issues with no blockers (can be started immediately)
 * - Blocked: Issues blocked by dependencies or explicit 'blocked' status
 * - In Progress: Issues actively being worked on
 * - Review: Issues needing human attention (status=review)
 * - Done: Closed issues
 */

import type { KanbanColumnConfig } from "./types";
import type { BlockedInfo, Issue } from "@/types";

const ClockIcon = (
  <svg viewBox="0 0 24 24" aria-hidden="true">
    <path
      d="M12 7v5l3 2"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
    />
    <circle
      cx="12"
      cy="12"
      r="9"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
    />
  </svg>
);

function isDeferredIssue(issue: Issue): boolean {
  return issue.is_deferred === true || issue.status === "deferred";
}

function isBlockedIssue(issue: Issue, blockedInfo?: BlockedInfo): boolean {
  return (
    issue.is_blocked === true ||
    issue.status === "blocked" ||
    (blockedInfo?.blockedByCount ?? 0) > 0
  );
}

function isReadyIssue(issue: Issue, blockedInfo?: BlockedInfo): boolean {
  const hasOpenStatus = issue.status === "open" || issue.status === undefined;
  if (issue.is_ready !== undefined) {
    return (
      issue.is_ready === true &&
      hasOpenStatus &&
      !isDeferredIssue(issue) &&
      !isBlockedIssue(issue, blockedInfo)
    );
  }
  return (
    hasOpenStatus &&
    !isDeferredIssue(issue) &&
    !isBlockedIssue(issue, blockedInfo)
  );
}

/**
 * Default kanban columns for multi-agent workflows.
 * Order matters: filter functions are evaluated in order, issue belongs to first match.
 */
export function createColumns(options?: {
  includeEpics?: boolean;
}): KanbanColumnConfig[] {
  const includeEpics = options?.includeEpics === true;
  return [
    {
      id: "backlog",
      label: "Backlog",
      filter: (issue) =>
        (includeEpics || issue.issue_type !== "epic") && isDeferredIssue(issue),
      droppableDisabled: true, // Cannot drop TO backlog (auto-calculated)
      allowedDropTargets: ["done"], // Can only drag FROM backlog to Done
      style: "muted",
    },
    {
      id: "ready",
      label: "Open",
      headerIcon: ClockIcon,
      filter: (issue, blockedInfo) =>
        (includeEpics || issue.issue_type !== "epic") &&
        isReadyIssue(issue, blockedInfo),
      targetStatus: "open",
      allowedDropTargets: ["ready", "in_progress", "review", "done"],
      style: "normal",
    },
    {
      id: "blocked",
      label: "Blocked",
      filter: (issue, blockedInfo) =>
        (includeEpics || issue.issue_type !== "epic") &&
        !isDeferredIssue(issue) &&
        isBlockedIssue(issue, blockedInfo),
      droppableDisabled: true, // Cannot drop TO blocked (auto-calculated)
      allowedDropTargets: ["done"], // Can only drag FROM blocked to Done
      style: "muted",
    },
    {
      id: "in_progress",
      label: "In Progress",
      filter: (issue) =>
        (includeEpics || issue.issue_type !== "epic") &&
        issue.status === "in_progress",
      targetStatus: "in_progress",
      allowedDropTargets: ["ready", "in_progress", "review", "done"],
      style: "normal",
    },
    {
      id: "review",
      label: "Review",
      filter: (issue) =>
        (includeEpics || issue.issue_type !== "epic") &&
        issue.status === "review",
      targetStatus: "review",
      allowedDropTargets: ["ready", "in_progress", "review", "done"],
      style: "normal",
    },
    {
      id: "done",
      label: "Done",
      filter: (issue) => issue.status === "closed",
      targetStatus: "closed",
      allowedDropTargets: ["ready", "in_progress", "review", "done"],
      style: "normal",
      defaultLimit: 10,
    },
  ];
}

export const DEFAULT_COLUMNS: KanbanColumnConfig[] = createColumns();
