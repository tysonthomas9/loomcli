/**
 * Default column configurations for the 6-column kanban layout.
 *
 * Columns (in display order):
 * - Backlog: Deferred issues not yet ready for work
 * - Open: Open issues with no blockers (can be started immediately)
 * - Blocked: Issues blocked by dependencies or explicit 'blocked' status
 * - In Progress: Issues actively being worked on
 * - Needs Review: Issues needing human attention (review, [Need Review])
 * - Done: Closed issues
 */

import type { KanbanColumnConfig } from './types';

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
    <circle cx="12" cy="12" r="9" fill="none" stroke="currentColor" strokeWidth="1.5" />
  </svg>
);

/**
 * Default kanban columns for multi-agent workflows.
 * Order matters: filter functions are evaluated in order, issue belongs to first match.
 */
/**
 * Helper to check if issue needs review based on title.
 */
const needsReviewByTitle = (title: string): boolean => title.includes('[Need Review]');

export const DEFAULT_COLUMNS: KanbanColumnConfig[] = [
  {
    id: 'backlog',
    label: 'Backlog',
    filter: (issue) =>
      issue.issue_type !== 'epic' &&
      issue.status === 'deferred' &&
      !needsReviewByTitle(issue.title),
    droppableDisabled: true, // Cannot drop TO backlog (auto-calculated)
    allowedDropTargets: ['done'], // Can only drag FROM backlog to Done
    style: 'muted',
  },
  {
    id: 'ready',
    label: 'Open',
    headerIcon: ClockIcon,
    filter: (issue, blockedInfo) =>
      issue.issue_type !== 'epic' &&
      (issue.status === 'open' || issue.status === undefined) &&
      (!blockedInfo || blockedInfo.blockedByCount === 0) &&
      !needsReviewByTitle(issue.title),
    targetStatus: 'open',
    allowedDropTargets: ['ready', 'in_progress', 'review', 'done'],
    style: 'normal',
  },
  {
    id: 'blocked',
    label: 'Blocked',
    filter: (issue, blockedInfo) =>
      issue.issue_type !== 'epic' &&
      (((issue.status === 'open' || issue.status === undefined) &&
        !!blockedInfo &&
        blockedInfo.blockedByCount > 0) ||
        issue.status === 'blocked') &&
      !needsReviewByTitle(issue.title),
    droppableDisabled: true, // Cannot drop TO blocked (auto-calculated)
    allowedDropTargets: ['done'], // Can only drag FROM blocked to Done
    style: 'muted',
  },
  {
    id: 'in_progress',
    label: 'In Progress',
    filter: (issue) => issue.issue_type !== 'epic' && issue.status === 'in_progress',
    targetStatus: 'in_progress',
    allowedDropTargets: ['ready', 'in_progress', 'review', 'done'],
    style: 'normal',
  },
  {
    id: 'review',
    label: 'Needs Review',
    filter: (issue) =>
      issue.issue_type !== 'epic' && (issue.status === 'review' || needsReviewByTitle(issue.title)),
    targetStatus: 'review',
    allowedDropTargets: ['ready', 'in_progress', 'review', 'done'],
    style: 'normal',
  },
  {
    id: 'done',
    label: 'Done',
    filter: (issue) => issue.status === 'closed',
    targetStatus: 'closed',
    allowedDropTargets: ['ready', 'in_progress', 'review', 'done'],
    style: 'normal',
  },
];
