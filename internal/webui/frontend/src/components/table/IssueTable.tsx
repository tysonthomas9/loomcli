/**
 * IssueTable component for displaying issues in a table format.
 * Foundational component for Phase 4 List/Table View.
 */

import { Fragment, useMemo, useRef } from "react";
import { useStore } from "zustand";

import type { BlockedInfo } from "@/types/issue";
import type { SortDirection } from "@/hooks";
import { useSort } from "@/hooks";
import { useAgentStoreInstance } from "@/hooks/common";
import { useWorkspaceContext } from "@/hooks/workspace";
import { buildEpicLeadClaims } from "@/utils/agentRole";
import type { Issue } from "@/types";

import type { ColumnDef } from "./columns";
import {
  DEFAULT_ISSUE_COLUMNS,
  formatStatus,
  getPriorityClassName,
} from "./columns";
import { IssueRow } from "./IssueRow";
import type { SortState } from "./TableHeader";
import { TableHeader } from "./TableHeader";
import { VirtualizedTableBody } from "./VirtualizedTableBody";
import "./IssueTable.css";

export interface IssueTableProps {
  /** Array of issues to display */
  issues: Issue[];
  /** Custom column definitions (defaults to DEFAULT_ISSUE_COLUMNS) */
  columns?: ColumnDef<Issue>[];
  /** Callback when a row is clicked */
  onRowClick?: (issue: Issue) => void;
  /** ID of the currently selected issue */
  selectedId?: string;
  /** Additional CSS class name */
  className?: string;
  /** Whether to show checkbox column */
  showCheckbox?: boolean;
  /** Set of selected issue IDs */
  selectedIds?: Set<string>;
  /** Callback when checkbox selection changes */
  onSelectionChange?: (issueId: string, selected: boolean) => void;
  /** Enable sorting functionality (default: false) */
  sortable?: boolean;
  /** Initial sort configuration (only used when sortable=true) */
  initialSort?: {
    key: string;
    direction: SortDirection;
  };
  /** Map of issue ID to blocked info (for showing blocked badges) */
  blockedIssues?: Map<string, BlockedInfo>;
  /** Whether to show blocked issues (default: true) */
  showBlocked?: boolean;
  /** Search term for title highlighting */
  searchTerm?: string;
  /** Group task rows under their parent epic */
  groupByEpic?: boolean;
}

interface EpicGroup {
  id: string;
  title: string;
  epic?: Issue;
  children: Issue[];
  firstIndex: number;
}

/**
 * IssueTable displays issues in a semantic HTML table with column definitions,
 * row selection, and click handling.
 */
