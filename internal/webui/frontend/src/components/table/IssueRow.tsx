/**
 * IssueRow component for rendering a single table row.
 * Encapsulates row rendering logic from IssueTable for better separation of concerns.
 */

import type { KeyboardEvent, ChangeEvent, MouseEvent } from 'react';

import type { BlockedInfo } from '@/components/KanbanBoard';
import type { Issue } from '@/types';

import { renderCellContent } from './cellRenderers';
import type { ColumnDef } from './columns';
import { getCellValue } from './columns';

/**
 * Props for IssueRow component.
 * Generic over row type T (extends Issue) to work with ColumnDef<T>.
 */
export interface IssueRowProps<T extends Issue> {
  /** The issue data to render */
  issue: T;
  /** Column definitions for cell rendering */
  columns: ColumnDef<T>[];
  /** Whether this row is selected */
  isSelected?: boolean | undefined;
  /** Whether the row is clickable */
  isClickable?: boolean | undefined;
  /** Callback when row is clicked */
  onClick?: ((issue: T) => void) | undefined;
  /** Additional CSS class name */
  className?: string | undefined;
  /** Whether to show checkbox column */
  showCheckbox?: boolean | undefined;
  /** Callback when checkbox state changes */
  onSelectionChange?: ((issueId: string, selected: boolean) => void) | undefined;
  /** Whether this issue is blocked */
  isBlocked?: boolean | undefined;
  /** Blocked info for rendering the blocked cell */
  blockedInfo?: BlockedInfo | undefined;
}

/**
 * IssueRow renders a single table row within IssueTable.
 * Handles cell rendering, selection state, click/keyboard interactions, and accessibility.
 */
export function IssueRow<T extends Issue>({
  issue,
  columns,
  isSelected = false,
  isClickable = false,
  onClick,
  className,
  showCheckbox = false,
  onSelectionChange,
  isBlocked = false,
  blockedInfo,
}: IssueRowProps<T>) {
  const handleClick = () => {
    if (isClickable && onClick) {
      onClick(issue);
    }
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLTableRowElement>) => {
    if (isClickable && onClick && (event.key === 'Enter' || event.key === ' ')) {
      event.preventDefault();
      onClick(issue);
    }
  };

  const handleCheckboxChange = (e: ChangeEvent<HTMLInputElement>) => {
    onSelectionChange?.(issue.id, e.target.checked);
  };

  const handleCheckboxClick = (e: MouseEvent<HTMLTableCellElement>) => {
    // Prevent row click from firing
    e.stopPropagation();
  };

  const rowClassName = [
    'issue-table__row',
    isSelected ? 'issue-table__row--selected' : '',
    isClickable ? 'issue-table__row--clickable' : '',
    isBlocked ? 'issue-table__row--blocked' : '',
    className,
  ]
    .filter(Boolean)
    .join(' ');

  return (
    <tr
      className={rowClassName}
      onClick={handleClick}
      onKeyDown={handleKeyDown}
      tabIndex={isClickable ? 0 : undefined}
      aria-selected={isClickable ? isSelected : undefined}
      data-testid={`issue-row-${issue.id}`}
    >
      {showCheckbox && (
        <td className="issue-table__cell issue-table__cell--checkbox" onClick={handleCheckboxClick}>
          <input
            type="checkbox"
            className="issue-table__checkbox"
            checked={isSelected}
            onChange={handleCheckboxChange}
            aria-label={`Select issue ${issue.id}`}
          />
        </td>
      )}
      {columns.map((column) => {
        const value = getCellValue(issue, column);
        return (
          <td
            key={column.id}
            className="issue-table__cell"
            style={{ textAlign: column.align ?? 'left' }}
            data-column={column.id}
          >
            {renderCellContent(column.id, value, issue, { blockedInfo })}
          </td>
        );
      })}
    </tr>
  );
}

export default IssueRow;
