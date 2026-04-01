import { ErrorBoundary, IssueTable, BulkActionToolbar } from "@/components";
import type { BlockedInfo } from "@/components/KanbanBoard";
import type { ViewMode } from "@/components/ViewSwitcher";
import type { Issue } from "@/types";

export interface TablePageProps {
  filteredIssues: Issue[];
  selectedIds: Set<string>;
  onSelectionChange: (issueId: string, selected: boolean) => void;
  onClearSelection: () => void;
  onIssueClick: (issue: Issue) => void;
  searchTerm: string;
  activeView: ViewMode;
  selectedIssueId?: string | null | undefined;
  blockedIssuesMap?: Map<string, BlockedInfo> | undefined;
  showBlocked?: boolean | undefined;
}

export function TablePage({
  filteredIssues,
  selectedIds,
  onSelectionChange,
  onClearSelection,
  onIssueClick,
  searchTerm,
  activeView,
  selectedIssueId,
  blockedIssuesMap,
  showBlocked,
}: TablePageProps) {
  return (
    <ErrorBoundary resetOnChange={[activeView]}>
      <IssueTable
        issues={filteredIssues}
        sortable
        showCheckbox
        selectedIds={selectedIds}
        onSelectionChange={onSelectionChange}
        onRowClick={onIssueClick}
        searchTerm={searchTerm}
        {...(selectedIssueId != null && {
          selectedId: selectedIssueId,
        })}
        {...(blockedIssuesMap !== undefined && {
          blockedIssues: blockedIssuesMap,
        })}
        {...(showBlocked !== undefined && {
          showBlocked,
        })}
      />
      <BulkActionToolbar
        selectedIds={selectedIds}
        onClearSelection={onClearSelection}
      />
    </ErrorBoundary>
  );
}
