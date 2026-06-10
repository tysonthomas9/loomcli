/**
 * Agent role helpers shared by the board, work panel, and detail panel.
 */

import type { LoomAgentStatus } from "@/types";

/** True when the role string denotes a lead/orchestrator agent. */
export function isLeadRole(role: string | undefined): boolean {
  const normalized = (role ?? "").trim().toLowerCase();
  return normalized === "lead" || normalized === "orchestrator";
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
