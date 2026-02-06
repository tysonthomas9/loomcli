/**
 * Column definitions for IssueTable.
 * Provides typed column configuration and cell rendering helpers.
 */

import type { Issue, Status, Priority, IssueType } from '@/types';

/**
 * Column definition for IssueTable.
 */
export interface ColumnDef<T> {
  /** Unique column identifier */
  id: string;
  /** Header text displayed in table header */
  header: string;
  /** Property key or accessor function to get cell value */
  accessor: keyof T | ((row: T) => unknown);
  /** Column width (CSS value, e.g., '100px', '1fr', 'auto') */
  width?: string;
  /** Text alignment for cell content */
  align?: 'left' | 'center' | 'right';
  /** Whether column is sortable (for future TableHeader integration) */
  sortable?: boolean;
}

/**
 * Format a priority value as a display string (P0-P4).
 */
export function formatPriority(priority: Priority): string {
  return `P${priority}`;
}

/**
 * Get CSS class name for priority badge styling.
 */
export function getPriorityClassName(priority: Priority): string {
  return `priority-${priority}`;
}

/**
 * Format a status value for display.
 * Replaces underscores with spaces and capitalizes first letter.
 */
export function formatStatus(status: Status | undefined): string {
  if (!status) return '-';
  return status.replace(/_/g, ' ').replace(/^\w/, (c) => c.toUpperCase());
}

/**
 * Get CSS class name for status badge styling.
 */
export function getStatusClassName(status: Status | undefined): string {
  if (!status) return 'status-unknown';
  return `status-${status.replace(/_/g, '-')}`;
}

/**
 * Format an issue type for display.
 * Capitalizes first letter.
 */
export function formatIssueType(issueType: IssueType | undefined): string {
  if (!issueType) return '-';
  return issueType.replace(/^\w/, (c) => c.toUpperCase());
}

/**
 * Format a date string for display.
 * Returns relative time for recent dates, date for older.
 */
export function formatDate(dateString: string | undefined): string {
  if (!dateString) return '-';

  const date = new Date(dateString);
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  const diffHours = diffMs / (1000 * 60 * 60);
  const diffDays = diffHours / 24;

  if (diffHours < 1) {
    const diffMins = Math.floor(diffMs / (1000 * 60));
    return diffMins <= 1 ? 'just now' : `${diffMins}m ago`;
  }
  if (diffHours < 24) {
    return `${Math.floor(diffHours)}h ago`;
  }
  if (diffDays < 7) {
    return `${Math.floor(diffDays)}d ago`;
  }

  return date.toLocaleDateString();
}

/**
 * Default column definitions for Issue display.
 */
export const DEFAULT_ISSUE_COLUMNS: ColumnDef<Issue>[] = [
  {
    id: 'id',
    header: 'ID',
    accessor: 'id',
    width: '120px',
    align: 'left',
    sortable: true,
  },
  {
    id: 'priority',
    header: 'Priority',
    accessor: 'priority',
    width: '80px',
    align: 'center',
    sortable: true,
  },
  {
    id: 'title',
    header: 'Title',
    accessor: 'title',
    width: '1fr',
    align: 'left',
    sortable: true,
  },
  {
    id: 'status',
    header: 'Status',
    accessor: 'status',
    width: '110px',
    align: 'center',
    sortable: true,
  },
  {
    id: 'blocked',
    header: 'Blocked',
    accessor: () => null, // Custom accessor - blocked info passed separately
    width: '80px',
    align: 'center',
    sortable: true,
  },
  {
    id: 'issue_type',
    header: 'Type',
    accessor: 'issue_type',
    width: '90px',
    align: 'center',
    sortable: true,
  },
  {
    id: 'assignee',
    header: 'Assignee',
    accessor: 'assignee',
    width: '120px',
    align: 'left',
    sortable: true,
  },
  {
    id: 'updated_at',
    header: 'Updated',
    accessor: 'updated_at',
    width: '100px',
    align: 'right',
    sortable: true,
  },
];

/**
 * Get cell value from a row using the column accessor.
 */
export function getCellValue<T>(row: T, column: ColumnDef<T>): unknown {
  if (typeof column.accessor === 'function') {
    return column.accessor(row);
  }
  return row[column.accessor];
}
