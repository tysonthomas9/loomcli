import type { LoomAgentStatus } from "@/types";

export const SK_AGENT_SECTION_ORDER = "agent-section-order";

/** Merge stored order with the current agent set, dropping stale names. */
export function mergeAgentSectionOrder(
  agentNames: readonly string[],
  storedOrder: readonly string[] | null | undefined,
): string[] {
  const known = new Set(agentNames);
  const ordered: string[] = [];

  if (storedOrder) {
    for (const name of storedOrder) {
      if (known.has(name)) {
        ordered.push(name);
        known.delete(name);
      }
    }
  }

  for (const name of agentNames) {
    if (known.has(name)) ordered.push(name);
  }

  return ordered;
}

/** Sort agents by a name order list; unknown agents append in source order. */
export function applyAgentSectionOrder(
  agents: readonly LoomAgentStatus[],
  order: readonly string[],
): LoomAgentStatus[] {
  if (order.length === 0) return [...agents];

  const byName = new Map(agents.map((agent) => [agent.name, agent]));
  const ordered: LoomAgentStatus[] = [];
  const seen = new Set<string>();

  for (const name of order) {
    const agent = byName.get(name);
    if (!agent) continue;
    ordered.push(agent);
    seen.add(name);
  }

  for (const agent of agents) {
    if (!seen.has(agent.name)) ordered.push(agent);
  }

  return ordered;
}

/** Reorder one contiguous group inside a full order list. */
export function reorderAgentGroup(
  fullOrder: readonly string[],
  groupNames: readonly string[],
  activeId: string,
  overId: string,
  move: (items: string[], from: number, to: number) => string[],
): string[] {
  const oldIndex = groupNames.indexOf(activeId);
  const newIndex = groupNames.indexOf(overId);
  if (oldIndex < 0 || newIndex < 0 || oldIndex === newIndex) {
    return [...fullOrder];
  }

  const reorderedGroup = move([...groupNames], oldIndex, newIndex);
  const groupSet = new Set(groupNames);
  let groupIdx = 0;

  return fullOrder.map((name) =>
    groupSet.has(name) ? reorderedGroup[groupIdx++]! : name,
  );
}

export function parseStoredAgentSectionOrder(
  raw: string | null,
): string[] | null {
  if (!raw) return null;
  try {
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return null;
    return parsed.filter((item): item is string => typeof item === "string");
  } catch {
    return null;
  }
}