export function IssueTable({
  issues,
  columns = DEFAULT_ISSUE_COLUMNS,
  onRowClick,
  selectedId,
  className,
  showCheckbox,
  selectedIds,
  onSelectionChange,
  sortable = false,
  initialSort,
  blockedIssues,
  showBlocked = true,
  searchTerm,
  groupByEpic = false,
}: IssueTableProps) {
  // Show repo column only in multi-repo workspaces with "All Workspaces" selected
  const { isMultiRepo, isAllSelected } = useWorkspaceContext();
  const showRepoColumn = isMultiRepo && isAllSelected;
  const agentStore = useAgentStoreInstance();
  const agents = useStore(agentStore, (s) => s.agents);
  const epicLeadClaims = useMemo(
    () =>
      groupByEpic ? buildEpicLeadClaims(agents) : new Map<string, string>(),
    [agents, groupByEpic],
  );
  const effectiveColumns = useMemo(
    () => (showRepoColumn ? columns : columns.filter((c) => c.id !== "repo")),
    [columns, showRepoColumn],
  );

  // Filter out blocked issues if showBlocked is false
  const filteredIssues = useMemo(() => {
    if (showBlocked || !blockedIssues) {
      return issues;
    }
    return issues.filter((issue) => !blockedIssues.has(issue.id));
  }, [issues, blockedIssues, showBlocked]);

  // Use the useSort hook for sorting
  const {
    sortedData,
    sortState: hookSortState,
    handleSort: hookHandleSort,
  } = useSort({
    data: filteredIssues,
    columns: effectiveColumns,
    initialKey: sortable ? (initialSort?.key ?? null) : null,
    initialDirection: initialSort?.direction ?? "asc",
  });

  // Use sorted data when sortable, otherwise filtered issues
  const displayData = sortable ? sortedData : filteredIssues;

  // Use hook state and handlers when sortable, otherwise provide stable defaults
  // Note: Even when sortable=false, TableHeader still handles UI state internally
  // but the data won't actually be sorted since we pass the original issues array
  const sortState: SortState = sortable
    ? hookSortState
    : { key: null, direction: "asc" };
  const handleSort = sortable ? hookHandleSort : () => {};

  const tableClassName = ["issue-table", className].filter(Boolean).join(" ");
  const wrapperRef = useRef<HTMLDivElement>(null);
  const colSpan = effectiveColumns.length + (showCheckbox ? 1 : 0);
  const useVirtualization = !groupByEpic && displayData.length > 100;
  const wrapperClassName = [
    "issue-table__wrapper",
    useVirtualization ? "issue-table__wrapper--virtualized" : "",
  ]
    .filter(Boolean)
    .join(" ");

  const renderIssueRow = (issue: Issue, className?: string) => {
    const blockedInfo = blockedIssues?.get(issue.id);
    const isBlocked =
      blockedInfo !== undefined && blockedInfo.blockedByCount > 0;
    return (
      <IssueRow
        key={issue.id}
        issue={issue}
        columns={effectiveColumns}
        isSelected={
          showCheckbox ? selectedIds?.has(issue.id) : selectedId === issue.id
        }
        isClickable={!!onRowClick}
        onClick={onRowClick}
        showCheckbox={showCheckbox}
        onSelectionChange={onSelectionChange}
        isBlocked={isBlocked}
        blockedInfo={blockedInfo}
        searchTerm={searchTerm}
        className={className}
      />
    );
  };

  const groupedData = useMemo(
    () => buildEpicGroups(displayData),
    [displayData],
  );

  const renderEpicGroupHeader = (group: EpicGroup) => {
    const isClickable = group.epic !== undefined && onRowClick !== undefined;
    const stats = epicGroupStats(group, blockedIssues);
    return (
      <tr
        className={[
          "issue-table__group-row",
          isClickable ? "issue-table__group-row--clickable" : "",
        ]
          .filter(Boolean)
          .join(" ")}
        data-testid={`issue-table-epic-group-${group.id}`}
        tabIndex={isClickable ? 0 : undefined}
        onClick={() => {
          if (group.epic) onRowClick?.(group.epic);
        }}
        onKeyDown={(event) => {
          if (!group.epic || !onRowClick) return;
          if (event.key === "Enter" || event.key === " ") {
            event.preventDefault();
            onRowClick(group.epic);
          }
        }}
      >
        <td className="issue-table__group-cell" colSpan={colSpan}>
          <div className="issue-table__group-content">
            <span className="issue-table__group-label">Epic</span>
            <span className="issue-table__group-id">{group.id}</span>
            <span className="issue-table__group-title">{group.title}</span>
            {group.epic && (
              <>
                <span
                  className={[
                    "issue-table__group-priority",
                    "issue-table__priority",
                    getPriorityClassName(group.epic.priority),
                  ].join(" ")}
                >
                  P{group.epic.priority}
                </span>
                <span className="issue-table__group-status">
                  {formatStatus(group.epic.status)}
                </span>
              </>
            )}
            <span className="issue-table__group-summary">
              {stats.taskCount} {stats.taskCount === 1 ? "task" : "tasks"} ·{" "}
              {stats.doneCount} done · {stats.activeCount} active ·{" "}
              {stats.blockedCount} blocked
            </span>
            {group.epic ? (
              epicLeadClaims.has(group.id) ? (
                <span
                  className="issue-table__runner-badge"
                  title={`Epic run by ${epicLeadClaims.get(group.id)}`}
                  data-testid={`issue-table-runner-${group.id}`}
                >
                  <span
                    className="issue-table__runner-dot"
                    aria-hidden="true"
                  />
                  {epicLeadClaims.get(group.id)}
                </span>
              ) : (
                <span
                  className="issue-table__unclaimed-badge"
                  data-testid={`issue-table-unclaimed-${group.id}`}
                >
                  Unclaimed
                </span>
              )
            ) : null}
          </div>
        </td>
      </tr>
    );
  };

  const renderGroupedBody = () => (
    <tbody className="issue-table__body">
      {groupedData.groups.map((group) => (
        <Fragment key={`group-${group.id}`}>
          {renderEpicGroupHeader(group)}
          {group.children.length === 0 ? (
            <tr className="issue-table__group-empty-row">
              <td className="issue-table__group-empty-cell" colSpan={colSpan}>
                No visible tasks in this epic
              </td>
            </tr>
          ) : (
            group.children.map((issue) =>
              renderIssueRow(issue, "issue-table__row--group-child"),
            )
          )}
        </Fragment>
      ))}
      {groupedData.ungrouped.length > 0 && (
        <>
          <tr
            className="issue-table__group-row"
            data-testid="issue-table-epic-group-ungrouped"
          >
            <td className="issue-table__group-cell" colSpan={colSpan}>
              <div className="issue-table__group-content">
                <span className="issue-table__group-label">Ungrouped</span>
                <span className="issue-table__group-title">
                  Issues without a visible parent epic
                </span>
                <span className="issue-table__group-summary">
                  {groupedData.ungrouped.length}{" "}
                  {groupedData.ungrouped.length === 1 ? "issue" : "issues"}
                </span>
              </div>
            </td>
          </tr>
          {groupedData.ungrouped.map((issue) =>
            renderIssueRow(issue, "issue-table__row--group-child"),
          )}
        </>
      )}
    </tbody>
  );

  return (
    <div className={wrapperClassName} ref={wrapperRef}>
      <table className={tableClassName} data-testid="issue-table">
        <TableHeader
          columns={effectiveColumns}
          sortState={sortState}
          onSort={handleSort}
          {...(showCheckbox !== undefined && { showCheckbox })}
        />
        {displayData.length === 0 ? (
          <tbody className="issue-table__body">
            <tr className="issue-table__empty-row">
              <td
                colSpan={colSpan}
                className="issue-table__empty-cell"
                data-testid="issue-table-empty"
              >
                No issues to display
              </td>
            </tr>
          </tbody>
        ) : useVirtualization ? (
          <VirtualizedTableBody
            className="issue-table__body"
            count={displayData.length}
            scrollContainerRef={wrapperRef}
            renderRow={(index) => renderIssueRow(displayData[index]!)}
            colSpan={colSpan}
          />
        ) : groupByEpic ? (
          renderGroupedBody()
        ) : (
          <tbody className="issue-table__body">
            {displayData.map((issue) => renderIssueRow(issue))}
          </tbody>
        )}
      </table>
    </div>
  );
}

