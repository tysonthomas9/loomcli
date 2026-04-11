/**
 * Pure cell-value helpers for table rendering.
 *
 * Lives in src/utils/ because it is a trivial generic function with no
 * React dependency. Moved out of src/components/table/columns.ts in
 * Phase 7 so hooks (useSort) can import it without the frontend layer
 * DAG forbidding hooks → components.
 */

import type { ColumnDef } from "@/types/table";

/**
 * Get cell value from a row using the column accessor.
 */
export function getCellValue<T>(row: T, column: ColumnDef<T>): unknown {
  if (typeof column.accessor === "function") {
    return column.accessor(row);
  }
  return row[column.accessor];
}
