import { useState, useCallback, useEffect, useRef } from "react";
import { useStore } from "zustand";

import { ErrorBoundary, SwimLaneBoard, AssigneePrompt } from "@/components";
import { IssueViewGuard } from "@/components/IssueViewGuard";
import type { Status } from "@/types";
import { updateIssue } from "@/api";
import { useRecentAssignees } from "@/hooks";
import { useIssueStoreInstance } from "@/hooks/common";
import { useRunEpicWorkflow } from "@/hooks/workspace";
import {
  useWorkspaceViewData,
  useWorkspaceViewActions,
} from "@/contexts/WorkspaceViewContext";

import styles from "./KanbanPage.module.css";

export function KanbanPage() {
  const {
    filteredIssues,
    hasActiveFilters,
    issues,
    isLoading,
    error,
    retryCount,
    nextRetryAt,
    isMultiRepo,
    activeView,
    blockedIssuesMap,
    filters,
    groupBy,
    pendingIds,
    workspaceId,
  } = useWorkspaceViewData();

  const { handleIssueClick, updateIssueStatus, refetch, showToast } =
    useWorkspaceViewActions();
  const issueStore = useIssueStoreInstance();
  const reconcileIssue = useStore(issueStore, (s) => s.reconcileIssue);
  const { runEpic, isRunningEpic } = useRunEpicWorkflow({ showToast });

  // Assignee prompt state for Ready -> In Progress drag
  const { recentAssignees, addRecentAssignee } = useRecentAssignees();
  const [pendingDragData, setPendingDragData] = useState<{
    issueId: string;
    newStatus: Status;
    oldStatus: Status;
  } | null>(null);

  const mountedRef = useRef(true);
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const handleDragEnd = useCallback(
    async (issueId: string, newStatus: Status, oldStatus: Status) => {
      if (oldStatus === "open" && newStatus === "in_progress") {
        setPendingDragData({ issueId, newStatus, oldStatus });
        return;
      }
      try {
        await updateIssueStatus(issueId, newStatus);
      } catch {
        // Rollback + error toast handled by useOptimisticUpdate
      }
    },
    [updateIssueStatus],
  );

  const handleAssigneeConfirm = useCallback(
    async (assignee: string) => {
      if (!pendingDragData) return;

      const { issueId, newStatus } = pendingDragData;
      setPendingDragData(null);

      const nameWithoutPrefix = assignee.replace(/^\[H\]\s*/, "");
      addRecentAssignee(nameWithoutPrefix);

      try {
        const updatedIssue = await updateIssue(workspaceId, issueId, {
          status: newStatus,
          assignee,
        });
        reconcileIssue(updatedIssue);
      } catch (err) {
        if (!mountedRef.current) return;
        const message =
          err instanceof Error ? err.message : "Failed to update status";
        showToast(message, { type: "error" });
      }
    },
    [
      pendingDragData,
      addRecentAssignee,
      workspaceId,
      showToast,
      reconcileIssue,
    ],
  );

  const handleAssigneeSkip = useCallback(async () => {
    if (!pendingDragData) return;

    const { issueId, newStatus } = pendingDragData;
    setPendingDragData(null);

    try {
      await updateIssueStatus(issueId, newStatus);
    } catch {
      // Rollback + error toast handled by useOptimisticUpdate
    }
  }, [pendingDragData, updateIssueStatus]);

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
        loadingVariant="columns"
      >
        <div className={styles.kanbanShell}>
          <SwimLaneBoard
            issues={filteredIssues}
            filters={filters}
            groupBy={groupBy}
            onDragEnd={handleDragEnd}
            onIssueClick={handleIssueClick}
            onRunEpic={runEpic}
            isRunningEpic={isRunningEpic}
            isMultiRepo={isMultiRepo}
            hasFiltersActive={hasActiveFilters}
            cardLimit={15}
            {...(blockedIssuesMap !== undefined && {
              blockedIssues: blockedIssuesMap,
            })}
            {...(filters.showBlocked !== undefined && {
              showBlocked: filters.showBlocked,
            })}
            {...(pendingIds !== undefined &&
              pendingIds.size > 0 && { pendingIds })}
          />
        </div>
      </IssueViewGuard>
      <AssigneePrompt
        isOpen={pendingDragData !== null}
        onConfirm={handleAssigneeConfirm}
        onSkip={handleAssigneeSkip}
        recentNames={recentAssignees}
      />
    </ErrorBoundary>
  );
}
