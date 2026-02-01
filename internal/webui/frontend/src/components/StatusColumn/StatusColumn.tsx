/**
 * StatusColumn component for Kanban board.
 * Displays a vertical column representing a specific issue status.
 * Acts as a drop target for draggable IssueCards via @dnd-kit.
 */

import { useDroppable } from '@dnd-kit/core';
import type { Status } from '@/types';
import { formatStatusLabel } from './utils';
import styles from './StatusColumn.module.css';

/**
 * Column type for visual variants (different from status value).
 * Allows same status to have different visual treatments.
 */
export type ColumnType = 'ready' | 'backlog' | 'review' | 'default';

/**
 * Props for the StatusColumn component.
 */
export interface StatusColumnProps {
  /** The status this column represents (also used as droppable ID) */
  status: Status;
  /** Human-readable label override (defaults to formatted status) */
  statusLabel?: string;
  /** Number of issues in this column */
  count: number;
  /** IssueCard components to display */
  children?: React.ReactNode;
  /** Additional CSS class name */
  className?: string;
  /** Whether dropping is disabled for this column */
  droppableDisabled?: boolean;
  /** Column type for visual styling (defaults to 'default') */
  columnType?: ColumnType;
  /** Icon to display in header (e.g., '📦' for backlog) */
  headerIcon?: string;
}

/**
 * StatusColumn displays a vertical column in the Kanban board.
 * Shows a header with status name and count, and contains IssueCard components.
 * The content area is a drop target for IssueCards when used within a DndContext.
 */
export function StatusColumn({
  status,
  statusLabel,
  count,
  children,
  className,
  droppableDisabled = false,
  columnType,
  headerIcon,
}: StatusColumnProps): JSX.Element {
  const { setNodeRef, isOver } = useDroppable({
    id: status,
    disabled: droppableDisabled,
    data: { status },
  });

  const displayLabel = statusLabel ?? formatStatusLabel(status);
  const issueWord = count === 1 ? 'issue' : 'issues';

  const rootClassName = className ? `${styles.statusColumn} ${className}` : styles.statusColumn;

  // Build content class with drop state
  // Note: isOver is only true during an active drag operation
  const contentClasses = [styles.content];
  if (isOver) {
    contentClasses.push(styles.contentDropOver);
  }

  return (
    <section
      className={rootClassName}
      data-status={status}
      data-column-type={columnType}
      data-has-items={count > 0 ? 'true' : undefined}
      aria-label={`${displayLabel} issues`}
    >
      <header className={styles.header}>
        {headerIcon && (
          <span className={styles.columnIcon} aria-hidden="true">
            {headerIcon}
          </span>
        )}
        <h2 className={styles.title}>{displayLabel}</h2>
        <span className={styles.count} aria-label={`${count} ${issueWord}`}>
          {count}
        </span>
      </header>
      <div
        ref={setNodeRef}
        className={contentClasses.join(' ')}
        role="list"
        data-droppable-id={status}
        data-is-over={isOver ? 'true' : undefined}
      >
        {children}
      </div>
    </section>
  );
}
