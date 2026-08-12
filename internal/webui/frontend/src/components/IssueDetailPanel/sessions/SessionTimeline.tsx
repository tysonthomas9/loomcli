/**
 * SessionTimeline - Vertical list of session rows, sorted newest-first.
 * Aggregate Runs / Tokens / Cost live in the rail header.
 */

import type { SessionRecord } from "@/types/agent";
import type { TaskWorkflowRun } from "@/api/workflows";
import { formatCost, formatTokens } from "@/utils/sessionUsage";

import { SessionTimelineRow } from "./SessionTimelineRow";
import { WorkflowRunTimelineRow } from "./WorkflowRunTimelineRow";
import styles from "@/styles/SessionRunDetail.module.css";

export interface RunRailSummary {
  count: number;
  totalTokens: number;
  totalCost: number;
  activeSessions: number;
  failedSessions: number;
}

export interface SessionTimelineProps {
  sessions: SessionRecord[];
  selectedId: string | null;
  onSelect: (id: string) => void;
  isLoading: boolean;
  workflowRuns?: TaskWorkflowRun[];
  selectedWorkflowRunId?: string | null;
  onSelectWorkflowRun?: (id: string) => void;
  summary: RunRailSummary;
}

export function SessionTimeline({
  sessions,
  selectedId,
  onSelect,
  isLoading,
  workflowRuns = [],
  selectedWorkflowRunId = null,
  onSelectWorkflowRun,
  summary,
}: SessionTimelineProps): JSX.Element {
  if (isLoading && sessions.length === 0 && workflowRuns.length === 0) {
    return (
      <div className={styles.timeline}>
        <div className={styles.timelineHeader}>
          <span className={styles.timelineTitle}>Runs</span>
        </div>
        <div className={styles.timelineSkeleton}>
          <div className={styles.skeletonRow} />
          <div className={styles.skeletonRow} />
          <div className={styles.skeletonRow} />
        </div>
      </div>
    );
  }

  const sorted = [
    ...sessions.map((session) => ({
      kind: "session" as const,
      id: session.session_id,
      timestamp: session.started_at,
      session,
    })),
    ...workflowRuns.map((run) => ({
      kind: "workflow" as const,
      id: run.run_id,
      timestamp: run.started_at || run.created_at,
      run,
    })),
  ].sort(
    (a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime(),
  );

  const summaryParts = [
    String(summary.count),
    `${formatTokens(summary.totalTokens)} tok`,
  ];
  if (summary.totalCost > 0) {
    summaryParts.push(formatCost(summary.totalCost));
  }
  if (summary.activeSessions > 0) {
    summaryParts.push(`${summary.activeSessions} active`);
  }
  if (summary.failedSessions > 0) {
    summaryParts.push(`${summary.failedSessions} failed`);
  }

  return (
    <div className={styles.timeline} data-testid="session-timeline">
      <div className={styles.timelineHeader} data-testid="timeline-summary">
        <span className={styles.timelineTitle}>Runs</span>
        <span className={styles.timelineSummary}>
          {summaryParts.map((part, i) => (
            <span key={part}>
              {i > 0 && <span aria-hidden="true"> · </span>}
              <span className={styles.summaryValue}>{part}</span>
            </span>
          ))}
        </span>
      </div>
      {sorted.map((item) =>
        item.kind === "session" ? (
          <SessionTimelineRow
            key={`session:${item.id}`}
            session={item.session}
            isSelected={selectedId === item.id}
            onClick={() => onSelect(item.id)}
          />
        ) : (
          <WorkflowRunTimelineRow
            key={`workflow:${item.id}`}
            run={item.run}
            isSelected={selectedWorkflowRunId === item.id}
            onClick={() => onSelectWorkflowRun?.(item.id)}
          />
        ),
      )}
    </div>
  );
}
