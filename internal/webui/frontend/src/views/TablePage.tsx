import { useCallback, useMemo, useState } from "react";

import {
  ErrorBoundary,
  ErrorToast,
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
  const [bulkError, setBulkError] = useState<string | null>(null);
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
    setBulkError(null);
    clearSelection();
    void refetch();
  }, [clearSelection, refetch]);

  const { bulkClose, isLoading: isClosing } = useBulkClose({
    onSuccess: () => {
      setShowCloseDialog(false);
      finishBulkMutation();
    },
    onPartialSuccess: (closedIds, failedIds) => {
      setBulkError(
        `Closed ${closedIds.length} of ${closedIds.length + failedIds.length} issues; ${failedIds.length} failed`,
      );
      void refetch();
    },
    onError: (error) => setBulkError(error.message),
  });

  const actions = useMemo(
    () => [
      {
        id: "status",
        label: "Change status",
        loading: isUpdatingStatus,
        disabled: isUpdatingStatus || isClosing,
        onClick: () => {
          setBulkError(null);
          setShowStatusDialog(true);
        },
      },
      {
        id: "close",
        label: "Close",
        variant: "danger" as const,
        loading: isClosing,
        disabled: isClosing || isUpdatingStatus,
        onClick: () => {
          setBulkError(null);
          setShowCloseDialog(true);
        },
      },
    ],
    [isClosing, isUpdatingStatus],
  );

  const handleStatusConfirm = useCallback(async () => {
    setIsUpdatingStatus(true);
    try {
      const results = await Promise.allSettled(
        Array.from(selectedIds, (id) =>
          updateIssue(workspaceId, id, { status: bulkStatus }),
        ),
      );
      const failedCount = results.filter(
        (result) => result.status === "rejected",
      ).length;
      const updatedCount = results.length - failedCount;
      if (failedCount === 0) {
        setShowStatusDialog(false);
        finishBulkMutation();
      } else {
        setBulkError(
          `Updated ${updatedCount} of ${results.length} issues; ${failedCount} failed`,
        );
        if (updatedCount > 0) void refetch();
      }
    } finally {
      setIsUpdatingStatus(false);
    }
  }, [bulkStatus, finishBulkMutation, refetch, selectedIds, workspaceId]);

  const handleCloseConfirm = useCallback(async () => {
    await bulkClose(selectedIds);
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
          onCancel={() => {
            setShowStatusDialog(false);
            setBulkError(null);
          }}
        />
        <ConfirmDialog
          isOpen={showCloseDialog}
          title="Close selected issues"
          message={`Close ${selectedIds.size} selected issue${selectedIds.size === 1 ? "" : "s"}?`}
          confirmLabel="Close issues"
          variant="danger"
          onConfirm={() => void handleCloseConfirm()}
          onCancel={() => {
            setShowCloseDialog(false);
            setBulkError(null);
          }}
        />
        {bulkError && (
          <ErrorToast
            message={bulkError}
            onDismiss={() => setBulkError(null)}
            duration={0}
          />
        )}
      </IssueViewGuard>
    </ErrorBoundary>
  );
}
