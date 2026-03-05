import styles from './AgentUtilizationBars.module.css';

export interface AgentUtilizationBarsProps {
  utilization: Record<string, number>;
}

function getLevel(value: number): string {
  if (value >= 0.7) return 'high';
  if (value >= 0.3) return 'medium';
  return 'low';
}

export function AgentUtilizationBars({ utilization }: AgentUtilizationBarsProps): JSX.Element {
  const entries = Object.entries(utilization ?? {}).sort(([a], [b]) => a.localeCompare(b));

  if (entries.length === 0) {
    return <div className={styles.emptyState}>No agent utilization data</div>;
  }

  return (
    <div className={styles.container}>
      {entries.map(([agent, value]) => {
        const pct = Math.min(value * 100, 100);
        return (
          <div key={agent} className={styles.row}>
            <span className={styles.agentName} title={agent}>{agent}</span>
            <div className={styles.barTrack}>
              <div
                className={styles.barFill}
                style={{ width: `${pct}%` }}
                data-level={getLevel(value)}
              />
            </div>
            <span className={styles.percentage}>{pct.toFixed(0)}%</span>
          </div>
        );
      })}
    </div>
  );
}
