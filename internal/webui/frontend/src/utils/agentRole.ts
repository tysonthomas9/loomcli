/**
 * Agent role helpers shared by the board, work panel, and detail panel.
 */

import type { LoomAgentStatus } from "@/types";
import type {
  BuiltInTeamTemplate,
  TeamTemplateApplyReport,
  TeamTemplateBreadcrumb,
} from "@/types/teamTemplate";
import {
  BUILT_IN_TEAM_TEMPLATES,
  builtInTeamTemplateById,
} from "@/utils/teamTemplates";

/** True when the role string denotes a lead/orchestrator agent. */
export function isLeadRole(role: string | undefined): boolean {
  const normalized = (role ?? "").trim().toLowerCase();
  return normalized === "lead" || normalized === "orchestrator";
}

export function isInteractiveAgent(
  agent: Pick<LoomAgentStatus, "role" | "role_kind">,
): boolean {
  const kind = (agent.role_kind ?? "").trim().toLowerCase();
  if (kind !== "") return kind === "interactive";
  return isLeadRole(agent.role);
}

const BACKGROUND_ROLE_NAMES = new Set([
  "plan",
  "planner",
  "task",
  "coder",
  "worker",
]);

/** True when the role string denotes a supervised plan/task worker. */
export function isWorkerRole(role: string | undefined): boolean {
  return BACKGROUND_ROLE_NAMES.has((role ?? "").trim().toLowerCase());
}

/**
 * Background agents are daemon-supervised auto workers (plan/task). Lead agents
 * stay in the regular section because they run interactively in a terminal.
 */
export function isBackgroundAgent(agent: LoomAgentStatus): boolean {
  if (isInteractiveAgent(agent)) return false;
  if (agent.daemon_managed === true) return true;
  return isWorkerRole(agent.role);
}

export function splitAgentsByRuntime(agents: LoomAgentStatus[]): {
  regular: LoomAgentStatus[];
  background: LoomAgentStatus[];
} {
  const regular: LoomAgentStatus[] = [];
  const background: LoomAgentStatus[] = [];
  for (const agent of agents) {
    (isBackgroundAgent(agent) ? background : regular).push(agent);
  }
  return { regular, background };
}

export function agentRailRank(agent: LoomAgentStatus): number {
  if (isInteractiveAgent(agent)) return 0;
  if (agent.orchestrator_session_id || agent.parent) return 1;
  return 2;
}

/** Sort agents lead-first, then epic-scoped workers, then the rest. */
export function orderAgentsForEpicRunner(
  agents: LoomAgentStatus[],
): LoomAgentStatus[] {
  return [...agents].sort((a, b) => {
    const aRank = agentRailRank(a);
    const bRank = agentRailRank(b);
    if (aRank !== bRank) return aRank - bRank;
    const aParent = a.parent ?? "";
    const bParent = b.parent ?? "";
    if (aParent !== bParent) return aParent.localeCompare(bParent);
    return a.name.localeCompare(b.name);
  });
}

/**
 * Build a map of epic id → lead agent name for every lead that has claimed
 * an epic (lead.parent === epicId). Used to surface "who is running this
 * epic" on epic surfaces (swim-lane headers, epic detail, open queue).
 */
export function buildEpicLeadClaims(
  agents: LoomAgentStatus[],
): Map<string, string> {
  const claims = new Map<string, string>();
  for (const agent of agents) {
    if (!agent || !isLeadRole(agent.role)) continue;
    if (!agent.parent) continue;
    claims.set(agent.parent, agent.name);
  }
  return claims;
}

export interface DetectTeamTemplateInput {
  roleNames: readonly string[];
  applyReport?: TeamTemplateApplyReport | null;
  breadcrumb?: TeamTemplateBreadcrumb | null;
}

/**
 * Detect the best-matching built-in team from live configured worker agent
 * role names, OR from an authoritative apply report in this browser session.
 * The breadcrumb can choose between multiple live matches, but never creates a
 * match on its own.
 */
export function detectTeamTemplate({
  roleNames,
  applyReport,
  breadcrumb,
}: DetectTeamTemplateInput): BuiltInTeamTemplate | null {
  if (applyReport) {
    return builtInTeamTemplateById(applyReport.template_id) ?? null;
  }

  const liveRoleNames = new Set(roleNames.filter(Boolean));
  const matches = BUILT_IN_TEAM_TEMPLATES.filter((teamTemplate) =>
    teamTemplate.roles
      .filter((agentRole) => agentRole.kind === "worker")
      .every((agentRole) => liveRoleNames.has(agentRole.name)),
  );
  if (matches.length === 0) return null;
  if (matches.length === 1) return matches[0] ?? null;

  if (breadcrumb) {
    const breadcrumbMatch = matches.find(
      (teamTemplate) => teamTemplate.id === breadcrumb.templateId,
    );
    if (breadcrumbMatch) return breadcrumbMatch;
  }
  return matches[0] ?? null;
}

export function findTemplateArchitectAgentName(input: {
  teamTemplateId: string | undefined;
  agents: readonly { name: string; role_name?: string }[];
  applyReport?: TeamTemplateApplyReport | null;
}): string | undefined {
  if (!input.teamTemplateId) return undefined;
  const teamTemplate = builtInTeamTemplateById(input.teamTemplateId);
  if (!teamTemplate) return undefined;

  const liveArchitect = input.agents.find(
    (agent) => agent.role_name === teamTemplate.architectRoleName,
  );
  if (liveArchitect) return liveArchitect.name;

  if (input.applyReport?.template_id !== teamTemplate.id) return undefined;
  const architectAgentName = teamTemplate.agents.find(
    (agent) => agent.role_name === teamTemplate.architectRoleName,
  )?.name;
  if (!architectAgentName) return undefined;
  const reportStep = input.applyReport.steps.find(
    (step) => step.entity === "agent" && step.name === architectAgentName,
  );
  return reportStep && reportStep.action !== "failed"
    ? architectAgentName
    : undefined;
}
