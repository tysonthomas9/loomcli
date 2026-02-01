/**
 * SwimLane component for Kanban board.
 * Displays a horizontal lane containing status columns for a group of issues.
 * Each swim lane represents a grouping (e.g., epic, assignee, priority).
 */

import { useMemo } from 'react';
import type { Issue } from '@/types';
import type { BlockedInfo } from '@/components/KanbanBoard';
import type { KanbanColumnConfig } from '@/components/KanbanBoard/types';
import { StatusColumn } from '@/components/StatusColumn';
import { DraggableIssueCard } from '@/components/DraggableIssueCard';
import { EmptyColumn } from '@/components/EmptyColumn';
import styles from './SwimLane.module.css';

/**
 * Props for the SwimLane component.
 */
export interface SwimLaneProps {
  /** Unique identifier for this lane */
  id: string;
  /** Display title (e.g., "Epic: User Authentication") */
  title: string;
  /** Issues belonging to this lane */
  issues: Issue[];
  /** Column configurations (5-column layout) */
  columns: KanbanColumnConfig[];
  /** Whether the lane content is collapsed */
  isCollapsed?: boolean;
  /** Callback when collapse toggle is clicked */
  onToggleCollapse?: () => void;
  /** Callback when an issue card is clicked */
  onIssueClick?: (issue: Issue) => void;
  /** Map of issue ID to blocked info */
  blockedIssues?: Map<string, BlockedInfo>;
  /** Whether to show blocked issues */
  showBlocked?: boolean;
  /** Additional CSS class */
  className?: string;
  /** Callback when approve button is clicked on a card in review column */
  onApprove?: (issue: Issue) => void | Promise<void>;
  /** Callback when reject is submitted with comment on a card in review column */
  onReject?: (issue: Issue, comment: string) => void | Promise<void>;
}

/**
 * SwimLane displays a horizontal lane with status columns for grouped issues.
 * Used within SwimLaneBoard to organize issues by epic, assignee, or other criteria.
 * Does NOT create its own DndContext - parent provides it for cross-lane drag support.
 */
export function SwimLane({
  id,
  title,
  issues,
  columns,
  isCollapsed = false,
  onToggleCollapse,
  onIssueClick,
  blockedIssues,
  showBlocked = true,
  className,
  onApprove,
  onReject,
}: SwimLaneProps): JSX.Element {
  // Filter issues based on blocked visibility
  const filteredIssues = useMemo(() => {
    if (showBlocked || !blockedIssues) return issues;
    return issues.filter((issue) => !blockedIssues.has(issue.id));
  }, [issues, showBlocked, blockedIssues]);

  // Group issues by column using filter functions
  const issuesByColumn = useMemo(() => {
    const grouped = new Map<string, Issue[]>();
    for (const col of columns) {
      grouped.set(col.id, []);
    }
    for (const issue of filteredIssues) {
      const blockedInfo = blockedIssues?.get(issue.id);
      for (const col of columns) {
        if (col.filter(issue, blockedInfo)) {
          grouped.get(col.id)?.push(issue);
          break; // Issue belongs to first matching column only
        }
      }
    }
    return grouped;
  }, [filteredIssues, columns, blockedIssues]);

  const headerId = `lane-header-${id}`;
  const rootClassName = [styles.swimLane, className].filter(Boolean).join(' ');

  return (
    <section
      className={rootClassName}
      aria-labelledby={headerId}
      data-collapsed={isCollapsed}
      data-testid={`swim-lane-${id}`}
    >
      <header className={styles.laneHeader} id={headerId}>
        <button
          className={styles.collapseToggle}
          onClick={onToggleCollapse}
          aria-expanded={!isCollapsed}
          aria-label={isCollapsed ? `Expand ${title}` : `Collapse ${title}`}
          data-testid="collapse-toggle"
        >
          <svg
            className={styles.chevronIcon}
            width="16"
            height="16"
            viewBox="0 0 16 16"
            fill="none"
            aria-hidden="true"
          >
            <path
              d="M6 4l4 4-4 4"
              stroke="currentColor"
              strokeWidth="2"
              strokeLinecap="round"
              strokeLinejoin="round"
            />
          </svg>
        </button>
        <h3 className={styles.laneTitle}>{title}</h3>
        <span className={styles.laneCount} aria-label={`${filteredIssues.length} issues`}>
          {filteredIssues.length}
        </span>
      </header>
      <div className={styles.laneContent} data-collapsed={isCollapsed} aria-hidden={isCollapsed}>
        {columns.map((col) => {
          const colIssues = issuesByColumn.get(col.id) ?? [];
          const columnClassName =
            col.style === 'muted'
              ? styles.mutedColumn
              : col.style === 'highlighted'
                ? styles.highlightedColumn
                : undefined;

          // Determine column type and icon for backlog column
          const isBacklogColumn = col.id === 'backlog';
          const columnType = isBacklogColumn ? ('backlog' as const) : undefined;
          const headerIcon = isBacklogColumn ? '⏳' : undefined;

          // Build props conditionally to satisfy exactOptionalPropertyTypes
          const isDropDisabled = isCollapsed || col.droppableDisabled === true;
          const statusColumnProps = {
            status: col.id,
            statusLabel: col.label,
            count: colIssues.length,
            ...(isDropDisabled && { droppableDisabled: true }),
            ...(columnClassName !== undefined && { className: columnClassName }),
            ...(columnType !== undefined && { columnType }),
            ...(headerIcon !== undefined && { headerIcon }),
          };

          return (
            <StatusColumn key={col.id} {...statusColumnProps}>
              {colIssues.length === 0 ? (
                <EmptyColumn status={col.id} />
              ) : (
                colIssues.map((issue) => {
                  const blockedInfo = blockedIssues?.get(issue.id);
                  const cardProps = {
                    issue,
                    columnId: col.id,
                    ...(onIssueClick !== undefined && { onClick: onIssueClick }),
                    ...(blockedInfo !== undefined && {
                      blockedByCount: blockedInfo.blockedByCount,
                      blockedBy: blockedInfo.blockedBy,
                    }),
                    ...(isBacklogColumn && { isBacklog: true }),
                    ...(onApprove !== undefined && { onApprove }),
                    ...(onReject !== undefined && { onReject }),
                  };
                  return <DraggableIssueCard key={issue.id} {...cardProps} />;
                })
              )}
            </StatusColumn>
          );
        })}
      </div>
    </section>
  );
}
