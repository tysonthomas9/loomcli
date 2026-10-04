// The sidebar's Agent API rows (SB2): which children show under their parent,
// and the second line and status dot each row shows.

import type { Agent } from "@/api/agentsv1";

/** A child at work, as the Lead chat's tray counts it (DF1). */
const WORKING = new Set(["creating", "active", "waiting", "stopping"]);

/**
 * Whether a child row shows under its parent: while it is at work, while its
 * chat is open, or while one of its own children shows. Finished, idle and
 * archived children leave the sidebar; the Lead's chat and tray still reach
 * them. Top-level rows (Leads, Background workers) do not use this rule.
 */
export function childVisible(
  a: Agent,
  kids: ReadonlyMap<string, readonly Agent[]>,
  openId: string | undefined,
): boolean {
  if (a.deleted_at) return false;
  if (a.agent_id === openId || WORKING.has(a.state)) return true;
  return (kids.get(a.agent_id) ?? []).some((k) =>
    childVisible(k, kids, openId),
  );
}

/** The row's second line: what kind of agent it is. */
export function agentRoleLabel(a: Agent): string {
  if (a.preset === "lead") return "Lead";
  if (a.preset?.includes("review")) return "Reviewer";
  if (a.role_kind === "worker") return "Worker";
  return "Agent";
}

export type AgentDot = "working" | "waiting" | "done" | "failed" | "idle";

/** The status dot's kind for an agent's state. */
export function agentDot(a: Agent): AgentDot {
  switch (a.state) {
    case "creating":
    case "active":
    case "stopping":
      return "working";
    case "waiting":
      return "waiting";
    case "finished":
      return !a.outcome || a.outcome === "completed" ? "done" : "failed";
    default:
      return "idle";
  }
}
