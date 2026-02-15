/**
 * SwimLane component for Kanban board.
 * Displays a horizontal lane containing status columns for a group of issues.
 * Each swim lane represents a grouping (e.g., epic, assignee, priority).
 */

import { useMemo, useState } from 'react';

import { DraggableIssueCard } from '@/components/DraggableIssueCard';
import { EmptyColumn } from '@/components/EmptyColumn';
import type { BlockedInfo } from '@/components/KanbanBoard';
import type { KanbanColumnConfig } from '@/components/KanbanBoard/types';
import { StatusColumn } from '@/components/StatusColumn';
import type { Issue } from '@/types';

import styles from './SwimLane.module.css';

/**
 * Maximum number of cards to show per column before collapsing.
 * When there are more than this many cards, a "Show all X" footer appears.
 */
const DEFAULT_CARD_LIMIT = 5;

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
  /** Maximum cards to show per column (default: 5) */
  cardLimit?: number;
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
  cardLimit = DEFAULT_CARD_LIMIT,
}: SwimLaneProps): JSX.Element {
  // Track which columns are expanded to show all cards
  const [expandedColumns, setExpandedColumns] = useState<Set<string>>(new Set());

  // Toggle expanded state for a column
  const toggleColumnExpanded = (columnId: string): void => {
    setExpandedColumns((prev) => {
      const next = new Set(prev);
      if (next.has(columnId)) {
        next.delete(columnId);
      } else {
        next.add(columnId);
      }
      return next;
    });
  };

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

          // Determine column type for backlog/blocked columns
          const isBacklogColumn = col.id === 'backlog';
          const isBlockedColumn = col.id === 'blocked';
          const isMutedColumn = isBacklogColumn || isBlockedColumn;
          const columnType = isBacklogColumn ? ('backlog' as const) : undefined;

          // Determine if column should show limited cards
          const isColumnExpanded = expandedColumns.has(col.id);
          const hasMoreCards = colIssues.length > cardLimit;
          const displayedIssues = isColumnExpanded ? colIssues : colIssues.slice(0, cardLimit);

          // Build footer action if there are more cards to show
          const footerAction = hasMoreCards ? (
            <button
              type="button"
              onClick={() => toggleColumnExpanded(col.id)}
              aria-label={
                isColumnExpanded
                  ? `Show fewer ${col.label} issues`
                  : `Show all ${colIssues.length} ${col.label} issues`
              }
              data-testid={`toggle-column-${col.id}`}
            >
              {isColumnExpanded ? 'Show fewer' : `Show all ${colIssues.length}`}
            </button>
          ) : undefined;

          // Build props conditionally to satisfy exactOptionalPropertyTypes
          const isDropDisabled = isCollapsed || col.droppableDisabled === true;
          const statusColumnProps = {
            status: col.id,
            statusLabel: col.label,
            count: colIssues.length,
            ...(isDropDisabled && { droppableDisabled: true }),
            ...(columnClassName !== undefined && { className: columnClassName }),
            ...(columnType !== undefined && { columnType }),
            ...(footerAction !== undefined && { footerAction }),
          };

          return (
            <StatusColumn key={col.id} {...statusColumnProps}>
              {colIssues.length === 0 ? (
                <EmptyColumn status={col.id} />
              ) : (
                displayedIssues.map((issue) => {
                  const blockedInfo = blockedIssues?.get(issue.id);
                  const cardProps = {
                    issue,
                    columnId: col.id,
                    ...(onIssueClick !== undefined && { onClick: onIssueClick }),
                    ...(blockedInfo !== undefined && {
                      blockedByCount: blockedInfo.blockedByCount,
                      blockedBy: blockedInfo.blockedBy,
                      ...(blockedInfo.blockedByDetails !== undefined && {
                        blockedByDetails: blockedInfo.blockedByDetails,
                      }),
                    }),
                    ...(isMutedColumn && { isBacklog: true }),
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
