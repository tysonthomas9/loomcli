/**
 * SessionsTab - Container component for the Runs tab in IssueDetailPanel.
 * Uses useTaskSessions to fetch session data, manages selected session state,
 * and renders SessionTimeline + SessionDetailView in a two-column layout.
 */

import { useMemo, useState } from "react";

import { useTaskSessions, useTaskWorkflowRuns } from "@/hooks/terminal";

import type { SessionRecord } from "@/types/agent";

import { SessionTimeline } from "./SessionTimeline";
import { SessionDetailView } from "./SessionDetailView";
import { WorkflowRunDetail } from "./WorkflowRunDetail";
import styles from "@/styles/SessionRunDetail.module.css";

export interface SessionsTabProps {
  taskId: string;
}

export function SessionsTab({ taskId }: SessionsTabProps): JSX.Element {
  const { sessions, isLoading, error } = useTaskSessions(taskId);
  const {
    runs: workflowRuns,
    isLoading: workflowRunsLoading,
    error: workflowRunsError,
  } = useTaskWorkflowRuns(taskId);
  const [selectedSessionId, setSelectedSessionId] = useState<string | null>(
    null,
  );
  const [selectedWorkflowRunId, setSelectedWorkflowRunId] = useState<
    string | null
  >(null);

  const selectedSession =
    selectedSessionId != null
      ? (sessions.find((s) => s.session_id === selectedSessionId) ?? null)
      : null;
  const selectedWorkflowRun =
    selectedWorkflowRunId != null
      ? (workflowRuns.find((run) => run.run_id === selectedWorkflowRunId) ??
        null)
      : null;

  const summary = useMemo(
    () => computeCostSummary(sessions, workflowRuns),
    [sessions, workflowRuns],
  );
  const hasRuns = sessions.length > 0 || workflowRuns.length > 0;
  const anyLoading = isLoading || workflowRunsLoading;
  const loadError = error ?? workflowRunsError;

  // Loading state with no data yet
  if (anyLoading && !hasRuns) {
    return (
      <div className={styles.loadingContainer}>
        <div className={styles.spinner} />
      </div>
    );
  }

  // Error state
  if (loadError && !hasRuns) {
    return (
      <div className={styles.emptyState}>
        Failed to load runs: {loadError.message}
      </div>
    );
  }

  // Empty state
  if (!anyLoading && !hasRuns) {
    return (
      <div className={styles.emptyState} data-testid="sessions-empty">
        No agent runs recorded yet
      </div>
    );
  }

  return (
    <div className={styles.outerContainer} data-testid="sessions-tab">
      <div className={styles.costSummary}>
        <span className={styles.summaryItem}>
          <span className={styles.summaryLabel}>Runs</span>
          <span className={styles.summaryValue}>{summary.count}</span>
        </span>
        <span className={styles.summaryItem}>
          <span className={styles.summaryLabel}>Tokens</span>
          <span className={styles.summaryValue}>
            {formatTokensShort(summary.totalTokens)}
          </span>
        </span>
        <span className={styles.summaryItem}>
          <span className={styles.summaryLabel}>Cost</span>
          <span className={styles.summaryValue}>
            {formatCostUSD(summary.totalCost)}
          </span>
        </span>
        {summary.activeSessions > 0 && (
          <span className={styles.summaryItem}>
            <span className={styles.activeBadge}>
              {summary.activeSessions} active
            </span>
          </span>
        )}
        {summary.failedSessions > 0 && (
          <span className={styles.summaryItem}>
            <span className={styles.failedBadge}>
              {summary.failedSessions} failed
            </span>
          </span>
        )}
      </div>
      <div className={styles.container}>
        <SessionTimeline
          sessions={sessions}
          selectedId={selectedSessionId}
          onSelect={(id) => {
            setSelectedSessionId(id);
            setSelectedWorkflowRunId(null);
          }}
          isLoading={anyLoading}
          workflowRuns={workflowRuns}
          selectedWorkflowRunId={selectedWorkflowRunId}
          onSelectWorkflowRun={(id) => {
            setSelectedWorkflowRunId(id);
            setSelectedSessionId(null);
          }}
        />
        {selectedSession ? (
          <SessionDetailView taskId={taskId} session={selectedSession} />
        ) : selectedWorkflowRun ? (
          <WorkflowRunDetail run={selectedWorkflowRun} />
        ) : (
          <div className={styles.detailEmpty}>Select a run to view details</div>
        )}
      </div>
    </div>
  );
}

interface CostSummary {
  count: number;
  totalTokens: number;
  totalCost: number;
  activeSessions: number;
  failedSessions: number;
}

function computeCostSummary(
  sessions: SessionRecord[],
  workflowRuns: Array<{ status: string }>,
): CostSummary {
  let totalTokens = 0;
  let totalCost = 0;
  let activeSessions = 0;
  let failedSessions = 0;
  for (const s of sessions) {
    totalTokens += s.input_tokens + s.output_tokens;
    totalCost += s.estimated_cost_usd;
    if (s.is_active) activeSessions++;
    if (s.status === "failed") failedSessions++;
  }
  for (const run of workflowRuns) {
    if (
      run.status === "queued" ||
      run.status === "running" ||
      run.status === "suspended_awaiting_event"
    ) {
      activeSessions++;
    }
    if (run.status === "failed") failedSessions++;
  }
  return {
    count: sessions.length + workflowRuns.length,
    totalTokens,
    totalCost,
    activeSessions,
    failedSessions,
  };
}

function formatTokensShort(count: number): string {
  if (count >= 1_000_000) return `${(count / 1_000_000).toFixed(1)}M`;
  if (count >= 1_000) return `${(count / 1_000).toFixed(1)}K`;
  return String(count);
}

function formatCostUSD(usd: number): string {
  if (usd === 0) return "$0.00";
  if (usd < 0.01) return "<$0.01";
  return `$${usd.toFixed(2)}`;
}
