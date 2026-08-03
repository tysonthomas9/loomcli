import type { WorkspaceAgentInfo } from "@/api/workspace";
import type { LoomAgentStatus } from "@/types";

import { orderAgentsForEpicRunner } from "./agentRole";

/**
 * Merge live monitor rows with workspace-configured agents.
 *
 * Workspace agents remain selectable before their runtime has produced a live
 * monitor row. Keep this resolution shared between the sidebar and the
 * full agent page so a route cannot be visible in one surface and unresolved
 * in the other.
 */
export function mergeAgentRoster(
  fleetAgents: LoomAgentStatus[],
  workspaceAgents: WorkspaceAgentInfo[],
  workspaceName = "",
): LoomAgentStatus[] {
  const orderedFleetAgents = orderAgentsForEpicRunner(fleetAgents);
  if (workspaceAgents.length === 0) return orderedFleetAgents;

  const fleetNames = new Set(orderedFleetAgents.map((agent) => agent.name));
  const configuredAgents: LoomAgentStatus[] = workspaceAgents
    .filter((agent) => !fleetNames.has(agent.name))
    .map((agent) => {
      const configured: LoomAgentStatus = {
        name: agent.name,
        branch: "",
        status: "configured",
        ahead: 0,
        behind: 0,
        workspace: workspaceName,
        cross_repo: agent.cross_repo,
      };
      if (agent.repos?.[0]) configured.repo = agent.repos[0];
      if (agent.role_name) configured.role = agent.role_name;
      return configured;
    });

  return [...orderedFleetAgents, ...configuredAgents];
}
