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
 *
 * The visual treatment is intentionally editorial: a Fraunces display
 * heading with optical sizing, marginalia step indicators in the
 * gutter, and a manuscript-like rule between rows. See the CSS module
 * for the full system.
 */

import type { CSSProperties } from "react";

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
  if (isLoading && steps.length === 0) return null;
  if (allComplete) return null;

  const subtitle =
    context === "no-workspace"
      ? "A guided path from a fresh install to a running agent — six chapters."
      : "Finish the chapters below to start your first agent in this workspace.";

  const completeCount = steps.filter(
    (s) => s.status === "complete" || s.status === "warning",
  ).length;
  const totalSteps = steps.length;
  const percent =
    totalSteps > 0 ? Math.round((completeCount / totalSteps) * 100) : 0;

  return (
    <section
      className={styles.container}
      role="region"
      aria-label="Onboarding"
      data-testid="onboarding-flow"
    >
      <header className={styles.header}>
        <div>
          <p className={styles.eyebrow}>Loom · First run</p>
          <h2 className={styles.heading}>You&rsquo;re almost ready.</h2>
          <p className={styles.subtitle}>{subtitle}</p>
        </div>
        <div className={styles.progressBlock}>
          <div className={styles.progressNumeric}>
            <span className={styles.progressNumber}>
              {String(completeCount).padStart(2, "0")}
            </span>
            <span className={styles.progressTotal}>
              / {String(totalSteps).padStart(2, "0")}
            </span>
          </div>
          <span className={styles.progressLabel}>Chapters complete</span>
        </div>
      </header>

      <div
        className={styles.progressBar}
        role="progressbar"
        aria-valuenow={completeCount}
        aria-valuemin={0}
        aria-valuemax={totalSteps}
      >
        <div className={styles.progressFill} style={{ width: `${percent}%` }} />
      </div>

      <ol className={styles.list}>
        {steps.map((step, index) => (
          <Row
            key={step.id}
            step={step}
            index={index + 1}
            delay={index * 70}
            onCta={() => dispatch(step.action)}
          />
        ))}
      </ol>

      {error ? (
        <p className={styles.error} role="alert">
          Could not load onboarding status. The checklist may be out of date.
        </p>
      ) : null}

      <footer className={styles.footer}>
        <span className={styles.footerNote}>
          {context === "no-workspace" ? "Local mode · loom serve" : "Workspace setup"}
        </span>
        {workspaceId ? (
          <button
            type="button"
            className={styles.dismiss}
            onClick={dismiss}
            data-testid="onboarding-dismiss"
            aria-label="Dismiss onboarding"
          >
            Hide for this workspace
          </button>
        ) : null}
      </footer>
    </section>
  );
}

interface RowProps {
  step: OnboardingStep;
  index: number;
  delay: number;
  onCta: () => void;
}

function Row({ step, index, delay, onCta }: RowProps): JSX.Element {
  const status = step.status;
  const showCta = canActOnStep(status);
  const isComplete = status === "complete" || status === "warning";

  const style = { "--row-delay": `${delay}ms` } as CSSProperties;

  return (
    <li
      className={`${styles.row} ${styles[status] ?? ""}`}
      aria-current={status === "actionable" ? "step" : undefined}
      data-step-id={step.id}
      data-status={status}
      style={style}
    >
      <span className={styles.indicator} aria-hidden="true">
        {isComplete ? "✓" : romanNumeral(index)}
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

function canActOnStep(status: OnboardingStepStatus): boolean {
  return status !== "complete" && status !== "blocked";
}

const ROMAN: Record<number, string> = {
  1: "I",
  2: "II",
  3: "III",
  4: "IV",
  5: "V",
  6: "VI",
  7: "VII",
  8: "VIII",
  9: "IX",
};
function romanNumeral(n: number): string {
  return ROMAN[n] ?? String(n);
}
