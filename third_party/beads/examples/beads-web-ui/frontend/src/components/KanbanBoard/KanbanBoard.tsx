/**
 * KanbanBoard component for displaying issues in a drag-and-drop Kanban layout.
 * Wraps content in @dnd-kit DndContext to enable drag-and-drop between status columns.
 * Renders StatusColumns for each status and uses DragOverlay for visual drag feedback.
 *
 * Supports 6-column layout: Backlog, Open, Blocked, In Progress, Needs Review, Done
 * where columns can be computed from issue data (status + blocked dependencies + title patterns).
 */

import {
  DndContext,
  DragOverlay,
  closestCenter,
  PointerSensor,
  KeyboardSensor,
  useSensor,
  useSensors,
  type DragStartEvent,
  type DragEndEvent,
} from '@dnd-kit/core';
import { useState, useMemo, useCallback } from 'react';

import { DraggableIssueCard } from '@/components/DraggableIssueCard';
import { EmptyColumn } from '@/components/EmptyColumn';
import { StatusColumn } from '@/components/StatusColumn';
import { formatStatusLabel } from '@/components/StatusColumn/utils';
import type { FilterState } from '@/hooks/useFilterState';
import type { Issue, Status } from '@/types';

import { DEFAULT_COLUMNS } from './columnConfigs';
import styles from './KanbanBoard.module.css';
import type { KanbanColumnConfig } from './types';

/**
 * Blocked issue info for lookup.
 */
export interface BlockedInfo {
  blockedByCount: number;
  blockedBy: string[];
}

/**
 * Props for the KanbanBoard component.
 */
export interface KanbanBoardProps {
  /** Issues to display in the board */
  issues: Issue[];
  /** Column configurations (default: 6-column kanban layout) */
  columns?: KanbanColumnConfig[];
  /** @deprecated Use columns prop instead. Status columns for backward compatibility */
  statuses?: Status[];
  /** Optional filter state to apply to issues */
  filters?: FilterState;
  /** Callback when card is clicked */
  onIssueClick?: (issue: Issue) => void;
  /** Callback when drag ends - receives issue and new status */
  onDragEnd?: (issueId: string, newStatus: Status, oldStatus: Status) => void;
  /** Additional CSS class name */
  className?: string;
  /** Map of issue ID to blocked info (for showing blocked badges) */
  blockedIssues?: Map<string, BlockedInfo>;
  /** Whether to show blocked issues (default: true) */
  showBlocked?: boolean;
  /** Callback when approve button is clicked on a card in review column */
  onApprove?: (issue: Issue) => void | Promise<void>;
  /** Callback when reject is submitted with comment on a card in review column */
  onReject?: (issue: Issue, comment: string) => void | Promise<void>;
}

/**
 * Convert legacy statuses prop to column configs for backward compatibility.
 * Handles undefined status as 'open' for backward compatibility.
 */
function statusesToColumns(statuses: Status[]): KanbanColumnConfig[] {
  return statuses.map((s) => ({
    id: s,
    label: formatStatusLabel(s),
    filter: (issue: Issue) =>
      s === 'open' ? issue.status === s || issue.status === undefined : issue.status === s,
    targetStatus: s,
  }));
}

/**
 * KanbanBoard displays issues in a horizontal drag-and-drop layout.
 * Issues are grouped by columns (which may be status-based or computed from dependencies).
 * The board uses @dnd-kit for accessible drag-and-drop functionality.
 */
