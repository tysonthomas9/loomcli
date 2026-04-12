/**
 * Pure utility for computing workspace/repo health from agent statuses.
 * Used by WorkspaceTree to show colored health dots per repo.
 */

import type { LoomAgentStatus } from "@/types";
import { parseLoomStatus } from "@/types";

export type HealthColor = "green" | "yellow" | "red";

export interface WorkspaceHealthSummary {
  totalAgents: number;
  activeCount: number;
  errorCount: number;
  healthColor: HealthColor;
}

/**
 * Compute health summary for a list of agents belonging to a single repo.
 * Active types: working, planning, dirty, changes.
 * Error type: error.
 * Everything else: idle/healthy.
 */
export function computeRepoHealth(
  agents: LoomAgentStatus[],
): WorkspaceHealthSummary {
  let activeCount = 0;
  let errorCount = 0;

  for (const agent of agents) {
    const parsed = parseLoomStatus(agent.status);
    switch (parsed.type) {
      case "working":
      case "planning":
      case "dirty":
      case "changes":
        activeCount++;
        break;
      case "error":
        errorCount++;
        break;
      // ready, idle, done, review — not active, not error
    }
  }

  let healthColor: HealthColor = "green";
  if (errorCount > 0) {
    healthColor = "red";
  } else if (activeCount > 0) {
    healthColor = "yellow";
  }

  return {
    totalAgents: agents.length,
    activeCount,
    errorCount,
    healthColor,
  };
}

/**
 * Return the worst health color from a set.
 * Priority: red > yellow > green.
 */
export function worstHealthColor(colors: HealthColor[]): HealthColor {
  let worst: HealthColor = "green";
  for (const color of colors) {
    if (color === "red") return "red";
    if (color === "yellow") worst = "yellow";
  }
  return worst;
}
