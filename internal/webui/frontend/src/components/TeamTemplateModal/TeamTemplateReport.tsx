import type {
  TeamTemplate,
  TeamTemplateApplyReport,
  TeamTemplateStepResult,
} from "@/types/teamTemplate";

import styles from "./TeamTemplateModal.module.css";

function entityLabel(step: TeamTemplateStepResult): string {
  return step.entity === "role" ? "agent role" : "agent";
}

function stepSummary(step: TeamTemplateStepResult): string {
  switch (step.action) {
    case "created":
      return step.entity === "agent"
        ? "created · configured to run"
        : "created";
    case "skipped_match":
      return "already exists — matches the template";
    case "skipped_diverged":
      return "already exists with different settings — kept yours";
    case "failed":
      return `failed: ${step.error || "unknown error"}`;
  }
}

function StepRow({ step }: { step: TeamTemplateStepResult }): JSX.Element {
  const routingFields = new Set(["labels", "exclude_labels", "task_filter"]);
  return (
    <li className={styles.reportRow} data-action={step.action}>
      <span className={styles.reportIcon} aria-hidden="true">
        {step.action === "failed"
          ? "×"
          : step.action === "skipped_diverged"
            ? "!"
            : step.action === "skipped_match"
              ? "○"
              : "✓"}
      </span>
      <span className={styles.reportEntity}>{entityLabel(step)}</span>
      <span className={styles.reportResult}>
        <strong>{step.name}</strong>
        <span>{stepSummary(step)}</span>
        {step.action === "skipped_diverged" && step.fields?.length ? (
          <ul
            className={styles.fieldList}
            aria-label={`${step.name} differences`}
          >
            {step.fields.map((field) => (
              <li key={field}>
                <code>{field}</code>
                {routingFields.has(field) ? " (routing)" : ""} — kept yours
              </li>
            ))}
          </ul>
        ) : null}
      </span>
    </li>
  );
}

function StepList({
  steps,
}: {
  steps: readonly TeamTemplateStepResult[];
}): JSX.Element {
  return (
    <ul className={styles.reportList}>
      {steps.map((step) => (
        <StepRow key={`${step.entity}:${step.name}`} step={step} />
      ))}
    </ul>
  );
}

export interface TeamTemplateReportProps {
  teamTemplate: TeamTemplate;
  report: TeamTemplateApplyReport;
  retryFocusNames?: ReadonlySet<string>;
}

export function TeamTemplateReport({
  teamTemplate,
  report,
  retryFocusNames,
}: TeamTemplateReportProps): JSX.Element {
  const matching = Math.max(0, report.skipped - report.diverged);
  const hasProblems = report.diverged > 0 || report.failed > 0;
  const focusSteps = retryFocusNames
    ? report.steps.filter((step) => retryFocusNames.has(step.name))
    : report.steps;
  const alreadySetUpSteps = retryFocusNames
    ? report.steps.filter((step) => !retryFocusNames.has(step.name))
    : [];

  return (
    <div className={styles.report}>
      <div className={styles.resultHeading}>
        <h3>
          {report.failed > 0
            ? "Team setup needs attention"
            : "Your team is set up"}
          {" — "}
          {report.created} created, {report.skipped} already existed
        </h3>
        <p>
          Applied {teamTemplate.label} · revision {report.revision}
        </p>
      </div>

      {report.warnings?.length ? (
        <div className={styles.warningBlock} role="status">
          <strong>Before you continue</strong>
          <ul>
            {report.warnings.map((warning) => (
              <li key={warning}>{warning}</li>
            ))}
          </ul>
        </div>
      ) : null}

      <details className={styles.reportDetails} open={hasProblems}>
        <summary>
          {report.steps.length} steps · {report.skipped} already existed
          {report.diverged > 0
            ? ` (${report.diverged} differ${report.diverged === 1 ? "s" : ""} from the template)`
            : matching > 0
              ? ` (${matching} match${matching === 1 ? "es" : ""})`
              : ""}
        </summary>
        {retryFocusNames && alreadySetUpSteps.length > 0 ? (
          <details className={styles.retrySuccesses}>
            <summary>{alreadySetUpSteps.length} already set up</summary>
            <StepList steps={alreadySetUpSteps} />
          </details>
        ) : null}
        <StepList steps={focusSteps} />
      </details>

      {report.failed > 0 ? (
        <p className={styles.retryGuidance}>
          {report.failed} step{report.failed === 1 ? "" : "s"} failed.
          Re-applying only creates what&apos;s missing and never overwrites your
          settings.
        </p>
      ) : (
        <p className={styles.successGuidance}>
          Your team is configured to run — agents start picking up matching
          issues once the daemon reconciles. Create the first issue next.
        </p>
      )}
    </div>
  );
}
