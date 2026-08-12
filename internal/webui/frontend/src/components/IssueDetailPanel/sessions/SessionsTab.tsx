/**
 * SessionsTab - Container component for the Runs tab in IssueDetailPanel.
 * Uses useTaskSessions to fetch session data, manages selected session state,
 * and renders SessionTimeline + SessionDetailView in a two-column layout.
 */

import { useMemo, useState } from "react";

import { useTaskSessions, useTaskWorkflowRuns } from "@/hooks/terminal";

import type { SessionRecord } from "@/types/agent";
import { sessionTotalTokens } from "@/utils/sessionUsage";

import { SessionTimeline, type RunRailSummary } from "./SessionTimeline";
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
      <div className={styles.container}>
        <SessionTimeline
          sessions={sessions}
          summary={summary}
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

function computeCostSummary(
  sessions: SessionRecord[],
  workflowRuns: Array<{ status: string }>,
): RunRailSummary {
  let totalTokens = 0;
  let totalCost = 0;
  let activeSessions = 0;
  let failedSessions = 0;
  for (const s of sessions) {
    totalTokens += sessionTotalTokens(s);
    if (s.estimated_cost_usd > 0) {
      totalCost += s.estimated_cost_usd;
    }
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
