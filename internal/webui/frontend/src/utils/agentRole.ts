/**
 * Agent role helpers shared by the board, work panel, and detail panel.
 */

import type { LoomAgentStatus } from "@/types";

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
 * A custom role is any workspace role that is neither a builtin lead/orchestrator
 * nor a builtin plan/task worker. Only custom roles expose an editable
 * prompt/config surface and may be deleted — the backend refuses to delete the
 * builtin plan/task roles. Used to gate the Phase B agent-config affordance.
 */
export function isCustomRole(role: string | undefined): boolean {
  const normalized = (role ?? "").trim();
  if (normalized === "") return false;
  return !isLeadRole(normalized) && !isWorkerRole(normalized);
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
