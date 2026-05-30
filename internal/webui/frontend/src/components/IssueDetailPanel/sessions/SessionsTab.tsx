/**
 * SessionsTab - Container component for the Runs tab in IssueDetailPanel.
 * Uses useTaskSessions to fetch session data, manages selected session state,
 * and renders SessionTimeline + SessionDetailView in a two-column layout.
 */

import { useMemo, useState } from "react";

import { useTaskSessions } from "@/hooks/terminal";

import type { SessionRecord } from "@/types/agent";

import { SessionTimeline } from "./SessionTimeline";
import { SessionDetailView } from "./SessionDetailView";
import { WorkflowRunHistory } from "./WorkflowRunHistory";
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
      <div className={styles.outerContainer}>
        <WorkflowRunHistory taskId={taskId} />
        <div className={styles.emptyState}>
          Failed to load runs: {error.message}
        </div>
      </div>
    );
  }

  // Empty state
  if (!isLoading && sessions.length === 0) {
    return (
      <div className={styles.outerContainer}>
        <WorkflowRunHistory taskId={taskId} />
        <div className={styles.emptyState} data-testid="sessions-empty">
          No agent runs recorded yet
        </div>
      </div>
    );
  }

  return (
    <div className={styles.outerContainer} data-testid="sessions-tab">
      <WorkflowRunHistory taskId={taskId} />
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
          onSelect={setSelectedSessionId}
          isLoading={isLoading}
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

interface CostSummary {
  count: number;
  totalTokens: number;
  totalCost: number;
  activeSessions: number;
  failedSessions: number;
}

function computeCostSummary(sessions: SessionRecord[]): CostSummary {
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
  return {
    count: sessions.length,
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
