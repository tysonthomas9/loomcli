import { useCallback, useMemo, useState } from "react";

import {
  ErrorBoundary,
  IssueTable,
  BulkActionToolbar,
  ConfirmDialog,
} from "@/components";
import { IssueViewGuard } from "@/components/IssueViewGuard";
import {
  updateIssue,
  useBulkClose,
  useSelection,
  useWorkspaceContext,
} from "@/hooks";
import type { Status } from "@/types/issue";
import {
  useWorkspaceViewData,
  useWorkspaceViewActions,
} from "@/contexts/WorkspaceViewContext";

export function TablePage() {
  const { workspaceId } = useWorkspaceContext();
  const [showStatusDialog, setShowStatusDialog] = useState(false);
  const [showCloseDialog, setShowCloseDialog] = useState(false);
  const [bulkStatus, setBulkStatus] = useState<Status>("in_progress");
  const [isUpdatingStatus, setIsUpdatingStatus] = useState(false);
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

  const finishBulkMutation = useCallback(() => {
    clearSelection();
    void refetch();
  }, [clearSelection, refetch]);

  const { bulkClose, isLoading: isClosing } = useBulkClose({
    onSuccess: finishBulkMutation,
    onPartialSuccess: () => void refetch(),
  });

  const actions = useMemo(
    () => [
      {
        id: "status",
        label: "Change status",
        loading: isUpdatingStatus,
        disabled: isUpdatingStatus || isClosing,
        onClick: () => setShowStatusDialog(true),
      },
      {
        id: "close",
        label: "Close",
        variant: "danger" as const,
        loading: isClosing,
        disabled: isClosing || isUpdatingStatus,
        onClick: () => setShowCloseDialog(true),
      },
    ],
    [isClosing, isUpdatingStatus],
  );

  const handleStatusConfirm = useCallback(async () => {
    setIsUpdatingStatus(true);
    try {
      await Promise.all(
        Array.from(selectedIds, (id) =>
          updateIssue(workspaceId, id, { status: bulkStatus }),
        ),
      );
      setShowStatusDialog(false);
      finishBulkMutation();
    } finally {
      setIsUpdatingStatus(false);
    }
  }, [bulkStatus, finishBulkMutation, selectedIds, workspaceId]);

  const handleCloseConfirm = useCallback(async () => {
    await bulkClose(selectedIds);
    setShowCloseDialog(false);
  }, [bulkClose, selectedIds]);

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
          groupByEpic
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
          actions={actions}
        />
        <ConfirmDialog
          isOpen={showStatusDialog}
          title="Change status"
          message={
            <label htmlFor="bulk-status">
              New status
              <select
                id="bulk-status"
                value={bulkStatus}
                onChange={(event) =>
                  setBulkStatus(event.target.value as Status)
                }
              >
                <option value="open">Ready</option>
                <option value="in_progress">In progress</option>
                <option value="blocked">Blocked</option>
                <option value="review">Review</option>
                <option value="deferred">Deferred</option>
              </select>
            </label>
          }
          confirmLabel="Update issues"
          onConfirm={() => void handleStatusConfirm()}
          onCancel={() => setShowStatusDialog(false)}
        />
        <ConfirmDialog
          isOpen={showCloseDialog}
          title="Close selected issues"
          message={`Close ${selectedIds.size} selected issue${selectedIds.size === 1 ? "" : "s"}?`}
          confirmLabel="Close issues"
          variant="danger"
          onConfirm={() => void handleCloseConfirm()}
          onCancel={() => setShowCloseDialog(false)}
        />
      </IssueViewGuard>
    </ErrorBoundary>
  );
}
