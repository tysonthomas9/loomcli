/**
 * TableHeader component for displaying sortable table headers.
 * Renders column headers with interactive sort indicators.
 */

import { KeyboardEvent, useCallback } from 'react';

import type { ColumnDef } from './columns';

/** Sort direction for table columns */
export type SortDirection = 'asc' | 'desc';

/** Current sort state */
export interface SortState {
  /** Currently sorted column key, or null if unsorted */
  key: string | null;
  /** Sort direction */
  direction: SortDirection;
}

export interface TableHeaderProps<T> {
  /** Column definitions */
  columns: ColumnDef<T>[];
  /** Current sort state */
  sortState: SortState;
  /** Callback when a sortable column header is clicked */
  onSort: (columnId: string) => void;
  /** Whether to show checkbox column */
  showCheckbox?: boolean;
}

/**
 * TableHeader displays table column headers with sort indicators.
 * Clicking a sortable header cycles through: ascending → descending → unsorted.
 */
export function TableHeader<T>({ columns, sortState, onSort, showCheckbox }: TableHeaderProps<T>) {
  const handleKeyDown = useCallback(
    (event: KeyboardEvent<HTMLTableCellElement>, columnId: string) => {
      if (event.key === 'Enter' || event.key === ' ') {
        event.preventDefault();
        onSort(columnId);
      }
    },
    [onSort]
  );

  return (
    <thead className="issue-table__head">
      <tr>
        {showCheckbox && (
          <th className="issue-table__header-cell issue-table__header-cell--checkbox">
            {/* Header checkbox will be added in a future task */}
            <span className="sr-only">Select</span>
          </th>
        )}
        {columns.map((column) => {
          const isSorted = sortState.key === column.id;
          const sortDirection = isSorted ? sortState.direction : null;

          // Build class name for the header cell
          const cellClassName = [
            'issue-table__header-cell',
            column.sortable && 'issue-table__header-cell--sortable',
            isSorted && 'issue-table__header-cell--sorted',
          ]
            .filter(Boolean)
            .join(' ');

          // Determine aria-sort value
          let ariaSort: 'ascending' | 'descending' | 'none' | undefined;
          if (column.sortable) {
            if (isSorted) {
              ariaSort = sortDirection === 'asc' ? 'ascending' : 'descending';
            } else {
              ariaSort = 'none';
            }
          }

          // Build aria-label for sortable headers (screen reader accessibility)
          let ariaLabel: string | undefined;
          if (column.sortable) {
            const sortStateLabel = isSorted
              ? `, currently sorted ${sortDirection === 'asc' ? 'ascending' : 'descending'}`
              : '';
            ariaLabel = `Sort by ${column.header}${sortStateLabel}`;
          }

          return (
            <th
              key={column.id}
              className={cellClassName}
              style={{
                width: column.width,
                textAlign: column.align ?? 'left',
              }}
              data-column={column.id}
              aria-sort={ariaSort}
              aria-label={ariaLabel}
              tabIndex={column.sortable ? 0 : undefined}
              role={column.sortable ? 'button' : undefined}
              onClick={column.sortable ? () => onSort(column.id) : undefined}
              onKeyDown={column.sortable ? (e) => handleKeyDown(e, column.id) : undefined}
            >
              <span className="issue-table__header-content">
                {column.header}
                {column.sortable && (
                  <span
                    className={[
                      'issue-table__sort-indicator',
                      isSorted && sortDirection && `issue-table__sort-indicator--${sortDirection}`,
                    ]
                      .filter(Boolean)
                      .join(' ')}
                    aria-hidden="true"
                  >
                    {isSorted && sortDirection ? (sortDirection === 'asc' ? '▲' : '▼') : '↕'}
                  </span>
                )}
              </span>
            </th>
          );
        })}
      </tr>
    </thead>
  );
}

export default TableHeader;
