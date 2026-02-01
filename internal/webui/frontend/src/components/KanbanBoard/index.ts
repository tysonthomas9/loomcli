/**
 * KanbanBoard component exports.
 * Barrel file for convenient imports.
 */

export { KanbanBoard } from './KanbanBoard';
export type { KanbanBoardProps, BlockedInfo } from './KanbanBoard';

export { createDragEndHandler } from './useDragEnd';
export type {
  HandleDragEndOptions,
  IssueStatusChangeCallback,
} from './useDragEnd';

export { DEFAULT_COLUMNS } from './columnConfigs';
export type { KanbanColumnConfig } from './types';
