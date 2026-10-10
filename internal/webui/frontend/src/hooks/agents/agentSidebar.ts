// The sidebar's Agent API rows (SB2): which children show under their parent,
// and the second line and status dot each row shows.

import type { Agent } from "@/api/agentsv1";
import {
  mergeAgentSectionOrder,
  parseStoredAgentSectionOrder,
} from "@/utils/agentSectionOrder";
import { wsGet } from "@/utils/scopedStorage";
import { childrenByParent } from "./agentRoster";
import type { Roster } from "./agentRoster";

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

/** Whether the agent is at work, as the Lead chat's tray counts it (DF1). */
export const agentAtWork = (a: Agent): boolean => WORKING.has(a.state);

/** The top-level rows' order by agent id, saved as the fleet rows' is. */
export const SK_AGENT_API_ORDER = "agent-api-order";

/** The workspace's saved top-level row order (SB4). */
export const storedAgentApiOrder = (workspaceId: string): string[] =>
  parseStoredAgentSectionOrder(wsGet(workspaceId, SK_AGENT_API_ORDER)) ?? [];

export interface SidebarRows {
  /** Each shown parent's children, archived and gone ones left out. */
  kids: ReadonlyMap<string, readonly Agent[]>;
  /** Every top-level row's id, in the saved order. */
  fullOrder: string[];
  /** Top-level rows: Leads and other agents, then Background workers. */
  main: Agent[];
  background: Agent[];
}

/**
 * The sidebar's top-level Agent API rows (SB2/SB4): Leads first, then other
 * agents, then workers, in the saved order; archived and gone agents leave.
 * A child whose parent is hidden (archived, deleted, or not listed, as List
 * leaves archived agents out) rises to the top only while it shows.
 */
export function sidebarRows(
  roster: Roster,
  openId: string | undefined,
  order: readonly string[],
  gone: ReadonlySet<string> = new Set(),
): SidebarRows {
  const kids = childrenByParent(
    new Map(
      [...roster].filter(([id, a]) => a.state !== "archived" && !gone.has(id)),
    ),
  );
  const top = (kids.get("") ?? []).filter(
    (a) => !a.parent_agent_id || childVisible(a, kids, openId),
  );
  const isWorker = (a: Agent) => a.role_kind === "worker";
  const isLead = (a: Agent) => a.preset === "lead";
  const fullOrder = mergeAgentSectionOrder(
    [
      ...top.filter(isLead),
      ...top.filter((a) => !isLead(a) && !isWorker(a)),
      ...top.filter(isWorker),
    ].map((a) => a.agent_id),
    order,
  );
  const ordered = fullOrder.map((id) => roster.get(id)!);
  return {
    kids,
    fullOrder,
    main: ordered.filter((a) => !isWorker(a)),
    background: ordered.filter(isWorker),
  };
}

/** A row's children that show under it (SB2), oldest first. */
export const visibleChildren = (
  a: Agent,
  kids: ReadonlyMap<string, readonly Agent[]>,
  openId: string | undefined,
): Agent[] =>
  (kids.get(a.agent_id) ?? []).filter((k) => childVisible(k, kids, openId));

/**
 * Every Agent API row the sidebar shows, flattened in its order: each
 * top-level row followed by its shown children, then the Background rows.
 */
export function sidebarAgents(
  roster: Roster,
  openId: string | undefined,
  order: readonly string[],
): Agent[] {
  const { kids, main, background } = sidebarRows(roster, openId, order);
  const out: Agent[] = [];
  const walk = (a: Agent) => {
    out.push(a);
    visibleChildren(a, kids, openId).forEach(walk);
  };
  [...main, ...background].forEach(walk);
  return out;
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
