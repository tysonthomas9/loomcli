import styles from "./ErrorIndicators.module.css";

export interface ErrorIndicatorsProps {
  errorRatePct: number;
  restartCount24h: number;
  restartsByAgent: Record<string, number>;
}

function getSeverity(errorRate: number): string {
  if (errorRate <= 5) return "ok";
  if (errorRate <= 15) return "warning";
  return "critical";
}

export function ErrorIndicators({
  errorRatePct,
  restartCount24h,
  restartsByAgent,
}: ErrorIndicatorsProps): JSX.Element {
  const agentEntries = Object.entries(restartsByAgent ?? {})
    .filter(([, count]) => count > 0)
    .sort(([, a], [, b]) => b - a);

  const hasIssues = errorRatePct > 0 || restartCount24h > 0;

  if (!hasIssues) {
    return (
      <div className={styles.emptyState}>
        No errors or restarts in the last 24 hours
      </div>
    );
  }

  return (
    <div className={styles.container}>
      <div className={styles.summary}>
        <span
          className={styles.badge}
          data-severity={getSeverity(errorRatePct)}
        >
          {errorRatePct.toFixed(1)}% error rate
        </span>
        <span
          className={styles.badge}
          data-severity={restartCount24h > 0 ? "warning" : "ok"}
        >
          {restartCount24h} restart{restartCount24h !== 1 ? "s" : ""} (24h)
        </span>
      </div>
      {agentEntries.length > 0 && (
        <div className={styles.agentList}>
          {agentEntries.map(([agent, count]) => (
            <div key={agent} className={styles.agentRow}>
              <span className={styles.agentName} title={agent}>
                {agent}
              </span>
              <span className={styles.restartCount}>{count}</span>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
