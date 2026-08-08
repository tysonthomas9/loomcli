import type { TaskWorkflowRun } from "@/api/workflows";

import styles from "@/styles/SessionRunDetail.module.css";

export interface WorkflowRunDetailProps {
  run: TaskWorkflowRun;
}

function label(value: string): string {
  return value
    .split("_")
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

function outputValue(value: string): string {
  return /^[a-z0-9]+(?:_[a-z0-9]+)+$/.test(value) ? label(value) : value;
}

function isActiveStatus(status: string): boolean {
  return (
    status === "queued" ||
    status === "running" ||
    status === "suspended_awaiting_event"
  );
}

function fallbackExplanation(status: string, errorClass?: string): string {
  if (errorClass) return `Automation stopped with ${errorClass}.`;
  switch (status) {
    case "queued":
      return "The automation is queued and has not started an agent session yet.";
    case "running":
      return "The automation is running and has not started an agent session yet.";
    case "suspended_awaiting_event":
      return "The automation is waiting for an event and has not started an agent session yet.";
    case "failed":
      return "The automation failed before an agent session became available.";
    case "cancelled":
      return "The automation was cancelled before an agent session became available.";
    case "needs_review":
      return "The automation needs review and has no agent session available.";
    default:
      return "The automation completed before an agent session became available.";
  }
}

export function WorkflowRunDetail({
  run,
}: WorkflowRunDetailProps): JSX.Element {
  const output = Object.entries(run.output ?? {}).sort(([left], [right]) =>
    left.localeCompare(right),
  );
  const active = isActiveStatus(run.status);
  const explanation =
    run.summary || fallbackExplanation(run.status, run.error_class);

  return (
    <div className={styles.workflowDetail} data-testid="workflow-run-detail">
      <header className={styles.workflowDetailHeader}>
        <div className={styles.workflowDetailTitleRow}>
          <div className={styles.workflowDetailIdentity}>
            <div className={styles.workflowDetailTitle}>Automation run</div>
            <div className={styles.workflowDetailSubtitle} title={run.run_id}>
              {run.run_id}
            </div>
          </div>
          <span className={styles.workflowStatusBadge} data-status={run.status}>
            {label(run.status)}
          </span>
        </div>
        <div
          className={styles.workflowTranscriptState}
          data-state={active ? "pending" : "absent"}
          data-testid="workflow-transcript-state"
        >
          <div className={styles.workflowTranscriptLabel}>Transcript</div>
          <div className={styles.workflowTranscriptTitle}>
            {active ? "Waiting for an agent session" : "No transcript created"}
          </div>
          <div className={styles.workflowTranscriptDescription}>
            {active
              ? "This workflow has not started an agent session yet. This view will update if it does."
              : "This workflow finished without starting an agent session, so no agent transcript or diff exists."}
          </div>
        </div>
        <div className={styles.workflowExplanation}>
          <div className={styles.workflowExplanationLabel}>Outcome</div>
          <div>{explanation}</div>
        </div>
        {run.error_class && (
          <div className={styles.workflowError} role="alert">
            {run.error_class}
          </div>
        )}
      </header>

      <div className={styles.workflowDetailBody}>
        <dl className={styles.workflowMetadata}>
          <div>
            <dt>Workflow</dt>
            <dd>{run.driver_id}</dd>
          </div>
          <div>
            <dt>Trigger event</dt>
            <dd>{run.source_ref || "—"}</dd>
          </div>
          <div>
            <dt>Started</dt>
            <dd>{run.started_at || run.created_at}</dd>
          </div>
          {run.finished_at && (
            <div>
              <dt>Finished</dt>
              <dd>{run.finished_at}</dd>
            </div>
          )}
        </dl>

        {output.length > 0 && (
          <section
            className={styles.workflowOutput}
            data-testid="workflow-run-output"
          >
            <h4>Result details</h4>
            <dl>
              {output.map(([key, value]) => (
                <div key={key}>
                  <dt>{label(key)}</dt>
                  <dd title={value}>{outputValue(value)}</dd>
                </div>
              ))}
            </dl>
          </section>
        )}
      </div>
    </div>
  );
}
