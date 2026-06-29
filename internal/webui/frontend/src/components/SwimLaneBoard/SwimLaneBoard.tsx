/**
 * SwimLaneBoard component for displaying issues grouped into horizontal swim lanes.
 * Each swim lane contains status columns, enabling a two-dimensional view of issues
 * organized by both their grouping (epic, assignee, priority, type, label) and workflow status.
 * When groupBy='none', delegates to KanbanBoard for a flat view.
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
import { useState, useMemo, useCallback, useEffect } from "react";
import { useStore } from "zustand";

import { useAgentStoreInstance } from "@/hooks/common";
import { useWorkspaceContext } from "@/hooks/workspace";
import { buildEpicLeadClaims } from "@/utils/agentRole";
import { wsGet, wsSet } from "@/utils/scopedStorage";
import { DraggableIssueCard } from "@/components/DraggableIssueCard";
import { EmptyWorkspaceBoard } from "@/components/EmptyWorkspaceBoard";
import { KanbanBoard, type KanbanColumnConfig } from "@/components/KanbanBoard";
import { SwimLane } from "@/components/SwimLane";
import type { FilterState } from "@/hooks/issues";
import type { Issue, Status } from "@/types";
import type { BlockedInfo } from "@/types/issue";

import {
  groupIssuesByField,
  sortLanes,
  type GroupByField,
  type LaneGroup,
} from "./groupingUtils";
import {
  loadCollapsedLanes,
  loadCompactColumns,
  saveCollapsedLanes,
  saveCompactColumns,
  resolveColumns,
} from "./swimLaneStorage";
import styles from "./SwimLaneBoard.module.css";

/**
 * Props for the SwimLaneBoard component.
 */
export interface SwimLaneBoardProps {
  /** Issues to display in the board */
  issues: Issue[];
  /** Field to group issues by */
  groupBy: GroupByField;
  /** Column configurations (default: 5-column kanban layout) */
  columns?: KanbanColumnConfig[];
  /** Optional filter state (used by KanbanBoard fallback) */
  filters?: FilterState;
  /** Callback when an issue card is clicked */
  onIssueClick?: (issue: Issue) => void;
  /** Callback when drag ends - receives issue ID and new/old status */
  onDragEnd?: (issueId: string, newStatus: Status, oldStatus: Status) => void;
  /** Additional CSS class name */
  className?: string;
  /** Map of issue ID to blocked info */
  blockedIssues?: Map<string, BlockedInfo>;
  /** Whether to show blocked issues (default: true) */
  showBlocked?: boolean;
  /** Sort lanes by 'title' or 'count' (default: 'title') */
  sortLanesBy?: "title" | "count";
  /** Default collapsed state for new lanes (default: false) */
  defaultCollapsed?: boolean;
  /** Maximum cards to show per column in swim lanes (default: 5) */
  cardLimit?: number;
  /** Set of issue IDs with pending optimistic updates */
  pendingIds?: Set<string>;
  /** Whether the app is in multi-repo mode (affects empty state text) */
  isMultiRepo?: boolean;
  /** Whether caller-applied filters/search are active */
  hasFiltersActive?: boolean;
}

/**
 * SwimLaneBoard displays issues grouped into horizontal swim lanes.
 * Each lane represents a grouping (epic, assignee, priority, type, or label)
 * and contains status columns for drag-and-drop workflow management.
 */
