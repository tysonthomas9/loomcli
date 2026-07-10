/**
 * KanbanBoard component exports.
 * Barrel file for convenient imports.
 */

export { KanbanBoard } from "./KanbanBoard";
export type { KanbanBoardProps } from "./KanbanBoard";

export { createDragEndHandler } from "./useDragEnd";
export type {
  HandleDragEndOptions,
  IssueStatusChangeCallback,
} from "./useDragEnd";

export { DEFAULT_COLUMNS, createColumns } from "./columnConfigs";
export { visibleKanbanColumns } from "./columnVisibility";
export type { KanbanColumnConfig } from "./types";
