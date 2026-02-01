/**
 * Utility functions for grouping issues into swim lanes.
 * Pure functions for testability and reuse.
 */

import type { Issue } from '@/types';

/**
 * Field by which to group issues into swim lanes.
 */
export type GroupByField = 'none' | 'epic' | 'assignee' | 'priority' | 'type' | 'label';

/**
 * A group of issues forming a swim lane.
 */
export interface LaneGroup {
  /** Unique ID for React key and collapse state */
  id: string;
  /** Display title (e.g., "Epic: User Auth", "Unassigned") */
  title: string;
  /** Issues in this lane */
  issues: Issue[];
}

/**
 * Priority display names for lane titles.
 */
const PRIORITY_DISPLAY_NAMES: Record<number, string> = {
  0: 'P0 (Critical)',
  1: 'P1 (High)',
  2: 'P2 (Medium)',
  3: 'P3 (Normal)',
  4: 'P4 (Backlog)',
};

/**
 * Get display name for a priority value.
 */
function getPriorityDisplayName(priority: number | undefined): string {
  if (priority === undefined) return 'No Priority';
  return PRIORITY_DISPLAY_NAMES[priority] ?? `P${priority}`;
}

/**
 * Group issues by a specified field.
 *
 * @param issues - Array of issues to group
 * @param groupBy - Field to group by
 * @returns Array of LaneGroup objects
 */
export function groupIssuesByField(issues: Issue[], groupBy: GroupByField): LaneGroup[] {
  if (groupBy === 'none') {
    // Single group containing all issues
    return [
      {
        id: 'lane-all',
        title: 'All Issues',
        issues,
      },
    ];
  }

  const groupMap = new Map<string, Issue[]>();

  for (const issue of issues) {
    const groupKeys = getGroupKeys(issue, groupBy);

    for (const key of groupKeys) {
      const existing = groupMap.get(key);
      if (existing) {
        existing.push(issue);
      } else {
        groupMap.set(key, [issue]);
      }
    }
  }

  // Convert map to LaneGroup array
  const lanes: LaneGroup[] = [];
  for (const [key, groupIssues] of groupMap) {
    lanes.push({
      id: getLaneId(groupBy, key),
      title: getLaneTitle(groupBy, key, groupIssues),
      issues: groupIssues,
    });
  }

  return lanes;
}

/**
 * Get the group key(s) for an issue based on groupBy field.
 * Returns array because label grouping can have multiple keys.
 */
function getGroupKeys(issue: Issue, groupBy: GroupByField): string[] {
  switch (groupBy) {
    case 'epic': {
      const parent = issue.parent;
      return parent ? [parent] : ['__ungrouped__'];
    }
    case 'assignee': {
      const assignee = issue.assignee;
      return assignee ? [assignee] : ['__unassigned__'];
    }
    case 'priority': {
      const priority = issue.priority;
      return priority !== undefined ? [priority.toString()] : ['__no_priority__'];
    }
    case 'type': {
      const issueType = issue.issue_type;
      return issueType ? [issueType] : ['__no_type__'];
    }
    case 'label': {
      const labels = issue.labels;
      return labels && labels.length > 0 ? labels : ['__no_labels__'];
    }
    default:
      return ['__ungrouped__'];
  }
}

/**
 * Generate a stable lane ID based on groupBy and key.
 */
function getLaneId(groupBy: GroupByField, key: string): string {
  return `lane-${groupBy}-${key}`;
}

/**
 * Get display title for a lane based on groupBy and key.
 */
function getLaneTitle(groupBy: GroupByField, key: string, issues: Issue[]): string {
  switch (groupBy) {
    case 'epic': {
      if (key === '__ungrouped__') return 'Ungrouped';
      // Try to get parent title from first issue
      const firstIssue = issues[0];
      if (firstIssue?.parent_title) return firstIssue.parent_title;
      // Fallback to key (parent ID)
      return key;
    }
    case 'assignee': {
      if (key === '__unassigned__') return 'Unassigned';
      return key;
    }
    case 'priority': {
      if (key === '__no_priority__') return 'No Priority';
      const priority = parseInt(key, 10);
      return getPriorityDisplayName(priority);
    }
    case 'type': {
      if (key === '__no_type__') return 'No Type';
      // Capitalize first letter
      return key.charAt(0).toUpperCase() + key.slice(1);
    }
    case 'label': {
      if (key === '__no_labels__') return 'No Labels';
      return key;
    }
    default:
      return key;
  }
}

/**
 * Sort lanes by title or issue count.
 *
 * @param lanes - Array of LaneGroup to sort
 * @param sortBy - Sort criteria: 'title' or 'count'
 * @returns New sorted array
 */
export function sortLanes(lanes: LaneGroup[], sortBy: 'title' | 'count'): LaneGroup[] {
  const sorted = [...lanes];

  // Separate ungrouped/special lanes (those with __ prefix in their original key)
  const isSpecialLane = (lane: LaneGroup): boolean => {
    return (
      lane.title === 'Ungrouped' ||
      lane.title === 'Unassigned' ||
      lane.title === 'No Priority' ||
      lane.title === 'No Type' ||
      lane.title === 'No Labels'
    );
  };

  const regularLanes = sorted.filter((lane) => !isSpecialLane(lane));
  const specialLanes = sorted.filter(isSpecialLane);

  // Sort regular lanes
  if (sortBy === 'title') {
    regularLanes.sort((a, b) => a.title.localeCompare(b.title));
  } else {
    regularLanes.sort((a, b) => b.issues.length - a.issues.length);
  }

  // Special lanes go at the end
  return [...regularLanes, ...specialLanes];
}
