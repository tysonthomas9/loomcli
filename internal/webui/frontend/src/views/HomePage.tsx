import { useCallback, useEffect, useRef } from "react";

import { updateIssue } from "@/api";
import { EmptyState, ErrorBoundary, OperatorQueueCard } from "@/components";
import { IssueViewGuard } from "@/components/IssueViewGuard";
import {
  useWorkspaceViewActions,
  useWorkspaceViewData,
} from "@/contexts/WorkspaceViewContext";
import { useElapsedTime } from "@/hooks/common";
import { useOperatorQueue } from "@/hooks/issues";
import type { Issue } from "@/types";
import { NEEDS_REVISION_LABEL } from "@/utils/issue";

import styles from "./HomePage.module.css";

export function HomePage(): JSX.Element {
  const {
    issues,
    isLoading,
    error,
    retryCount,
    nextRetryAt,
    isMultiRepo,
    activeView,
    workspaceId,
    agents,
  } = useWorkspaceViewData();
  const { refetch, handleIssueClick, showToast, updateIssueStatus } =
    useWorkspaceViewActions();
  const queue = useOperatorQueue(issues);
  const oldestWaitingSince = queue[0]?.waitingSince;
  const oldestAge = useElapsedTime(
    oldestWaitingSince !== undefined && Number.isFinite(oldestWaitingSince)
      ? oldestWaitingSince
      : null,
  );
  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  const handleApproveAndRoute = useCallback(
    async (issue: Issue, assignee?: string): Promise<void> => {
      try {
        await updateIssue(workspaceId, issue.id, {
          status: "open",
          remove_labels: [NEEDS_REVISION_LABEL],
          ...(assignee ? { assignee } : {}),
        });
        await refetch();
        if (!mountedRef.current) return;
        showToast(
          assignee
            ? `Approved ${issue.id} and routed to ${assignee}`
            : `Approved ${issue.id} without an assignee`,
          { type: "success" },
        );
      } catch (err) {
        if (!mountedRef.current) return;
        const message =
          err instanceof Error ? err.message : "Failed to approve and route";
        showToast(message, { type: "error" });
      }
    },
    [workspaceId, refetch, showToast],
  );

  const handleUnblock = useCallback(
    async (issue: Issue): Promise<void> => {
      try {
        await updateIssueStatus(issue.id, "in_progress");
        if (!mountedRef.current) return;
        showToast(`Unblocked ${issue.id}`, { type: "success" });
      } catch {
        // Rollback and error toast are handled by the optimistic store action.
      }
    },
    [updateIssueStatus, showToast],
  );

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
        showEmptyState={false}
      >
        <div className={styles.page} data-testid="home-page">
          <div className={styles.layout}>
            <section className={styles.queueColumn}>
              <header className={styles.header}>
                <h2>Needs you</h2>
                <span className={styles.count}>{queue.length}</span>
                <span className={styles.summary}>
                  {queue.length > 0
                    ? `Oldest waiting ~${oldestAge || "unknown"} · measured from last update`
                    : "No design gates, blocked declarations, or revision bounces"}
                </span>
              </header>

              {queue.length > 0 ? (
                <div className={styles.queue} data-testid="operator-queue">
                  {queue.map((item) => (
                    <OperatorQueueCard
                      item={item}
                      agents={agents}
                      onApprove={handleApproveAndRoute}
                      onUnblock={handleUnblock}
                      onOpenIssue={handleIssueClick}
                      key={item.issue.id}
                    />
                  ))}
                </div>
              ) : (
                <div data-testid="queue-empty">
                  <EmptyState
                    variant="queue-clear"
                    className={styles.queueEmpty ?? ""}
                  />
                </div>
              )}
            </section>
          </div>
        </div>
      </IssueViewGuard>
    </ErrorBoundary>
  );
}
