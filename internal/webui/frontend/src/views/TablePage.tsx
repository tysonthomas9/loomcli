import { ErrorBoundary } from "@/components/ErrorBoundary/ErrorBoundary";
import { IssueTable } from "@/components/table/IssueTable";
import { BulkActionToolbar } from "@/components/BulkActionToolbar/BulkActionToolbar";
import { IssueViewGuard } from "@/components/IssueViewGuard/IssueViewGuard";
import { useSelection } from "@/hooks/issues/useSelection";
import {
  useWorkspaceViewData,
  useWorkspaceViewActions,
} from "@/contexts/WorkspaceViewContext";

export function TablePage() {
  const {
    filteredIssues,
    issues,
    isLoading,
    error,
    retryCount,
    nextRetryAt,
    isMultiRepo,
    debouncedSearch,
    activeView,
    selectedIssueId,
    blockedIssuesMap,
    filters,
  } = useWorkspaceViewData();

  const { handleIssueClick, refetch } = useWorkspaceViewActions();

  const {
    selectedIds,
    toggleSelection,
    deselectAll: clearSelection,
  } = useSelection({ visibleItems: filteredIssues });

  return (
    <ErrorBoundary resetOnChange={[activeView]}>
      <IssueViewGuard
        issues={issues}
        isLoading={isLoading}
        error={error}
        retryCount={retryCount}
        nextRetryAt={nextRetryAt}
        isMultiRepo={isMultiRepo}
        onRetry={refetch}
        loadingVariant="table"
      >
        <IssueTable
          issues={filteredIssues}
          sortable
          showCheckbox
          selectedIds={selectedIds}
          onSelectionChange={toggleSelection}
          onRowClick={handleIssueClick}
          searchTerm={debouncedSearch}
          {...(selectedIssueId != null && {
            selectedId: selectedIssueId,
          })}
          {...(blockedIssuesMap !== undefined && {
            blockedIssues: blockedIssuesMap,
          })}
          {...(filters.showBlocked !== undefined && {
            showBlocked: filters.showBlocked,
          })}
        />
        <BulkActionToolbar
          selectedIds={selectedIds}
          onClearSelection={clearSelection}
        />
      </IssueViewGuard>
    </ErrorBoundary>
  );
}
