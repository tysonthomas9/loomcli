import type { KanbanColumnConfig } from "./types";

/**
 * When compact mode is on, keep only columns that contain at least one issue.
 * Falls back to the full column set when every column is empty (e.g. an empty lane).
 */
export function visibleKanbanColumns(
  columns: KanbanColumnConfig[],
  issuesByColumn: Map<string, unknown[]>,
  compactColumns: boolean,
): KanbanColumnConfig[] {
  if (!compactColumns) return columns;

  const nonempty = columns.filter(
    (col) => (issuesByColumn.get(col.id)?.length ?? 0) > 0,
  );
  return nonempty.length > 0 ? nonempty : columns;
}
