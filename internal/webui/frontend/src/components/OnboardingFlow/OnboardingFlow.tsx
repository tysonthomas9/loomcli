import type { ReactNode } from "react";

import styles from "./OnboardingFlow.module.css";

export type OnboardingStepStatus =
  | "complete"
  | "current"
  | "actionable"
  | "blocked";

export interface OnboardingStep {
  id: string;
  title: string;
  description: string;
  status: OnboardingStepStatus;
  actionLabel?: string;
  onAction?: () => void;
  detail?: ReactNode;
}

export interface OnboardingFlowProps {
  title?: string;
  subtitle?: string;
  steps: OnboardingStep[];
  className?: string;
  variant?: "page" | "panel";
  onDismiss?: () => void;
  dismissLabel?: string;
}

const STATUS_LABELS: Record<OnboardingStepStatus, string> = {
  complete: "Done",
  current: "Next",
  actionable: "Setup",
  blocked: "Locked",
};

function stepClassName(status: OnboardingStepStatus): string {
  return `${styles.step} ${styles[status]}`;
}

export function OnboardingFlow({
  title = "Set up Loom",
  subtitle = "Follow the fixed first-run flow to create a workspace, attach a repo, configure the CLI, and start useful agent work.",
  steps,
  className,
  variant = "page",
  onDismiss,
  dismissLabel = "Dismiss",
}: OnboardingFlowProps): JSX.Element {
  const completedCount = steps.filter((step) => step.status === "complete")
    .length;
  const rootClassName = [
    styles.onboardingFlow,
    variant === "panel" ? styles.panel : "",
    className ?? "",
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <section
      className={rootClassName}
      aria-labelledby="onboarding-flow-title"
      data-testid="onboarding-flow"
    >
      <div className={styles.header}>
        <div>
          <p className={styles.kicker}>First run</p>
          <h2 id="onboarding-flow-title" className={styles.title}>
            {title}
          </h2>
          <p className={styles.subtitle}>{subtitle}</p>
        </div>
        <div className={styles.headerAside}>
          <div className={styles.progress} aria-label="Onboarding progress">
            <strong>
              {completedCount}/{steps.length}
            </strong>
            <span>complete</span>
          </div>
          {onDismiss && (
            <button
              type="button"
              className={styles.dismissButton}
              onClick={onDismiss}
            >
              {dismissLabel}
            </button>
          )}
        </div>
      </div>

      <ol className={styles.steps}>
        {steps.map((step, index) => {
          const isBlocked = step.status === "blocked";
          const hasAction =
            step.status !== "complete" && step.actionLabel && step.onAction;

          return (
            <li key={step.id} className={stepClassName(step.status)}>
              <div className={styles.stepIndex} aria-hidden="true">
                {index + 1}
              </div>
              <div className={styles.stepBody}>
                <div className={styles.stepTitleRow}>
                  <h3 className={styles.stepTitle}>{step.title}</h3>
                  <span className={styles.statusBadge}>
                    {STATUS_LABELS[step.status]}
                  </span>
                </div>
                <p className={styles.stepDescription}>{step.description}</p>
                {step.detail && (
                  <div className={styles.stepDetail}>{step.detail}</div>
                )}
              </div>
              {hasAction && (
                <button
                  type="button"
                  className={styles.stepAction}
                  onClick={step.onAction}
                  disabled={isBlocked}
                >
                  {step.actionLabel}
                </button>
              )}
            </li>
          );
        })}
      </ol>
    </section>
  );
}
