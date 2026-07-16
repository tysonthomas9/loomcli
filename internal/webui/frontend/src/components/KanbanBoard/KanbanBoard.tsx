/**
 * KanbanBoard component for displaying issues in a drag-and-drop Kanban layout.
 * Wraps content in @dnd-kit DndContext to enable drag-and-drop between status columns.
 * Renders StatusColumns for each status and uses DragOverlay for visual drag feedback.
 *
 * Supports 6-column layout: Backlog, Open, Blocked, In Progress, Review, Done
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
} from "@dnd-kit/core";
import { useState, useMemo, useCallback, useRef, createRef } from "react";

import { DraggableIssueCard } from "@/components/DraggableIssueCard";
import { EmptyColumn } from "@/components/EmptyColumn";
import { EmptyWorkspaceBoard } from "@/components/EmptyWorkspaceBoard";
import { StatusColumn, VirtualizedCardList } from "@/components/StatusColumn";
import type { FilterState } from "@/hooks/issues";
import type { Issue, Status } from "@/types";
import type { BlockedInfo } from "@/types/issue";

import { issueMatchesSearch } from "@/utils/issueSearch";

import { DEFAULT_COLUMNS } from "./columnConfigs";
import { visibleKanbanColumns } from "./columnVisibility";
import styles from "./KanbanBoard.module.css";
import type { KanbanColumnConfig } from "./types";

const LOAD_MORE_BATCH = 50;

/**
 * Props for the KanbanBoard component.
 */
export interface KanbanBoardProps {
  /** Issues to display in the board */
  issues: Issue[];
  /** Column configurations (default: 6-column kanban layout) */
  columns?: KanbanColumnConfig[];
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
  /** Set of issue IDs with pending optimistic updates */
  pendingIds?: Set<string>;
  /** Whether the app is in multi-repo mode (affects empty state text) */
  isMultiRepo?: boolean;
  /** Hide columns with no issues */
  compactColumns?: boolean;
}

/**
 * KanbanBoard displays issues in a horizontal drag-and-drop layout.
 * Issues are grouped by columns (which may be status-based or computed from dependencies).
 * The board uses @dnd-kit for accessible drag-and-drop functionality.
 */
