/**
 * IssueTable component for displaying issues in a table format.
 * Foundational component for Phase 4 List/Table View.
 */

import { useMemo, useRef } from "react";

import type { BlockedInfo } from "@/components/KanbanBoard";
import { useSort, SortDirection } from "@/hooks";
import { useWorkspaceContext } from "@/hooks/useWorkspaceContext";
import type { Issue } from "@/types";

import { ColumnDef, DEFAULT_ISSUE_COLUMNS } from "./columns";
import { IssueRow } from "./IssueRow";
import { TableHeader, SortState } from "./TableHeader";
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
  /** Enable sorting functionality (default: false for backwards compatibility) */
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
}: IssueTableProps) {
  // Show repo column only in multi-repo workspaces with "All Workspaces" selected
  const { isMultiRepo, isAllSelected } = useWorkspaceContext();
  const showRepoColumn = isMultiRepo && isAllSelected;
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
  const useVirtualization = displayData.length > 100;
  const wrapperClassName = [
    "issue-table__wrapper",
    useVirtualization ? "issue-table__wrapper--virtualized" : "",
  ]
    .filter(Boolean)
    .join(" ");

  const renderIssueRow = (index: number) => {
    const issue = displayData[index]!;
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
      />
    );
  };

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
            renderRow={renderIssueRow}
            colSpan={colSpan}
          />
        ) : (
          <tbody className="issue-table__body">
            {displayData.map((_issue, index) => renderIssueRow(index))}
          </tbody>
        )}
      </table>
    </div>
  );
}

export default IssueTable;
