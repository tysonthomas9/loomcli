/**
 * QueueStatsBar displays a compact single-line summary of issue queue counts.
 * Replaces the expanded WorkQueueSection grid in the redesigned sidebar.
 */

import styles from "./QueueStatsBar.module.css";

/** Counts for each Work Queue category. */
export interface WorkQueueCounts {
  backlog: number;
  open: number;
  blocked: number;
  inProgress: number;
  needsReview: number;
  done: number;
}

export interface QueueStatsBarProps {
  counts: WorkQueueCounts;
}

export function QueueStatsBar({ counts }: QueueStatsBarProps): JSX.Element {
  const queued = counts.backlog + counts.open;
  return (
    <div className={styles.bar}>
      <span className={styles.stat}>
        Queued <span className={styles.count}>{queued}</span>
      </span>
      <span className={styles.separator}>&middot;</span>
      <span className={styles.stat}>
        Done <span className={styles.count}>{counts.done}</span>
      </span>
      <span className={styles.separator}>&middot;</span>
      <span className={styles.stat}>
        Blocked <span className={styles.count}>{counts.blocked}</span>
      </span>
    </div>
  );
}
