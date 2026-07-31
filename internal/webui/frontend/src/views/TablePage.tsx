import { useCallback, useMemo, useState } from "react";

import {
  ErrorBoundary,
  IssueTable,
  BulkActionToolbar,
  ConfirmDialog,
} from "@/components";
import { IssueViewGuard } from "@/components/IssueViewGuard";
import { updateIssue } from "@/api";
import { useBulkClose, useSelection } from "@/hooks";
import {
  useWorkspaceViewData,
  useWorkspaceViewActions,
} from "@/contexts/WorkspaceViewContext";
import type { BulkAction, Priority, Status } from "@/types";

import styles from "./TablePage.module.css";

type BulkDialog = "close" | "status" | "priority" | "assign";

const BULK_STATUS_OPTIONS: { value: Status; label: string }[] = [
  { value: "open", label: "Open" },
  { value: "in_progress", label: "In Progress" },
  { value: "blocked", label: "Blocked" },
  { value: "deferred", label: "Backlog" },
  { value: "review", label: "Review" },
];

const BULK_PRIORITY_OPTIONS: { value: Priority; label: string }[] = [
  { value: 0, label: "P0 - Critical" },
  { value: 1, label: "P1 - High" },
  { value: 2, label: "P2 - Medium" },
  { value: 3, label: "P3 - Normal" },
  { value: 4, label: "P4 - Backlog" },
];

