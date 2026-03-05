import styles from './MetricsCards.module.css';

export interface MetricsCardsProps {
  tasksPerHour: number;
  avgDurationSec: number;
  linesPerHour: number;
  errorRatePct: number;
}

function formatDuration(seconds: number): string {
  if (seconds < 60) return `${Math.round(seconds)}s`;
  const mins = Math.floor(seconds / 60);
  const secs = Math.round(seconds % 60);
  return secs > 0 ? `${mins}m ${secs}s` : `${mins}m`;
}

export function MetricsCards({
  tasksPerHour,
  avgDurationSec,
  linesPerHour,
  errorRatePct,
}: MetricsCardsProps): JSX.Element {
  return (
    <div className={styles.cards}>
      <div className={styles.card}>
        <span className={styles.cardValue}>{tasksPerHour}</span>
        <span className={styles.cardLabel}>Tasks / Hour</span>
      </div>
      <div className={styles.card}>
        <span className={styles.cardValue}>{formatDuration(avgDurationSec)}</span>
        <span className={styles.cardLabel}>Avg Duration</span>
      </div>
      <div className={styles.card}>
        <span className={styles.cardValue}>{linesPerHour}</span>
        <span className={styles.cardLabel}>Lines / Hour</span>
      </div>
      <div className={styles.card}>
        <span className={`${styles.cardValue} ${errorRatePct > 10 ? styles.error : ''}`}>
          {errorRatePct.toFixed(1)}%
        </span>
        <span className={styles.cardLabel}>Error Rate</span>
      </div>
    </div>
  );
}
