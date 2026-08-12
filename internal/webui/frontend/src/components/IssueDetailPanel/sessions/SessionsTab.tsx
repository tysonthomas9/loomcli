/**
 * SessionsTab - Container component for the Runs tab in IssueDetailPanel.
 * Uses useTaskSessions to fetch session data, manages selected session state,
 * and renders SessionTimeline + SessionDetailView in a two-column layout.
 */

import { useMemo, useState } from "react";

import { useTaskSessions } from "@/hooks/terminal";

import type { SessionRecord } from "@/types/agent";
import { sessionTotalTokens } from "@/utils/sessionUsage";

import { SessionTimeline, type RunRailSummary } from "./SessionTimeline";
import { SessionDetailView } from "./SessionDetailView";
import styles from "./SessionsTab.module.css";

export interface SessionsTabProps {
  taskId: string;
}

export function SessionsTab({ taskId }: SessionsTabProps): JSX.Element {
  const { sessions, isLoading, error } = useTaskSessions(taskId);
  const [selectedSessionId, setSelectedSessionId] = useState<string | null>(
    null,
  );

  const selectedSession =
    selectedSessionId != null
      ? (sessions.find((s) => s.session_id === selectedSessionId) ?? null)
      : null;

  const summary = useMemo(() => computeCostSummary(sessions), [sessions]);

  // Loading state with no data yet
  if (isLoading && sessions.length === 0) {
    return (
      <div className={styles.loadingContainer}>
        <div className={styles.spinner} />
      </div>
    );
  }

  // Error state
  if (error && sessions.length === 0) {
    return (
      <div className={styles.emptyState}>
        Failed to load runs: {error.message}
      </div>
    );
  }

  // Empty state
  if (!isLoading && sessions.length === 0) {
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
          selectedId={selectedSessionId}
          onSelect={setSelectedSessionId}
          isLoading={isLoading}
          summary={summary}
        />
        {selectedSession ? (
          <SessionDetailView taskId={taskId} session={selectedSession} />
        ) : (
          <div className={styles.detailEmpty}>Select a run to view details</div>
        )}
      </div>
    </div>
  );
}

function computeCostSummary(sessions: SessionRecord[]): RunRailSummary {
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
  return {
    count: sessions.length,
    totalTokens,
    totalCost,
    activeSessions,
    failedSessions,
  };
}
