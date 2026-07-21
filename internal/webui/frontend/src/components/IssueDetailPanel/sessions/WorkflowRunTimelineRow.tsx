import type { TaskWorkflowRun } from "@/api/workflows";

import styles from "./SessionsTab.module.css";

export interface WorkflowRunTimelineRowProps {
  run: TaskWorkflowRun;
  isSelected: boolean;
  onClick: () => void;
}

function statusLabel(status: string): string {
  return status
    .split("_")
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

function durationSeconds(run: TaskWorkflowRun): number | null {
  const start = new Date(run.started_at || run.created_at).getTime();
  const endValue =
    run.finished_at ||
    (run.status === "completed" ||
    run.status === "failed" ||
    run.status === "cancelled" ||
    run.status === "needs_review"
      ? run.updated_at
      : undefined);
  if (!endValue) return null;
  const end = new Date(endValue).getTime();
  if (!Number.isFinite(start) || !Number.isFinite(end) || end < start) {
    return null;
  }
  return (end - start) / 1000;
}

function formatDuration(seconds: number | null): string {
  if (seconds == null) return "--";
  if (seconds < 60) return `${Math.max(0, Math.round(seconds))}s`;
  const minutes = Math.floor(seconds / 60);
  return `${minutes}m ${Math.round(seconds % 60)}s`;
}

export function WorkflowRunTimelineRow({
  run,
  isSelected,
  onClick,
}: WorkflowRunTimelineRowProps): JSX.Element {
  const label = statusLabel(run.status);
  const explanation = run.summary || run.error_class || run.output?.blocker;
  return (
    <div
      className={`${styles.row} ${isSelected ? styles.selected : ""}`}
      onClick={onClick}
      role="button"
      tabIndex={0}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          onClick();
        }
      }}
      aria-label={`Automation run, ${label}${explanation ? `, ${explanation}` : ""}`}
      data-testid={`workflow-run-row-${run.run_id}`}
    >
      <span
        className={styles.statusDot}
        data-status={run.status}
        aria-label={run.status}
      />
      <div className={styles.rowMain}>
        <div className={styles.rowTop}>
          <span className={styles.agentName}>Automation</span>
          <span className={styles.backendBadge}>workflow</span>
          <span className={styles.statusLabel} data-status={run.status}>
            {label}
          </span>
        </div>
        {explanation && (
          <div className={styles.workflowSummary}>{explanation}</div>
        )}
        <div className={styles.rowBottom}>
          <span className={styles.duration}>
            {formatDuration(durationSeconds(run))}
          </span>
          <span className={styles.runIdentifier} title={run.run_id}>
            {run.run_id}
          </span>
        </div>
      </div>
    </div>
  );
}
