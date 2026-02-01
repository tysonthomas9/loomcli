/**
 * Types for kanban board column configuration.
 * Columns can filter issues by status, dependencies, or title patterns.
 */

import type { Issue, Status } from '@/types';
import type { BlockedInfo } from './KanbanBoard';

/**
 * Configuration for a kanban column.
 * Decouples visual columns from underlying status values to support
 * computed columns like "Backlog" (blocked by deps, blocked status, or deferred).
 */
export interface KanbanColumnConfig {
  /** Unique column identifier (used as droppable ID) */
  id: string;
  /** Display label for column header */
  label: string;
  /** Filter function to determine which issues appear in this column */
  filter: (issue: Issue, blockedInfo?: BlockedInfo) => boolean;
  /** Status to set when an issue is dropped in this column */
  targetStatus?: Status;
  /** Whether this column rejects all drops (auto-calculated columns) */
  droppableDisabled?: boolean;
  /** Column IDs that cards from this column can be dragged to */
  allowedDropTargets?: string[];
  /** Visual style variant */
  style?: 'normal' | 'muted' | 'highlighted';
}
