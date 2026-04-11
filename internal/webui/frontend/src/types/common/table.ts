/**
 * Table column types.
 *
 * Lives in src/types/ so that hooks (e.g. useSort) and utils (e.g.
 * tableCells) can reference the column-definition shape without
 * crossing the frontend layer DAG back into src/components/table/.
 */

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
  align?: "left" | "center" | "right";
  /** Whether column is sortable (for future TableHeader integration) */
  sortable?: boolean;
}
