/**
 * EpicProgress component for swim lane headers.
 * Displays a compact progress bar with completion count and optional "Ready to close" badge.
 */

import styles from './EpicProgress.module.css';

export interface EpicProgressProps {
  /** Total number of child issues */
  totalChildren: number;
  /** Number of closed child issues */
  closedChildren: number;
  /** Whether the epic is eligible for closing */
  eligibleForClose: boolean;
}

/**
 * Renders a compact progress bar showing epic completion status.
 * Used inline within swim lane headers.
 */
export function EpicProgress({
  totalChildren,
  closedChildren,
  eligibleForClose,
}: EpicProgressProps): JSX.Element {
  const percent = totalChildren > 0 ? Math.round((closedChildren / totalChildren) * 100) : 0;

  return (
    <span className={styles.epicProgress}>
      <span
        className={styles.progressBar}
        role="progressbar"
        aria-valuenow={percent}
        aria-valuemin={0}
        aria-valuemax={100}
        aria-label={`Epic completion: ${closedChildren} of ${totalChildren} done`}
      >
        <span className={styles.progressFill} style={{ width: `${percent}%` }} />
      </span>
      <span className={styles.progressLabel}>
        {closedChildren}/{totalChildren}
      </span>
      {eligibleForClose && (
        <span className={styles.readyBadge} aria-label="Ready to close">
          Ready
        </span>
      )}
    </span>
  );
}
