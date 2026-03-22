/**
 * SessionsTab - Container component for the Sessions tab in IssueDetailPanel.
 * Uses useTaskSessions to fetch session data, manages selected session state,
 * and renders SessionTimeline + SessionDetailView in a two-column layout.
 */

import { useMemo, useState } from "react";

import { useTaskSessions } from "@/hooks/useTaskSessions";
import type { SessionRecord } from "@/types/session";

import { SessionTimeline } from "./SessionTimeline";
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
      ? (sessions.find((s) => s.id === selectedSessionId) ?? null)
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
        Failed to load sessions: {error.message}
      </div>
    );
  }

  // Empty state
  if (!isLoading && sessions.length === 0) {
    return (
      <div className={styles.emptyState} data-testid="sessions-empty">
        No sessions recorded yet
      </div>
    );
  }

  return (
    <div className={styles.outerContainer} data-testid="sessions-tab">
      <div className={styles.costSummary}>
        <span className={styles.summaryItem}>
          <span className={styles.summaryLabel}>Sessions</span>
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
          <div className={styles.detailEmpty}>
            Select a session to view details
          </div>
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
}

function computeCostSummary(sessions: SessionRecord[]): CostSummary {
  let totalTokens = 0;
  let totalCost = 0;
  let activeSessions = 0;
  for (const s of sessions) {
    totalTokens += s.input_tokens + s.output_tokens;
    totalCost += s.estimated_cost_usd;
    if (s.is_active) activeSessions++;
  }
  return { count: sessions.length, totalTokens, totalCost, activeSessions };
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
