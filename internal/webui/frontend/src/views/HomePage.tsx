import { useCallback, useEffect, useRef, useState } from "react";

import { updateIssue } from "@/api";
import {
  EmptyState,
  ErrorBoundary,
  HomeRail,
  OperatorQueueCard,
  workspaceCountsFromStats,
} from "@/components";
import { IssueViewGuard } from "@/components/IssueViewGuard";
import {
  useWorkspaceViewActions,
  useWorkspaceViewData,
} from "@/contexts/WorkspaceViewContext";
import { useOperatorQueue } from "@/hooks/issues";
import { useRecentActivity, useWorkspaceStats } from "@/hooks/workspace";
import { isAgentActive } from "@/types";
import type { Issue } from "@/types";
import { NEEDS_REVISION_LABEL } from "@/utils/issue";
import { plural } from "@/utils/plural";

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
  const { refetch, handleIssueClick, handleReject, showToast } =
    useWorkspaceViewActions();
  const queue = useOperatorQueue(issues);
  const [optimisticallyResolvedIds, setOptimisticallyResolvedIds] = useState<
    ReadonlySet<string>
  >(new Set());
  const visibleQueue = queue.filter(
    (item) => !optimisticallyResolvedIds.has(item.issue.id),
  );
  const activity = useRecentActivity(workspaceId, issues, agents);
  // Workspace-wide, from GET /stats — deliberately not derived from `issues`,
  // which is whatever the board view has fetched or filtered.
  const { stats } = useWorkspaceStats(workspaceId);
  const workspaceCounts = workspaceCountsFromStats(stats);
  const idleAgents = agents.filter((agent) => !isAgentActive(agent)).length;
  const mountedRef = useRef(true);

  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);

  useEffect(() => {
    setOptimisticallyResolvedIds((current) => {
      const stillQueued = new Set(queue.map((item) => item.issue.id));
      const remaining = [...current].filter((id) => stillQueued.has(id));
      return remaining.length === current.size ? current : new Set(remaining);
    });
  }, [queue]);

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
      setOptimisticallyResolvedIds((current) => new Set(current).add(issue.id));
      try {
        await updateIssue(workspaceId, issue.id, {
          status: "open",
          ...(issue.assignee ? { assignee: issue.assignee } : {}),
        });
        await refetch();
        if (!mountedRef.current) return;
        showToast(`Unblocked ${issue.id}`, { type: "success" });
      } catch (err) {
        setOptimisticallyResolvedIds((current) => {
          const restored = new Set(current);
          restored.delete(issue.id);
          return restored;
        });
        if (!mountedRef.current) return;
        const message =
          err instanceof Error ? err.message : "Failed to unblock issue";
        showToast(message, { type: "error" });
      }
    },
    [workspaceId, refetch, showToast],
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
                <span className={styles.count}>{visibleQueue.length}</span>
              </header>

              {visibleQueue.length > 0 ? (
                <div className={styles.queue} data-testid="operator-queue">
                  {visibleQueue.map((item) => (
                    <OperatorQueueCard
                      item={item}
                      agents={agents}
                      onApprove={handleApproveAndRoute}
                      onReject={handleReject}
                      onUnblock={handleUnblock}
                      onOpenIssue={handleIssueClick}
                      key={item.issue.id}
                    />
                  ))}
                </div>
              ) : (
                <div className={styles.queueEmpty} data-testid="queue-empty">
                  <EmptyState variant="queue-clear" />
                  <div className={styles.queueStats}>
                    <span data-stat="closed" data-testid="queue-stat">
                      <strong>
                        {workspaceCounts?.closed ?? 0}{" "}
                        {plural(
                          workspaceCounts?.closed ?? 0,
                          "issue",
                          "issues",
                        )}
                      </strong>
                      closed
                    </span>
                    <span data-stat="idle" data-testid="queue-stat">
                      <strong>
                        {idleAgents} {plural(idleAgents, "agent", "agents")}
                      </strong>
                      idle
                    </span>
                  </div>
                </div>
              )}
            </section>
            <HomeRail
              activity={activity}
              counts={workspaceCounts}
              workspaceId={workspaceId}
            />
          </div>
        </div>
      </IssueViewGuard>
    </ErrorBoundary>
  );
}