function issueCount(count: number): string {
  return `${count} issue${count === 1 ? "" : "s"}`;
}

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
    workspaceId,
  } = useWorkspaceViewData();

  const { handleIssueClick, refetch, showToast } = useWorkspaceViewActions();

  const {
    selectedIds,
    toggleSelection,
    deselectAll: clearSelection,
  } = useSelection({ visibleItems: filteredIssues });

  const [pendingDialog, setPendingDialog] = useState<BulkDialog | null>(null);
  const [bulkStatus, setBulkStatus] = useState<Status>("open");
  const [bulkPriority, setBulkPriority] = useState<Priority>(2);
  const [bulkAssignee, setBulkAssignee] = useState("");
  const [isBulkUpdating, setIsBulkUpdating] = useState(false);

  const selectedCount = selectedIds.size;

  const { bulkClose, isLoading: isClosing } = useBulkClose({
    closeReason: "Closed from table bulk action",
    onSuccess: (closedIds) => {
      clearSelection();
      showToast(`Closed ${issueCount(closedIds.length)}`, { type: "success" });
    },
    onPartialSuccess: (closedIds, failedIds) => {
      showToast(
        `Closed ${closedIds.length} of ${closedIds.length + failedIds.length} issues; ${failedIds.length} failed`,
        { type: "warning" },
      );
    },
    onError: (_error, failedIds) => {
      showToast(`Failed to close ${issueCount(failedIds.length)}`, {
        type: "error",
      });
    },
  });

  const runBulkUpdate = useCallback(
    async (
      payload: Parameters<typeof updateIssue>[2],
      successLabel: string,
      failureLabel: string,
    ) => {
      const ids = Array.from(selectedIds);
      if (ids.length === 0 || isBulkUpdating) return;

      setIsBulkUpdating(true);
      try {
        const results = await Promise.allSettled(
          ids.map((id) => updateIssue(workspaceId, id, payload)),
        );
        const successCount = results.filter(
          (result) => result.status === "fulfilled",
        ).length;
        const failedCount = ids.length - successCount;

        if (failedCount === 0) {
          clearSelection();
          showToast(`${successLabel} ${issueCount(successCount)}`, {
            type: "success",
          });
        } else if (successCount > 0) {
          showToast(
            `${successLabel} ${successCount} of ${ids.length} issues; ${failedCount} failed`,
            { type: "warning" },
          );
        } else {
          showToast(`${failureLabel} ${issueCount(failedCount)}`, {
            type: "error",
          });
        }
      } finally {
        setIsBulkUpdating(false);
        setPendingDialog(null);
      }
    },
    [clearSelection, isBulkUpdating, selectedIds, showToast, workspaceId],
  );

  const handleConfirmClose = useCallback(() => {
    const ids = Array.from(selectedIds);
    setPendingDialog(null);
    void bulkClose(ids);
  }, [bulkClose, selectedIds]);

  const handleConfirmStatus = useCallback(() => {
    void runBulkUpdate(
      { status: bulkStatus },
      "Updated status for",
      "Failed to update status for",
    );
  }, [bulkStatus, runBulkUpdate]);

  const handleConfirmPriority = useCallback(() => {
    void runBulkUpdate(
      { priority: bulkPriority },
      "Updated priority for",
      "Failed to update priority for",
    );
  }, [bulkPriority, runBulkUpdate]);

  const handleConfirmAssign = useCallback(() => {
    void runBulkUpdate(
      { assignee: bulkAssignee.trim() },
      "Updated assignee for",
      "Failed to update assignee for",
    );
  }, [bulkAssignee, runBulkUpdate]);

  const bulkActions = useMemo<BulkAction[]>(
    () => [
      {
        id: "close",
        label: "Close",
        variant: "danger",
        loading: isClosing,
        disabled: isClosing || isBulkUpdating,
        onClick: () => setPendingDialog("close"),
      },
      {
        id: "status",
        label: "Change status",
        variant: "secondary",
        disabled: isClosing || isBulkUpdating,
        onClick: () => setPendingDialog("status"),
      },
      {
        id: "priority",
        label: "Change priority",
        variant: "secondary",
        disabled: isClosing || isBulkUpdating,
        onClick: () => setPendingDialog("priority"),
      },
      {
        id: "assign",
        label: "Assign",
        variant: "secondary",
        disabled: isClosing || isBulkUpdating,
        onClick: () => setPendingDialog("assign"),
      },
    ],
    [isBulkUpdating, isClosing],
  );

  const closeDialogTitle = `Close ${issueCount(selectedCount)}?`;

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
        <div className={styles.tablePage}>
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
            actions={bulkActions}
          />
        </div>
        <ConfirmDialog
          isOpen={pendingDialog === "close"}
          title={closeDialogTitle}
          message={`This will close ${issueCount(selectedCount)}.`}
          confirmLabel="Close"
          variant="danger"
          onConfirm={handleConfirmClose}
          onCancel={() => setPendingDialog(null)}
        />
        <ConfirmDialog
          isOpen={pendingDialog === "status"}
          title={`Change status for ${issueCount(selectedCount)}`}
          message={
            <label className={styles.bulkDialogField} htmlFor="bulk-status">
              Status
              <select
                id="bulk-status"
                className={styles.bulkDialogSelect}
                value={bulkStatus}
                onChange={(event) =>
                  setBulkStatus(event.target.value as Status)
                }
                disabled={isBulkUpdating}
              >
                {BULK_STATUS_OPTIONS.map((option) => (
                  <option key={option.value} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </select>
            </label>
          }
          confirmLabel="Apply"
          onConfirm={handleConfirmStatus}
          onCancel={() => setPendingDialog(null)}
        />
        <ConfirmDialog
          isOpen={pendingDialog === "priority"}
          title={`Change priority for ${issueCount(selectedCount)}`}
          message={
            <label className={styles.bulkDialogField} htmlFor="bulk-priority">
              Priority
              <select
                id="bulk-priority"
                className={styles.bulkDialogSelect}
                value={bulkPriority}
                onChange={(event) =>
                  setBulkPriority(Number(event.target.value) as Priority)
                }
                disabled={isBulkUpdating}
              >
                {BULK_PRIORITY_OPTIONS.map((option) => (
                  <option key={option.value} value={option.value}>
                    {option.label}
                  </option>
                ))}
              </select>
            </label>
          }
          confirmLabel="Apply"
          onConfirm={handleConfirmPriority}
          onCancel={() => setPendingDialog(null)}
        />
        <ConfirmDialog
          isOpen={pendingDialog === "assign"}
          title={`Assign ${issueCount(selectedCount)}`}
          message={
            <label className={styles.bulkDialogField} htmlFor="bulk-assignee">
              Assignee
              <input
                id="bulk-assignee"
                className={styles.bulkDialogInput}
                value={bulkAssignee}
                onChange={(event) => setBulkAssignee(event.target.value)}
                placeholder="Agent or person name"
                disabled={isBulkUpdating}
              />
              <span className={styles.bulkDialogHint}>
                Leave empty to clear the assignee.
              </span>
            </label>
          }
          confirmLabel="Apply"
          onConfirm={handleConfirmAssign}
          onCancel={() => setPendingDialog(null)}
        />
      </IssueViewGuard>
    </ErrorBoundary>
  );
}
