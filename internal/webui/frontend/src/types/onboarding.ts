/**
 * Domain types for the web onboarding flow.
 *
 * Wire format mirrors GET /api/onboarding/status (see
 * docs/product/web-onboarding-spec.md and
 * internal/webui/handlers/onboarding/onboarding.go).
 */

export type OnboardingStepId =
  | "workspace-repo"
  | "verify-repo"
  | "setup-backend"
  | "create-agent"
  | "create-issue"
  | "run-agent";

export type OnboardingStepStatus =
  | "complete"
  | "actionable"
  | "blocked"
  | "warning"
  | "error"
  | "unknown";

/**
 * Action names map to OnboardingActionsContext on the frontend. Kept in
 * sync with the Go ActionOpen* constants in
 * internal/webui/handlers/onboarding/onboarding.go.
 */
export type OnboardingAction =
  | "open_workspace_repo_wizard"
  | "open_repo_checks"
  | "open_backend_setup"
  | "open_create_agent"
  | "open_create_issue"
  | "start_first_agent";

/** A single step as returned by the server. */
export interface OnboardingStepWire {
  id: OnboardingStepId;
  status: OnboardingStepStatus;
  action: OnboardingAction;
  message?: string;
}

/** The wire-format status response. */
export interface OnboardingStatusWire {
  workspace_id?: string;
  active_repo?: string;
  steps: OnboardingStepWire[];
  all_complete: boolean;
}

/**
 * Returns true when a step's status unblocks downstream rendering. Per
 * the spec, only `complete` and `warning` qualify.
 */
export function unblocksDownstream(status: OnboardingStepStatus): boolean {
  return status === "complete" || status === "warning";
}
