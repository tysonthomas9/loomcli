/**
 * SessionTimeline - Vertical list of session rows, sorted newest-first.
 * Aggregate Runs / Tokens / Cost live in the rail header.
 */

import type { SessionRecord } from "@/types/agent";
import { formatCost, formatTokens } from "@/utils/sessionUsage";

import { SessionTimelineRow, type SessionRowLabel } from "./SessionTimelineRow";
import styles from "./SessionsTab.module.css";

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
  summary: RunRailSummary;
  getRowLabel?: (session: SessionRecord) => SessionRowLabel;
  compactRows?: boolean;
}

export function SessionTimeline({
  sessions,
  selectedId,
  onSelect,
  isLoading,
  summary,
  getRowLabel,
  compactRows = false,
}: SessionTimelineProps): JSX.Element {
  if (isLoading && sessions.length === 0) {
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

  // Sort newest-first by started_at
  const sorted = [...sessions].sort(
    (a, b) =>
      new Date(b.started_at).getTime() - new Date(a.started_at).getTime(),
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
      <div className={styles.timelineList}>
        {sorted.map((session) => (
          <SessionTimelineRow
            key={session.session_id}
            session={session}
            isSelected={selectedId === session.session_id}
            onClick={() => onSelect(session.session_id)}
            {...(getRowLabel ? { label: getRowLabel(session) } : {})}
            compact={compactRows}
          />
        ))}
      </div>
    </div>
  );
}