export function KanbanBoard({
  issues,
  columns: propColumns,
  statuses,
  filters,
  onIssueClick,
  onDragEnd,
  className,
  blockedIssues,
  showBlocked = true,
  onApprove,
  onReject,
}: KanbanBoardProps): JSX.Element {
  const [activeIssue, setActiveIssue] = useState<Issue | null>(null);
  const [sourceColumnId, setSourceColumnId] = useState<string | null>(null);

  // Resolve columns: props.columns > props.statuses (legacy) > DEFAULT_COLUMNS
  const columns = useMemo(() => {
    if (propColumns) return propColumns;
    if (statuses) return statusesToColumns(statuses);
    return DEFAULT_COLUMNS;
  }, [propColumns, statuses]);

  // Configure drag sensors with activation constraints
  const sensors = useSensors(
    useSensor(PointerSensor, {
      activationConstraint: { distance: 5 },
    }),
    useSensor(KeyboardSensor)
  );

  // Filter issues based on active filters and blocked visibility
  const filteredIssues = useMemo(() => {
    let result = issues;

    // Filter out blocked issues if showBlocked is false
    if (!showBlocked && blockedIssues) {
      result = result.filter((issue) => !blockedIssues.has(issue.id));
    }

    if (!filters) return result;

    return result.filter((issue) => {
      // Priority filter (exact match)
      if (filters.priority !== undefined && issue.priority !== filters.priority) {
        return false;
      }

      // Type filter (exact match)
      if (filters.type !== undefined && issue.issue_type !== filters.type) {
        return false;
      }

      // Labels filter (issue must have ALL specified labels)
      if (filters.labels !== undefined && filters.labels.length > 0) {
        const issueLabels = issue.labels ?? [];
        if (!filters.labels.every((label) => issueLabels.includes(label))) {
          return false;
        }
      }

      // Search filter (case-insensitive title match)
      if (filters.search !== undefined && filters.search !== '') {
        const searchLower = filters.search.toLowerCase();
        const titleLower = issue.title.toLowerCase();
        if (!titleLower.includes(searchLower)) {
          return false;
        }
      }

      return true;
    });
  }, [issues, filters, showBlocked, blockedIssues]);

  // Group issues by column using filter functions
  const issuesByColumn = useMemo(() => {
    const grouped = new Map<string, Issue[]>();
    // Initialize all columns with empty arrays
    for (const col of columns) {
      grouped.set(col.id, []);
    }
    // Group filtered issues - issue belongs to first matching column
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

  // Handle drag start - store the dragged issue and source column for DragOverlay
  const handleDragStart = useCallback(
    (event: DragStartEvent) => {
      const issue = event.active.data.current?.issue as Issue | undefined;
      if (issue) {
        setActiveIssue(issue);
        // Find which column this issue belongs to
        const blockedInfo = blockedIssues?.get(issue.id);
        for (const col of columns) {
          if (col.filter(issue, blockedInfo)) {
            setSourceColumnId(col.id);
            break;
          }
        }
      }
    },
    [blockedIssues, columns]
  );

  // Handle drag end - enforce restrictions and notify parent of status change
  const handleDragEnd = useCallback(
    (event: DragEndEvent) => {
      const currentSourceColumnId = sourceColumnId;
      setActiveIssue(null);
      setSourceColumnId(null);

      const { active, over } = event;
      if (!over || !onDragEnd) return;

      const issue = active.data.current?.issue as Issue | undefined;
      if (!issue || !currentSourceColumnId) return;

      const targetColumnId = over.id as string;
      const sourceColumn = columns.find((c) => c.id === currentSourceColumnId);
      const targetColumn = columns.find((c) => c.id === targetColumnId);

      // Enforce drag restrictions
      if (
        sourceColumn?.allowedDropTargets &&
        !sourceColumn.allowedDropTargets.includes(targetColumnId)
      ) {
        return; // Drop not allowed
      }

      // Only process if target column has a targetStatus defined
      if (targetColumn?.targetStatus) {
        const newStatus = targetColumn.targetStatus;
        const oldStatus = issue.status ?? 'open';

        // Only call callback if status actually changed
        if (newStatus !== oldStatus) {
          onDragEnd(issue.id, newStatus, oldStatus);
        }
      }
    },
    [onDragEnd, columns, sourceColumnId]
  );

  const rootClassName = className ? `${styles.board} ${className}` : styles.board;

  return (
    <DndContext
      sensors={sensors}
      collisionDetection={closestCenter}
      onDragStart={handleDragStart}
      onDragEnd={handleDragEnd}
    >
      <div className={rootClassName}>
        {columns.map((col) => {
          const colIssues = issuesByColumn.get(col.id) ?? [];
          const columnClassName =
            col.style === 'muted'
              ? styles.mutedColumn
              : col.style === 'highlighted'
                ? styles.highlightedColumn
                : undefined;

          // Determine column type for special columns
          const isBacklogColumn = col.id === 'backlog';
          const isBlockedColumn = col.id === 'blocked';
          const isReviewColumn = col.id === 'review';
          const isMutedColumn = isBacklogColumn || isBlockedColumn;
          const columnType = isBacklogColumn
            ? ('backlog' as const)
            : isReviewColumn
              ? ('review' as const)
              : undefined;

          // Build props conditionally to satisfy exactOptionalPropertyTypes
          const statusColumnProps = {
            status: col.id,
            statusLabel: col.label,
            count: colIssues.length,
            ...(col.headerIcon !== undefined && { headerIcon: col.headerIcon }),
            ...(col.droppableDisabled !== undefined && {
              droppableDisabled: col.droppableDisabled,
            }),
            ...(columnClassName !== undefined && { className: columnClassName }),
            ...(columnType !== undefined && { columnType }),
          };

          return (
            <StatusColumn key={col.id} {...statusColumnProps}>
              {colIssues.length === 0 ? (
                <EmptyColumn status={col.id} />
              ) : (
                colIssues.map((issue) => {
                  const blockedInfo = blockedIssues?.get(issue.id);
                  return (
                    <DraggableIssueCard
                      key={issue.id}
                      issue={issue}
                      columnId={col.id}
                      {...(onIssueClick !== undefined && { onClick: onIssueClick })}
                      {...(blockedInfo !== undefined && {
                        blockedByCount: blockedInfo.blockedByCount,
                        blockedBy: blockedInfo.blockedBy,
                      })}
                      {...(isMutedColumn && { isBacklog: true })}
                      {...(onApprove !== undefined && { onApprove })}
                      {...(onReject !== undefined && { onReject })}
                    />
                  );
                })
              )}
            </StatusColumn>
          );
        })}
      </div>
      <DragOverlay dropAnimation={null}>
        {activeIssue &&
          (() => {
            const blockedInfo = blockedIssues?.get(activeIssue.id);
            const isMutedCard = sourceColumnId === 'backlog' || sourceColumnId === 'blocked';
            return (
              <DraggableIssueCard
                issue={activeIssue}
                isOverlay
                {...(blockedInfo !== undefined && {
                  blockedByCount: blockedInfo.blockedByCount,
                  blockedBy: blockedInfo.blockedBy,
                })}
                {...(isMutedCard && { isBacklog: true })}
              />
            );
          })()}
      </DragOverlay>
    </DndContext>
  );
}