export function KanbanBoard({
  issues,
  columns: propColumns,
  filters,
  onIssueClick,
  onDragEnd,
  className,
  blockedIssues,
  showBlocked = true,
  pendingIds,
  isMultiRepo,
  compactColumns = false,
}: KanbanBoardProps): JSX.Element {
  const [activeIssue, setActiveIssue] = useState<Issue | null>(null);
  const [sourceColumnId, setSourceColumnId] = useState<string | null>(null);
  const [columnDisplayLimits, setColumnDisplayLimits] = useState<
    Map<string, number>
  >(new Map());

  // Refs for column scroll containers (used by VirtualizedCardList)
  const columnRefsMap = useRef(
    new Map<string, React.RefObject<HTMLDivElement | null>>(),
  );
  const getColumnRef = useCallback((colId: string) => {
    let ref = columnRefsMap.current.get(colId);
    if (!ref) {
      ref = createRef<HTMLDivElement | null>();
      columnRefsMap.current.set(colId, ref);
    }
    return ref;
  }, []);

  // Resolve columns: props.columns > DEFAULT_COLUMNS
  const columns = useMemo(() => {
    if (propColumns) return propColumns;
    return DEFAULT_COLUMNS;
  }, [propColumns]);

  // Configure drag sensors with activation constraints
  const sensors = useSensors(
    useSensor(PointerSensor, {
      activationConstraint: { distance: 5 },
    }),
    useSensor(KeyboardSensor),
  );

  // Filter issues based on active filters and blocked visibility
  const filteredIssues = useMemo(() => {
    let result = issues;

    // Filter out blocked issues if showBlocked is false
    if (!showBlocked && blockedIssues) {
      result = result.filter((issue) => !blockedIssues.has(issue.id));
    }

    if (!filters) return result;

    if (filters.search !== undefined && filters.search !== "") {
      // Flat board has no lanes to keep intact, so match each card directly —
      // filterIssuesBySearch's epic-sibling inclusion (used by grouped/lane
      // views) would otherwise flood the board with an epic's child tasks.
      result = result.filter((issue) =>
        issueMatchesSearch(issue, filters.search as string),
      );
    }

    return result.filter((issue) => {
      // Priority filter (exact match)
      if (
        filters.priority !== undefined &&
        issue.priority !== filters.priority
      ) {
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

  const displayColumns = useMemo(
    () => visibleKanbanColumns(columns, issuesByColumn, compactColumns),
    [columns, issuesByColumn, compactColumns],
  );

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
    [blockedIssues, columns],
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
        const oldStatus = issue.status ?? "open";

        // Only call callback if status actually changed
        if (newStatus !== oldStatus) {
          onDragEnd(issue.id, newStatus, oldStatus);
        }
      }
    },
    [onDragEnd, columns, sourceColumnId],
  );

  // Board-level empty state: show when all issues have been filtered out
  if (filteredIssues.length === 0) {
    // Determine if user-driven filters caused the empty state (not just showBlocked)
    const hasUserFilters =
      filters !== undefined &&
      (filters.priority !== undefined ||
        filters.type !== undefined ||
        (filters.labels !== undefined && filters.labels.length > 0) ||
        (filters.search !== undefined && filters.search !== ""));
    return (
      <EmptyWorkspaceBoard
        {...(isMultiRepo !== undefined && { isMultiRepo })}
        hasFiltersActive={hasUserFilters}
      />
    );
  }

  const rootClassName = className
    ? `${styles.board} ${className}`
    : styles.board;

  return (
    <DndContext
      sensors={sensors}
      collisionDetection={closestCenter}
      onDragStart={handleDragStart}
      onDragEnd={handleDragEnd}
    >
      <div
        className={rootClassName}
        data-compact-columns={compactColumns || undefined}
      >
        {displayColumns.map((col) => {
          const allColIssues = issuesByColumn.get(col.id) ?? [];
          const columnClassName =
            col.style === "muted"
              ? styles.mutedColumn
              : col.style === "highlighted"
                ? styles.highlightedColumn
                : undefined;

          // Determine column type for special columns
          const isBacklogColumn = col.id === "backlog";
          const isBlockedColumn = col.id === "blocked";
          const isMutedColumn = isBacklogColumn || isBlockedColumn;
          const columnType = isBacklogColumn ? ("backlog" as const) : undefined;

          // Apply display limit for columns with defaultLimit
          const hasLimit =
            col.defaultLimit !== undefined && col.defaultLimit > 0;
          const currentLimit =
            columnDisplayLimits.get(col.id) ?? col.defaultLimit ?? Infinity;
          const shouldTruncate = hasLimit && allColIssues.length > currentLimit;
          const colIssues = shouldTruncate
            ? allColIssues
                .slice()
                .sort((a, b) => {
                  const timeB = b.updated_at
                    ? new Date(b.updated_at).getTime()
                    : 0;
                  const timeA = a.updated_at
                    ? new Date(a.updated_at).getTime()
                    : 0;
                  return (
                    (isNaN(timeB) ? 0 : timeB) - (isNaN(timeA) ? 0 : timeA)
                  );
                })
                .slice(0, currentLimit)
            : allColIssues;

          // Footer action for columns with defaultLimit and more items than the limit
          let footerAction: JSX.Element | undefined;
          if (hasLimit && allColIssues.length > col.defaultLimit!) {
            const remaining = allColIssues.length - currentLimit;
            if (remaining > 0) {
              const batch = Math.min(LOAD_MORE_BATCH, remaining);
              footerAction = (
                <button
                  type="button"
                  onClick={() => {
                    setColumnDisplayLimits((prev) => {
                      const next = new Map(prev);
                      next.set(col.id, currentLimit + LOAD_MORE_BATCH);
                      return next;
                    });
                  }}
                >
                  {`Load ${batch} more · ${allColIssues.length} total`}
                </button>
              );
            } else {
              footerAction = (
                <button
                  type="button"
                  onClick={() => {
                    setColumnDisplayLimits((prev) => {
                      const next = new Map(prev);
                      next.delete(col.id);
                      return next;
                    });
                  }}
                >
                  Show recent
                </button>
              );
            }
          }

          // Virtualize columns with >50 cards for performance
          const useVirtualization = colIssues.length > 50;
          const columnContentRef = useVirtualization
            ? getColumnRef(col.id)
            : undefined;

          // Build props conditionally to satisfy exactOptionalPropertyTypes
          const statusColumnProps = {
            status: col.id,
            statusLabel: col.label,
            count: allColIssues.length,
            ...(col.headerIcon !== undefined && { headerIcon: col.headerIcon }),
            ...(col.droppableDisabled !== undefined && {
              droppableDisabled: col.droppableDisabled,
            }),
            ...(columnClassName !== undefined && {
              className: columnClassName,
            }),
            ...(columnType !== undefined && { columnType }),
            ...(footerAction !== undefined && { footerAction }),
            ...(columnContentRef !== undefined && {
              contentRef: columnContentRef,
            }),
          };

          const renderCard = (issue: Issue) => {
            const blockedInfo = blockedIssues?.get(issue.id);
            return (
              <DraggableIssueCard
                key={issue.id}
                issue={issue}
                columnId={col.id}
                {...(onIssueClick !== undefined && {
                  onClick: onIssueClick,
                })}
                {...(blockedInfo !== undefined && {
                  blockedByCount: blockedInfo.blockedByCount,
                  blockedBy: blockedInfo.blockedBy,
                  ...(blockedInfo.blockedByDetails !== undefined && {
                    blockedByDetails: blockedInfo.blockedByDetails,
                  }),
                })}
                {...(isMutedColumn && { isBacklog: true })}
                {...(pendingIds?.has(issue.id) && { isPending: true })}
              />
            );
          };

          return (
            <StatusColumn key={col.id} {...statusColumnProps}>
              {colIssues.length === 0 ? (
                <EmptyColumn status={col.id} />
              ) : useVirtualization && columnContentRef ? (
                <VirtualizedCardList
                  count={colIssues.length}
                  scrollContainerRef={columnContentRef}
                  renderItem={(index) => renderCard(colIssues[index]!)}
                />
              ) : (
                colIssues.map((issue) => renderCard(issue))
              )}
            </StatusColumn>
          );
        })}
      </div>
      <DragOverlay dropAnimation={null}>
        {activeIssue &&
          (() => {
            const blockedInfo = blockedIssues?.get(activeIssue.id);
            const isMutedCard =
              sourceColumnId === "backlog" || sourceColumnId === "blocked";
            return (
              <DraggableIssueCard
                issue={activeIssue}
                isOverlay
                {...(blockedInfo !== undefined && {
                  blockedByCount: blockedInfo.blockedByCount,
                  blockedBy: blockedInfo.blockedBy,
                  ...(blockedInfo.blockedByDetails !== undefined && {
                    blockedByDetails: blockedInfo.blockedByDetails,
                  }),
                })}
                {...(isMutedCard && { isBacklog: true })}
              />
            );
          })()}
      </DragOverlay>
    </DndContext>
  );
}
