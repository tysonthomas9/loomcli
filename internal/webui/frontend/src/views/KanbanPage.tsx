import { useState, useCallback, useEffect, useRef } from "react";

import { ErrorBoundary } from "@/components/ErrorBoundary/ErrorBoundary";
import { SwimLaneBoard } from "@/components/SwimLaneBoard/SwimLaneBoard";
import { AssigneePrompt } from "@/components/AssigneePrompt/AssigneePrompt";
import { IssueViewGuard } from "@/components/IssueViewGuard/IssueViewGuard";
import type { Status } from "@/types";
import { updateIssue } from "@/api";
import { useRecentAssignees } from "@/hooks/issues/useRecentAssignees";
import {
  useWorkspaceViewData,
  useWorkspaceViewActions,
} from "@/contexts/WorkspaceViewContext";

import styles from "./KanbanPage.module.css";

export function KanbanPage() {
  const {
    filteredIssues,
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
        await updateIssue(workspaceId, issueId, {
          status: newStatus,
          assignee,
        });
      } catch (err) {
        if (!mountedRef.current) return;
        const message =
          err instanceof Error ? err.message : "Failed to update status";
        showToast(message, { type: "error" });
      }
    },
    [pendingDragData, addRecentAssignee, workspaceId, showToast],
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
            groupBy={groupBy}
            onDragEnd={handleDragEnd}
            onIssueClick={handleIssueClick}
            isMultiRepo={isMultiRepo}
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
