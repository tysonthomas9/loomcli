/**
 * SessionTimeline - Vertical list of session rows, sorted newest-first.
 */

import type { SessionRecord } from "@/types/agent";

import { SessionTimelineRow } from "./SessionTimelineRow";
import styles from "./SessionsTab.module.css";

export interface SessionTimelineProps {
  sessions: SessionRecord[];
  selectedId: string | null;
  onSelect: (id: string) => void;
  isLoading: boolean;
}

export function SessionTimeline({
  sessions,
  selectedId,
  onSelect,
  isLoading,
}: SessionTimelineProps): JSX.Element {
  if (isLoading && sessions.length === 0) {
    return (
      <div className={styles.timeline}>
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

  return (
    <div className={styles.timeline} data-testid="session-timeline">
      {sorted.map((session) => (
        <SessionTimelineRow
          key={session.session_id}
          session={session}
          isSelected={selectedId === session.session_id}
          onClick={() => onSelect(session.session_id)}
        />
      ))}
    </div>
  );
}
