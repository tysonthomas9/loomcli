import styles from './EpicProgress.module.css';

export interface EpicProgressProps {
  tasksByEpic: Record<string, number>;
}

export function EpicProgress({ tasksByEpic }: EpicProgressProps): JSX.Element {
  const entries = Object.entries(tasksByEpic ?? {}).sort(([, a], [, b]) => b - a);

  if (entries.length === 0) {
    return <div className={styles.emptyState}>No epic data available</div>;
  }

  const maxCount = Math.max(...entries.map(([, count]) => count), 1);

  return (
    <div className={styles.container}>
      {entries.map(([epicId, count]) => {
        const pct = (count / maxCount) * 100;
        return (
          <div key={epicId} className={styles.row}>
            <span className={styles.epicId} title={epicId}>{epicId}</span>
            <div className={styles.barTrack}>
              <div className={styles.barFill} style={{ width: `${pct}%` }} />
            </div>
            <span className={styles.count}>{count}</span>
          </div>
        );
      })}
    </div>
  );
}