export default IssueTable;

function buildEpicGroups(issues: Issue[]): {
  groups: EpicGroup[];
  ungrouped: Issue[];
} {
  const groupsByID = new Map<string, EpicGroup>();
  const ungrouped: Issue[] = [];

  issues.forEach((issue, index) => {
    if (isEpic(issue)) {
      groupsByID.set(issue.id, {
        id: issue.id,
        title: issue.title,
        epic: issue,
        children: [],
        firstIndex: index,
      });
    }
  });

  issues.forEach((issue, index) => {
    if (isEpic(issue)) return;

    const parentID = normalizedParentID(issue);
    if (!parentID) {
      ungrouped.push(issue);
      return;
    }

    let group = groupsByID.get(parentID);
    if (!group) {
      group = {
        id: parentID,
        title: issue.parent_title || parentID,
        children: [],
        firstIndex: index,
      };
      groupsByID.set(parentID, group);
    }
    group.children.push(issue);
    group.firstIndex = Math.min(group.firstIndex, index);
  });

  const groups = [...groupsByID.values()]
    .filter((group) => group.epic || group.children.length > 0)
    .sort((a, b) => a.firstIndex - b.firstIndex || a.id.localeCompare(b.id));

  return { groups, ungrouped };
}

function isEpic(issue: Issue): boolean {
  return (issue.issue_type ?? "").toLowerCase() === "epic";
}

function normalizedParentID(issue: Issue): string {
  const parent = issue.parent;
  if (typeof parent === "string") return parent.trim();
  return "";
}

function epicGroupStats(
  group: EpicGroup,
  blockedIssues?: Map<string, BlockedInfo>,
): {
  taskCount: number;
  doneCount: number;
  activeCount: number;
  blockedCount: number;
} {
  const taskCount = group.children.length;
  let doneCount = 0;
  let activeCount = 0;
  let blockedCount = 0;

  for (const issue of group.children) {
    const status = issue.status ?? "";
    const blockedInfo = blockedIssues?.get(issue.id);
    const isBlocked =
      issue.is_blocked === true ||
      status === "blocked" ||
      (blockedInfo !== undefined && blockedInfo.blockedByCount > 0);

    if (status === "closed") {
      doneCount += 1;
    } else if (isBlocked) {
      blockedCount += 1;
    } else if (status === "in_progress" || status === "review") {
      activeCount += 1;
    }
  }

  return { taskCount, doneCount, activeCount, blockedCount };
}