export function SwimLaneBoard({
  issues,
  groupBy,
  columns: propColumns,
  filters,
  onIssueClick,
  onDragEnd,
  className,
  blockedIssues,
  showBlocked = true,
  sortLanesBy = "title",
  defaultCollapsed = false,
  cardLimit,
  pendingIds,
  isMultiRepo,
  hasFiltersActive,
}: SwimLaneBoardProps): JSX.Element {
  const { workspaceId } = useWorkspaceContext();
  const [compactColumns, setCompactColumns] = useState(() =>
    loadCompactColumns(workspaceId),
  );

  // Reload the persisted value when the workspace changes (covers in-place
  // switches where this component is not remounted).
  useEffect(() => {
    setCompactColumns(loadCompactColumns(workspaceId));
  }, [workspaceId]);

  // Persist only on the user toggle. A second save-on-change effect (keyed on
  // workspaceId) would also fire on a workspace switch with the PREVIOUS value
  // and clobber the new workspace's saved preference before the reload effect
  // above reads it.
  const toggleCompactColumns = useCallback(() => {
    setCompactColumns((value) => {
      const next = !value;
      saveCompactColumns(next, workspaceId);
      return next;
    });
  }, [workspaceId]);

  const columns = useMemo(
    () => resolveColumns(propColumns, groupBy),
    [propColumns, groupBy],
  );

  const compactToggle = (
    <div className={styles.compactToggle}>
      <span className={styles.compactToggleLabel} id="compact-columns-label">
        Compact
      </span>
      <button
        type="button"
        role="switch"
        className={styles.compactSwitch}
        aria-labelledby="compact-columns-label"
        aria-checked={compactColumns}
        onClick={toggleCompactColumns}
        data-testid="toggle-compact-columns"
      >
        <span className={styles.compactSwitchThumb} aria-hidden="true" />
      </button>
    </div>
  );

  // When groupBy='none', delegate to KanbanBoard
  if (groupBy === "none") {
    // Build props conditionally to satisfy exactOptionalPropertyTypes
    const kanbanProps = {
      issues,
      columns,
      showBlocked,
      compactColumns,
      ...(filters !== undefined && { filters }),
      ...(onIssueClick !== undefined && { onIssueClick }),
      ...(onDragEnd !== undefined && { onDragEnd }),
      ...(blockedIssues !== undefined && { blockedIssues }),
      ...(pendingIds !== undefined && { pendingIds }),
      ...(isMultiRepo !== undefined && { isMultiRepo }),
    };
    return (
      <div
        className={[styles.swimLaneBoard, className].filter(Boolean).join(" ")}
        data-testid="swim-lane-board"
        data-compact-columns={compactColumns || undefined}
      >
        <div
          className={styles.toolbar}
          role="toolbar"
          aria-label="Board controls"
        >
          {compactToggle}
        </div>
        <KanbanBoard {...kanbanProps} />
      </div>
    );
  }

  // Build props conditionally to satisfy exactOptionalPropertyTypes
  const contentProps = {
    issues,
    groupBy,
    columns,
    showBlocked,
    sortLanesBy,
    defaultCollapsed,
    compactColumns,
    compactToggle,
    ...(onIssueClick !== undefined && { onIssueClick }),
    ...(onDragEnd !== undefined && { onDragEnd }),
    ...(className !== undefined && { className }),
    ...(blockedIssues !== undefined && { blockedIssues }),
    ...(cardLimit !== undefined && { cardLimit }),
    ...(pendingIds !== undefined && { pendingIds }),
    ...(isMultiRepo !== undefined && { isMultiRepo }),
    ...(hasFiltersActive !== undefined && { hasFiltersActive }),
  };

  return <SwimLaneBoardContent {...contentProps} />;
}

/**
 * Internal component that handles the actual swim lane rendering.
 * Separated to allow hooks after early return in main component.
 */
