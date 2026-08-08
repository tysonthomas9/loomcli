/**
 * ObservabilityDashboard container component.
 * Fetches metrics via useObservabilityMetrics and renders 5 presentational panels.
 */

import { ErrorDisplay } from "@/components/ErrorDisplay";
import { LoadingSkeleton } from "@/components/LoadingSkeleton";
import { useParams } from "react-router-dom";

import { useObservabilityMetrics } from "../useObservabilityMetrics";
import { AgentUtilizationBars } from "./AgentUtilizationBars";
import { EpicProgress } from "./EpicProgress";
import { ErrorIndicators } from "./ErrorIndicators";
import { MetricsCards } from "./MetricsCards";
import styles from "./ObservabilityDashboard.module.css";
import { TaskTimeline } from "./TaskTimeline";

export interface ObservabilityDashboardProps {
  className?: string;
}

export function ObservabilityDashboard({
  className,
}: ObservabilityDashboardProps): JSX.Element {
  const { workspaceId } = useParams<{ workspaceId: string }>();
  const { metrics, isLoading, error, isConnected, lastUpdated, refetch } =
    useObservabilityMetrics({
      pollInterval: 30000,
      ...(workspaceId ? { workspaceId } : {}),
    });

  const rootClassName = [styles.dashboard, className].filter(Boolean).join(" ");

  if (isLoading && !metrics) {
    return (
      <div className={rootClassName}>
        <LoadingSkeleton.Observability />
      </div>
    );
  }

  if (error && !metrics) {
    const is503 = error.message.includes("503");
    return (
      <div className={rootClassName}>
        {is503 ? (
          <ErrorDisplay
            variant="custom"
            title="Observability not configured"
            description="Events will appear once the observability system is initialized."
          />
        ) : (
          <ErrorDisplay
            variant="fetch-error"
            error={error}
            showDetails
            onRetry={() => void refetch()}
          />
        )}
      </div>
    );
  }

  const m = metrics;

  return (
    <div className={rootClassName}>
      {!isConnected && m && (
        <div className={styles.staleIndicator}>
          Data may be stale
          {lastUpdated
            ? ` (last updated ${lastUpdated.toLocaleTimeString()})`
            : ""}
          <button className={styles.retryButton} onClick={() => void refetch()}>
            Retry
          </button>
        </div>
      )}

      <MetricsCards
        tasksPerHour={m?.tasks_completed_last_hour ?? 0}
        avgDurationSec={m?.avg_task_duration_sec ?? 0}
        linesPerHour={m?.lines_changed_last_hour ?? 0}
        errorRatePct={m?.error_rate_pct ?? 0}
      />

      <section className={styles.panel} aria-label="Task Timeline">
        <div className={styles.panelHeader}>
          <h3 className={styles.panelTitle}>Hourly Completions (24h)</h3>
          {lastUpdated && (
            <span className={styles.refreshIndicator}>
              {lastUpdated.toLocaleTimeString()}
            </span>
          )}
        </div>
        <div className={styles.panelContent}>
          <TaskTimeline hourlyCompletions={m?.hourly_completions ?? []} />
        </div>
      </section>

      <div className={styles.twoColumnRow}>
        <section className={styles.panel} aria-label="Agent Utilization">
          <div className={styles.panelHeader}>
            <h3 className={styles.panelTitle}>Agent Utilization</h3>
          </div>
          <div className={styles.panelContent}>
            <AgentUtilizationBars utilization={m?.agent_utilization ?? {}} />
          </div>
        </section>

        <section className={styles.panel} aria-label="Errors & Restarts">
          <div className={styles.panelHeader}>
            <h3 className={styles.panelTitle}>Errors & Restarts</h3>
          </div>
          <div className={styles.panelContent}>
            <ErrorIndicators
              errorRatePct={m?.error_rate_pct ?? 0}
              restartCount24h={m?.restart_count_24h ?? 0}
              restartsByAgent={m?.restarts_by_agent ?? {}}
            />
          </div>
        </section>
      </div>

      <section className={styles.panel} aria-label="Epic Progress">
        <div className={styles.panelHeader}>
          <h3 className={styles.panelTitle}>Epic Progress</h3>
        </div>
        <div className={styles.panelContent}>
          <EpicProgress tasksByEpic={m?.tasks_by_epic ?? {}} />
        </div>
      </section>
    </div>
  );
}
