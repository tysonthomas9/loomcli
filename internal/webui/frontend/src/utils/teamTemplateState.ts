import type {
  TeamTemplateApplyReport,
  TeamTemplateBreadcrumb,
} from "@/types/teamTemplate";
import { wsGet, wsSet } from "@/utils/scopedStorage";

export const TEAM_TEMPLATE_APPLIED_KEY = "team-template-applied";

export type TeamSetupStepStatus =
  | "complete"
  | "current"
  | "pending"
  | "blocked";

export function deriveTeamSetupStepStatus(input: {
  isApplying: boolean;
  hasWorkspaceAgent: boolean;
  hasWorkspaceRepo: boolean;
  isDefaultBackendReady: boolean;
}): TeamSetupStepStatus {
  if (input.isApplying) return "pending";
  if (input.hasWorkspaceAgent) return "complete";
  if (input.hasWorkspaceRepo && input.isDefaultBackendReady) return "current";
  return "blocked";
}

export function deriveFirstIssueStepStatus(input: {
  isRunning: boolean;
  hasWorkspaceIssue: boolean;
  hasOnboardingPlanner: boolean;
  hasTemplateArchitect: boolean;
  isDefaultBackendReady: boolean;
}): TeamSetupStepStatus {
  if (input.isRunning) return "pending";
  if (input.hasWorkspaceIssue) return "complete";
  if (
    (input.hasOnboardingPlanner || input.hasTemplateArchitect) &&
    input.isDefaultBackendReady
  ) {
    return "current";
  }
  return "blocked";
}

function isCount(value: unknown): value is number {
  return typeof value === "number" && Number.isInteger(value) && value >= 0;
}

function isTeamTemplateBreadcrumb(
  value: unknown,
): value is TeamTemplateBreadcrumb {
  if (!value || typeof value !== "object") return false;
  const candidate = value as Partial<TeamTemplateBreadcrumb>;
  const counts = candidate.counts;
  return (
    typeof candidate.templateId === "string" &&
    candidate.templateId.length > 0 &&
    isCount(candidate.revision) &&
    typeof candidate.ts === "number" &&
    Number.isFinite(candidate.ts) &&
    Boolean(counts) &&
    isCount(counts?.created) &&
    isCount(counts?.skipped) &&
    isCount(counts?.diverged) &&
    isCount(counts?.failed)
  );
}

export function readTeamTemplateBreadcrumb(
  workspaceId: string,
): TeamTemplateBreadcrumb | null {
  const raw = wsGet(workspaceId, TEAM_TEMPLATE_APPLIED_KEY);
  if (!raw) return null;
  try {
    const parsed: unknown = JSON.parse(raw);
    return isTeamTemplateBreadcrumb(parsed) ? parsed : null;
  } catch {
    return null;
  }
}

export function writeTeamTemplateBreadcrumb(
  workspaceId: string,
  breadcrumb: TeamTemplateBreadcrumb,
): void {
  wsSet(workspaceId, TEAM_TEMPLATE_APPLIED_KEY, JSON.stringify(breadcrumb));
}

export function teamTemplateBreadcrumbFromReport(
  report: TeamTemplateApplyReport,
  ts = Date.now(),
): TeamTemplateBreadcrumb {
  return {
    templateId: report.template_id,
    revision: report.revision,
    ts,
    counts: {
      created: report.created,
      skipped: report.skipped,
      diverged: report.diverged,
      failed: report.failed,
    },
  };
}
