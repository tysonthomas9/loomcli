/**
 * Static UI metadata for each onboarding step.
 *
 * Pure data, no React. Joined with server-derived status in
 * useOnboardingStatus to produce the rendered checklist. Strings live
 * here so a future i18n pass has a single migration point.
 */

import type {
  OnboardingAction,
  OnboardingStepId,
} from "@/types/onboarding";

export interface OnboardingStepDefinition {
  id: OnboardingStepId;
  /** Render order, 1-indexed. */
  order: number;
  label: string;
  description: string;
  /** CTA shown when the step is actionable. */
  ctaLabel: string;
  /** Action dispatched through OnboardingActionsContext when CTA fires. */
  action: OnboardingAction;
}

export const ONBOARDING_STEPS: OnboardingStepDefinition[] = [
  {
    id: "workspace-repo",
    order: 1,
    label: "Create a workspace with a repo",
    description:
      "Pick a workspace name and attach a local repo path or git URL.",
    ctaLabel: "Create workspace",
    action: "open_workspace_repo_wizard",
  },
  {
    id: "verify-repo",
    order: 2,
    label: "Verify your repo",
    description: "Loom checks the repo is readable and ready for an agent.",
    ctaLabel: "Open repo checks",
    action: "open_repo_checks",
  },
  {
    id: "setup-backend",
    order: 3,
    label: "Set up an AI backend",
    description: "Install the CLI and authenticate, or set the env var.",
    ctaLabel: "Open backend setup",
    action: "open_backend_setup",
  },
  {
    id: "create-agent",
    order: 4,
    label: "Create an agent",
    description: "Define a role and pick a backend.",
    ctaLabel: "Create agent",
    action: "open_create_agent",
  },
  {
    id: "create-issue",
    order: 5,
    label: "Create your first issue",
    description: "Capture the task you want the agent to take on.",
    ctaLabel: "Create issue",
    action: "open_create_issue",
  },
  {
    id: "run-agent",
    order: 6,
    label: "Run your first agent",
    description: "Start the agent against the issue and watch the run.",
    ctaLabel: "Start agent",
    action: "start_first_agent",
  },
];

const STEP_INDEX: Map<OnboardingStepId, OnboardingStepDefinition> = new Map(
  ONBOARDING_STEPS.map((s) => [s.id, s]),
);

export function getStepDefinition(
  id: OnboardingStepId,
): OnboardingStepDefinition | undefined {
  return STEP_INDEX.get(id);
}
