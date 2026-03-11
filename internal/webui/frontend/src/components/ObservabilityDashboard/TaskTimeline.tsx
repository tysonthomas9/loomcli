import type { HourlyBucket } from "@/types";

import styles from "./TaskTimeline.module.css";

export interface TaskTimelineProps {
  hourlyCompletions: HourlyBucket[];
}

function formatHourLabel(hour: string): string {
  const date = new Date(hour);
  if (isNaN(date.getTime())) return "";
  return date.toLocaleTimeString([], { hour: "2-digit", hour12: false });
}

export function TaskTimeline({
  hourlyCompletions,
}: TaskTimelineProps): JSX.Element {
  const hasData =
    hourlyCompletions.length > 0 &&
    hourlyCompletions.some((b) => b.completed > 0 || b.failed > 0);

  if (!hasData) {
    return (
      <div className={styles.emptyState}>
        No task completions in the last 24 hours
      </div>
    );
  }

  const maxValue = Math.max(
    ...hourlyCompletions.map((b) => b.completed + b.failed),
    1,
  );

  return (
    <div className={styles.timeline}>
      {hourlyCompletions.map((bucket, i) => {
        const completedPct = (bucket.completed / maxValue) * 100;
        const failedPct = (bucket.failed / maxValue) * 100;
        const showLabel = i % 3 === 0;

        return (
          <div key={bucket.hour} className={styles.column}>
            <div className={styles.bar}>
              {bucket.failed > 0 && (
                <div
                  className={styles.barFailed}
                  style={{ height: `${failedPct}%` }}
                  title={`${bucket.failed} failed`}
                />
              )}
              {bucket.completed > 0 && (
                <div
                  className={styles.barCompleted}
                  style={{ height: `${completedPct}%` }}
                  title={`${bucket.completed} completed`}
                />
              )}
            </div>
            <span className={styles.hourLabel}>
              {showLabel ? formatHourLabel(bucket.hour) : ""}
            </span>
          </div>
        );
      })}
    </div>
  );
}