function SwimLaneBoardContent({
  issues,
  groupBy,
  columns,
  onIssueClick,
  onDragEnd,
  className,
  blockedIssues,
  showBlocked,
  sortLanesBy,
  defaultCollapsed,
  cardLimit,
  pendingIds,
  isMultiRepo,
  hasFiltersActive,
  compactColumns,
  compactToggle,
}: Omit<SwimLaneBoardProps, "filters" | "groupBy"> & {
  groupBy: Exclude<GroupByField, "none">;
  columns: KanbanColumnConfig[];
  compactColumns: boolean;
  compactToggle: JSX.Element;
}): JSX.Element {
  const { workspaceId } = useWorkspaceContext();
  // Lead claims for epic lane headers (Aether design, pin 10): show which
  // lead is running each epic, or "Unclaimed" when nobody has bound to it.
  const agentStore = useAgentStoreInstance();
  const agents = useStore(agentStore, (s) => s.agents);
  const epicLeadClaims = useMemo(
    () => (groupBy === "epic" ? buildEpicLeadClaims(agents) : undefined),
    [groupBy, agents],
  );
  const [activeIssue, setActiveIssue] = useState<Issue | null>(null);
  const [sourceColumnId, setSourceColumnId] = useState<string | null>(null);
  const [showCompletedLanes, setShowCompletedLanes] = useState(() => {
    if (!workspaceId) return false;
    return wsGet(workspaceId, "swimlane-show-completed") === "true";
  });
  // Track lanes that have been toggled from their default state.
  // When defaultCollapsed=true, this tracks lanes that were EXPANDED (toggled to open).
  // When defaultCollapsed=false, this tracks lanes that were COLLAPSED (toggled to closed).
  // Initialize from scoped localStorage for persistence across page refreshes.
  const [toggledLanes, setToggledLanes] = useState<Set<string>>(() =>
    loadCollapsedLanes(groupBy, workspaceId),
  );

  // Persist showCompletedLanes to scoped localStorage
  useEffect(() => {
    if (workspaceId) {
      wsSet(workspaceId, "swimlane-show-completed", String(showCompletedLanes));
    }
  }, [showCompletedLanes, workspaceId]);

  // Persist toggledLanes to scoped localStorage when it changes
  useEffect(() => {
    saveCollapsedLanes(groupBy, toggledLanes, workspaceId);
  }, [toggledLanes, groupBy, workspaceId]);

  // When groupBy changes, reset toggledLanes from scoped localStorage for the new groupBy mode
  useEffect(() => {
    setToggledLanes(loadCollapsedLanes(groupBy, workspaceId));
  }, [groupBy, workspaceId]);

  // Configure drag sensors with activation constraints
  const sensors = useSensors(
    useSensor(PointerSensor, {
      activationConstraint: { distance: 5 },
    }),
    useSensor(KeyboardSensor),
  );

  // Filter issues based on blocked visibility
  const filteredIssues = useMemo(() => {
    if (showBlocked || !blockedIssues) return issues;
    return issues.filter((issue) => !blockedIssues.has(issue.id));
  }, [issues, showBlocked, blockedIssues]);

  // Group and sort lanes
  const allLanes = useMemo((): LaneGroup[] => {
    const grouped = groupIssuesByField(filteredIssues, groupBy);
    return sortLanes(grouped, sortLanesBy ?? "title");
  }, [filteredIssues, groupBy, sortLanesBy]);

  // Split lanes into active and completed (all issues closed)
  const { lanes, completedLaneCount } = useMemo(() => {
    if (groupBy !== "epic") return { lanes: allLanes, completedLaneCount: 0 };
    const active: LaneGroup[] = [];
    let completed = 0;
    for (const lane of allLanes) {
      // A lane is "completed" when every card is closed — or when it's an
      // empty lane whose epic itself is closed (a zero-child closed epic
      // would otherwise render a full empty lane forever).
      const allClosed =
        lane.issues.length > 0
          ? lane.issues.every((i) => i.status === "closed")
          : lane.groupIssue?.status === "closed";
      if (allClosed) {
        completed++;
        if (showCompletedLanes) active.push(lane);
      } else {
        active.push(lane);
      }
    }
    return { lanes: active, completedLaneCount: completed };
  }, [allLanes, groupBy, showCompletedLanes]);

  // Toggle lane collapse state - adds/removes from toggled set
  const toggleLaneCollapse = useCallback((laneId: string) => {
    setToggledLanes((prev) => {
      const next = new Set(prev);
      if (next.has(laneId)) {
        next.delete(laneId);
      } else {
        next.add(laneId);
      }
      return next;
    });
  }, []);

  // Determine if a lane is collapsed based on defaultCollapsed and toggled state
  const isLaneCollapsed = useCallback(
    (laneId: string): boolean => {
      const isToggled = toggledLanes.has(laneId);
      // If defaultCollapsed=true, lanes start collapsed, toggling opens them
      // If defaultCollapsed=false, lanes start expanded, toggling closes them
      return defaultCollapsed ? !isToggled : isToggled;
    },
    [toggledLanes, defaultCollapsed],
  );

  // Expand all lanes
  const expandAll = useCallback(() => {
    if (defaultCollapsed) {
      // When defaultCollapsed=true, all lanes need to be in toggled set to be expanded
      const allLaneIds = new Set(lanes.map((lane) => lane.id));
      setToggledLanes(allLaneIds);
    } else {
      // When defaultCollapsed=false, clear toggled set to expand all
      setToggledLanes(new Set());
    }
  }, [lanes, defaultCollapsed]);

  // Collapse all lanes
  const collapseAll = useCallback(() => {
    if (defaultCollapsed) {
      // When defaultCollapsed=true, clear toggled set to collapse all
      setToggledLanes(new Set());
    } else {
      // When defaultCollapsed=false, add all lanes to toggled set to collapse them
      const allLaneIds = new Set(lanes.map((lane) => lane.id));
      setToggledLanes(allLaneIds);
    }
  }, [lanes, defaultCollapsed]);

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

  // Board-level empty state in swim lane mode.
  if (filteredIssues.length === 0) {
    return (
      <EmptyWorkspaceBoard
        {...(isMultiRepo !== undefined && { isMultiRepo })}
        hasFiltersActive={hasFiltersActive === true}
      />
    );
  }

  // All epic lanes completed and hidden — show a reveal prompt instead of empty board
  if (lanes.length === 0 && groupBy === "epic" && completedLaneCount > 0) {
    return (
      <div className={styles.allCompleteState} data-testid="all-epics-complete">
        <p>All epics are complete.</p>
        <button
          type="button"
          className={styles.toolbarButton}
          onClick={() => setShowCompletedLanes(true)}
          data-testid="toggle-completed-lanes"
        >
          Show {completedLaneCount} completed{" "}
          {completedLaneCount !== 1 ? "epics" : "epic"}
        </button>
      </div>
    );
  }

  const rootClassName = [styles.swimLaneBoard, className]
    .filter(Boolean)
    .join(" ");

  return (
    <DndContext
      sensors={sensors}
      collisionDetection={closestCenter}
      onDragStart={handleDragStart}
      onDragEnd={handleDragEnd}
    >
      <div
        className={rootClassName}
        data-testid="swim-lane-board"
        data-compact-columns={compactColumns || undefined}
      >
        {/* Toolbar: compact mode + expand/collapse + completed epic toggle */}
        <div
          className={styles.toolbar}
          role="toolbar"
          aria-label="Lane controls"
        >
          {compactToggle}
          {(lanes.length > 1 ||
            (groupBy === "epic" && completedLaneCount > 0)) && (
            <span className={styles.toolbarDivider} aria-hidden="true" />
          )}
          {lanes.length > 1 && (
            <>
              <button
                type="button"
                className={styles.toolbarButton}
                onClick={expandAll}
                aria-label="Expand all lanes"
                data-testid="expand-all-lanes"
              >
                Expand All
              </button>
              <button
                type="button"
                className={styles.toolbarButton}
                onClick={collapseAll}
                aria-label="Collapse all lanes"
                data-testid="collapse-all-lanes"
              >
                Collapse All
              </button>
            </>
          )}
          {groupBy === "epic" && completedLaneCount > 0 && (
            <button
              type="button"
              className={styles.toolbarButton}
              onClick={() => setShowCompletedLanes((v) => !v)}
              data-testid="toggle-completed-lanes"
            >
              {showCompletedLanes
                ? "Hide Completed"
                : `${completedLaneCount} Completed`}
            </button>
          )}
        </div>
        {lanes.map((lane) => {
          // Epic lanes get a runner badge; the synthetic Ungrouped lane and
          // non-epic groupings do not.
          const epicKey =
            lane.groupIssue?.id ?? lane.id.replace(/^lane-epic-/, "");
          const epicRunner =
            epicLeadClaims !== undefined && epicKey !== "__ungrouped__"
              ? (epicLeadClaims.get(epicKey) ?? null)
              : undefined;
          // Build props conditionally to satisfy exactOptionalPropertyTypes
          const laneProps = {
            id: lane.id,
            title: lane.title,
            issues: lane.issues,
            columns,
            isCollapsed: isLaneCollapsed(lane.id),
            onToggleCollapse: () => toggleLaneCollapse(lane.id),
            ...(epicRunner !== undefined && { epicRunner }),
            ...(onIssueClick !== undefined && { onIssueClick }),
            ...(lane.groupIssue !== undefined &&
              onIssueClick !== undefined && {
                headerIssue: lane.groupIssue,
                onHeaderIssueClick: onIssueClick,
              }),
            ...(blockedIssues !== undefined && { blockedIssues }),
            ...(showBlocked !== undefined && { showBlocked }),
            ...(cardLimit !== undefined && { cardLimit }),
            ...(pendingIds !== undefined && { pendingIds }),
            compactColumns,
          };
          return <SwimLane key={lane.id} {...laneProps} />;
        })}
      </div>
      <DragOverlay dropAnimation={null}>
        {activeIssue &&
          (() => {
            const blockedInfo = blockedIssues?.get(activeIssue.id);
            return (
              <DraggableIssueCard
                issue={activeIssue}
                isOverlay
                {...(blockedInfo !== undefined && {
                  blockedByCount: blockedInfo.blockedByCount,
                  blockedBy: blockedInfo.blockedBy,
                })}
              />
            );
          })()}
      </DragOverlay>
    </DndContext>
  );
}
