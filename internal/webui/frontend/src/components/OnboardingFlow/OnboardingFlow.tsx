/**
 * OnboardingFlow — visible checklist of onboarding steps.
 *
 * Used in two surfaces, controlled by the `context` prop:
 *   - "no-workspace" (rendered by RedirectToWorkspace at "/")
 *   - "empty-kanban" (rendered by EmptyWorkspaceBoard inside a workspace)
 *
 * Step status comes from useOnboardingStatus. CTA clicks dispatch
 * through OnboardingActionsContext so the flow component never knows
 * which component owns each side effect.
 */

import { useOnboardingActions } from "@/contexts/OnboardingActionsContext";
import {
  useOnboardingStatus,
  type OnboardingStep,
} from "@/hooks/onboarding";
import { type OnboardingStepStatus } from "@/types/onboarding";

import styles from "./OnboardingFlow.module.css";

export type OnboardingFlowContext = "no-workspace" | "empty-kanban";

interface OnboardingFlowProps {
  context: OnboardingFlowContext;
  workspaceId?: string;
}

export function OnboardingFlow({
  context,
  workspaceId,
}: OnboardingFlowProps): JSX.Element | null {
  const { dispatch } = useOnboardingActions();
  const {
    steps,
    allComplete,
    isLoading,
    error,
    isDismissed,
    dismiss,
  } = useOnboardingStatus(workspaceId);

  if (isDismissed) return null;
  // While loading the very first time we render nothing rather than a
  // spinner — the surface we're on (no-workspace screen, empty board)
  // already has its own placeholder. Subsequent refetches keep prior
  // steps visible so the UI doesn't flicker.
  if (isLoading && steps.length === 0) return null;
  if (allComplete) return null;

  const subtitle =
    context === "no-workspace"
      ? "Get from zero to your first agent run."
      : "Finish setup to run your first agent in this workspace.";

  const completeCount = steps.filter(
    (s) => s.status === "complete" || s.status === "warning",
  ).length;
  const totalSteps = steps.length;
  const percent = totalSteps > 0 ? Math.round((completeCount / totalSteps) * 100) : 0;

  return (
    <section
      className={styles.container}
      role="region"
      aria-label="Onboarding"
      data-testid="onboarding-flow"
    >
      <header className={styles.header}>
        <div className={styles.headerText}>
          <h2 className={styles.title}>Get started</h2>
          <p className={styles.subtitle}>{subtitle}</p>
        </div>
        <div
          className={styles.progress}
          aria-label={`${completeCount} of ${totalSteps} steps complete`}
        >
          <span className={styles.progressCount}>
            {completeCount}/{totalSteps}
          </span>
          <div
            className={styles.progressBar}
            role="progressbar"
            aria-valuenow={completeCount}
            aria-valuemin={0}
            aria-valuemax={totalSteps}
          >
            <div
              className={styles.progressFill}
              style={{ width: `${percent}%` }}
            />
          </div>
        </div>
      </header>

      {error ? (
        <p className={styles.error} role="alert">
          Could not load onboarding status. The checklist may be out of date.
        </p>
      ) : null}

      <ol className={styles.list}>
        {steps.map((step, index) => (
          <Row
            key={step.id}
            step={step}
            index={index + 1}
            onCta={() => dispatch(step.action)}
          />
        ))}
      </ol>

      {workspaceId ? (
        <div className={styles.footer}>
          <button
            type="button"
            className={styles.dismiss}
            onClick={dismiss}
            data-testid="onboarding-dismiss"
            aria-label="Dismiss onboarding"
          >
            Hide checklist
          </button>
        </div>
      ) : null}
    </section>
  );
}

interface RowProps {
  step: OnboardingStep;
  index: number;
  onCta: () => void;
}

function Row({ step, index, onCta }: RowProps): JSX.Element {
  const status = step.status;
  const showCta = canActOnStep(status);
  const isComplete = status === "complete" || status === "warning";

  return (
    <li
      className={`${styles.row} ${styles[status] ?? ""}`}
      aria-current={status === "actionable" ? "step" : undefined}
      data-step-id={step.id}
      data-status={status}
    >
      <span
        className={`${styles.indicator} ${styles[status] ?? ""}`}
        aria-hidden="true"
      >
        {isComplete ? "✓" : index}
      </span>
      <span className={styles.label}>
        <span className={styles.labelText}>{step.label}</span>
        <span className={styles.message}>
          {step.message ?? step.description}
        </span>
      </span>
      {showCta ? (
        <button
          type="button"
          className={styles.cta}
          onClick={onCta}
          data-testid={`onboarding-cta-${step.id}`}
        >
          {step.ctaLabel}
        </button>
      ) : status === "complete" ? (
        <span
          className={styles.ctaDone}
          data-testid={`onboarding-cta-${step.id}`}
          aria-label="Done"
        >
          Done
        </span>
      ) : (
        <span className={styles.ctaPlaceholder} aria-hidden="true" />
      )}
    </li>
  );
}

/**
 * A step accepts a click for any non-terminal, non-prerequisite-missing
 * status. The dispatched handler decides what to surface (e.g. open
 * repo checks for warning, retry for error).
 */
function canActOnStep(status: OnboardingStepStatus): boolean {
  return status !== "complete" && status !== "blocked";
}
