/**
 * Default column configurations for the 5-column kanban layout.
 *
 * Columns:
 * - Ready: Open issues with no blockers (can be started immediately)
 * - Backlog: Issues not yet actionable (blocked by deps, blocked status, or deferred)
 * - In Progress: Issues actively being worked on
 * - Review: Issues needing human attention (review, [Need Review])
 * - Done: Closed issues
 */

import type { KanbanColumnConfig } from './types';

/**
 * Default kanban columns for multi-agent workflows.
 * Order matters: filter functions are evaluated in order, issue belongs to first match.
 */
/**
 * Helper to check if issue needs review based on title.
 */
const needsReviewByTitle = (title: string): boolean =>
  title.includes('[Need Review]');

export const DEFAULT_COLUMNS: KanbanColumnConfig[] = [
  {
    id: 'ready',
    label: 'Ready',
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
    id: 'backlog',
    label: 'Backlog',
    filter: (issue, blockedInfo) =>
      issue.issue_type !== 'epic' &&
      (((issue.status === 'open' || issue.status === undefined) &&
        !!blockedInfo &&
        blockedInfo.blockedByCount > 0) ||
        issue.status === 'blocked' ||
        issue.status === 'deferred') &&
      !needsReviewByTitle(issue.title),
    droppableDisabled: true, // Cannot drop TO backlog (auto-calculated)
    allowedDropTargets: ['done'], // Can only drag FROM backlog to Done
    style: 'muted',
  },
  {
    id: 'in_progress',
    label: 'In Progress',
    filter: (issue) =>
      issue.issue_type !== 'epic' && issue.status === 'in_progress',
    targetStatus: 'in_progress',
    allowedDropTargets: ['ready', 'in_progress', 'review', 'done'],
    style: 'normal',
  },
  {
    id: 'review',
    label: 'Review',
    filter: (issue) =>
      issue.issue_type !== 'epic' &&
      (issue.status === 'review' ||
       needsReviewByTitle(issue.title)),
    targetStatus: 'review',
    allowedDropTargets: ['ready', 'in_progress', 'review', 'done'],
    style: 'highlighted',
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
