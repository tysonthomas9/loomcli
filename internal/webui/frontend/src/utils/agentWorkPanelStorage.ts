/**
 * Persistence helpers for AgentWorkPanel filter/search/collapse state.
 * Scoped per workspace and agent via scopedStorage.
 */

import { wsGet, wsSet } from "@/utils/scopedStorage";
import type { StatusBucket } from "@/utils/statusBuckets";

export type AgentWorkPanelStatusFilter = "all" | StatusBucket;
export type AgentWorkPanelLeadFilter = "all" | "running" | "idle";

export interface AgentWorkPanelViewState {
  statusFilter: AgentWorkPanelStatusFilter;
  leadFilter: AgentWorkPanelLeadFilter;
  taskSearch: string;
  expandedEpics: Record<string, boolean>;
  /** Inline task detail selection on /agents (AgentsPage). */
  selectedTaskId: string | null;
}

export const DEFAULT_AGENT_WORK_PANEL_VIEW_STATE: AgentWorkPanelViewState = {
  statusFilter: "all",
  leadFilter: "all",
  taskSearch: "",
  expandedEpics: {},
  selectedTaskId: null,
};

const STATUS_FILTERS = new Set<string>([
  "all",
  "in_progress",
  "open",
  "review",
  "blocked",
  "done",
]);

const LEAD_FILTERS = new Set<string>(["all", "running", "idle"]);

function scopedViewKey(agentName: string): string {
  return `agent-work-panel-view:${agentName}`;
}

function parseExpandedEpics(value: unknown): Record<string, boolean> {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return {};
  }
  const expanded: Record<string, boolean> = {};
  for (const [key, entry] of Object.entries(value)) {
    if (typeof key === "string" && typeof entry === "boolean") {
      expanded[key] = entry;
    }
  }
  return expanded;
}

function parseViewState(raw: unknown): AgentWorkPanelViewState | null {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return null;
  }
  const record = raw as Record<string, unknown>;
  const statusFilter = record.statusFilter;
  const leadFilter = record.leadFilter;
  const taskSearch = record.taskSearch;
  if (
    typeof statusFilter !== "string" ||
    !STATUS_FILTERS.has(statusFilter) ||
    typeof leadFilter !== "string" ||
    !LEAD_FILTERS.has(leadFilter) ||
    typeof taskSearch !== "string"
  ) {
    return null;
  }
  const selectedTaskId = record.selectedTaskId;
  if (
    selectedTaskId !== undefined &&
    selectedTaskId !== null &&
    typeof selectedTaskId !== "string"
  ) {
    return null;
  }

  return {
    statusFilter: statusFilter as AgentWorkPanelStatusFilter,
    leadFilter: leadFilter as AgentWorkPanelLeadFilter,
    taskSearch,
    expandedEpics: parseExpandedEpics(record.expandedEpics),
    selectedTaskId: typeof selectedTaskId === "string" ? selectedTaskId : null,
  };
}

/** Load persisted view state for an agent, falling back to defaults. */
export function loadAgentWorkPanelView(
  wsId: string | null | undefined,
  agentName: string | null | undefined,
): AgentWorkPanelViewState {
  if (!wsId || !agentName) {
    return DEFAULT_AGENT_WORK_PANEL_VIEW_STATE;
  }
  try {
    const stored = wsGet(wsId, scopedViewKey(agentName));
    if (!stored) {
      return DEFAULT_AGENT_WORK_PANEL_VIEW_STATE;
    }
    const parsed: unknown = JSON.parse(stored);
    return parseViewState(parsed) ?? DEFAULT_AGENT_WORK_PANEL_VIEW_STATE;
  } catch {
    return DEFAULT_AGENT_WORK_PANEL_VIEW_STATE;
  }
}

/** Save view state for an agent, merging with any existing persisted fields. */
export function saveAgentWorkPanelView(
  wsId: string | null | undefined,
  agentName: string | null | undefined,
  state: Partial<AgentWorkPanelViewState>,
): void {
  if (!wsId || !agentName) return;
  const merged = { ...loadAgentWorkPanelView(wsId, agentName), ...state };
  wsSet(wsId, scopedViewKey(agentName), JSON.stringify(merged));
}
