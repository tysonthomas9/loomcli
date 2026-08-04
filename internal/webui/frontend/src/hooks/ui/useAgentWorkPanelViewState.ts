/**
 * Persists AgentWorkPanel filter/search/collapse state per workspace + agent.
 *
 * State is restored when the selected agent changes; writes are debounced so
 * search keystrokes do not hit localStorage on every keypress.
 */

import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type Dispatch,
  type SetStateAction,
} from "react";

import {
  DEFAULT_AGENT_WORK_PANEL_VIEW_STATE,
  loadAgentWorkPanelView,
  saveAgentWorkPanelView,
  type AgentWorkPanelLeadFilter,
  type AgentWorkPanelStatusFilter,
  type AgentWorkPanelViewState,
} from "@/utils/agentWorkPanelStorage";

const PERSIST_DEBOUNCE_MS = 200;

export interface UseAgentWorkPanelViewStateReturn {
  statusFilter: AgentWorkPanelStatusFilter;
  setStatusFilter: Dispatch<SetStateAction<AgentWorkPanelStatusFilter>>;
  leadFilter: AgentWorkPanelLeadFilter;
  setLeadFilter: Dispatch<SetStateAction<AgentWorkPanelLeadFilter>>;
  taskSearch: string;
  setTaskSearch: Dispatch<SetStateAction<string>>;
  expandedEpics: Record<string, boolean>;
  setExpandedEpics: Dispatch<SetStateAction<Record<string, boolean>>>;
}

export function useAgentWorkPanelViewState(
  workspaceId: string | undefined,
  agentName: string | undefined,
): UseAgentWorkPanelViewStateReturn {
  const [statusFilter, setStatusFilter] = useState<AgentWorkPanelStatusFilter>(
    () =>
      loadAgentWorkPanelView(workspaceId, agentName).statusFilter ??
      DEFAULT_AGENT_WORK_PANEL_VIEW_STATE.statusFilter,
  );
  const [leadFilter, setLeadFilter] = useState<AgentWorkPanelLeadFilter>(
    () =>
      loadAgentWorkPanelView(workspaceId, agentName).leadFilter ??
      DEFAULT_AGENT_WORK_PANEL_VIEW_STATE.leadFilter,
  );
  const [taskSearch, setTaskSearch] = useState(
    () =>
      loadAgentWorkPanelView(workspaceId, agentName).taskSearch ??
      DEFAULT_AGENT_WORK_PANEL_VIEW_STATE.taskSearch,
  );
  const [expandedEpics, setExpandedEpics] = useState<Record<string, boolean>>(
    () =>
      loadAgentWorkPanelView(workspaceId, agentName).expandedEpics ??
      DEFAULT_AGENT_WORK_PANEL_VIEW_STATE.expandedEpics,
  );

  const pendingRef = useRef<{
    ws: string;
    agent: string;
    // This hook owns a subset of the view state (selectedTaskId is persisted
    // separately by AgentsPage); saveAgentWorkPanelView merges partials.
    state: Partial<AgentWorkPanelViewState>;
  } | null>(null);
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const flush = useCallback(() => {
    if (timerRef.current !== null) {
      clearTimeout(timerRef.current);
      timerRef.current = null;
    }
    const pending = pendingRef.current;
    pendingRef.current = null;
    if (pending) {
      saveAgentWorkPanelView(pending.ws, pending.agent, pending.state);
    }
  }, []);

  const schedulePersist = useCallback(
    (state: Partial<AgentWorkPanelViewState>) => {
      if (!workspaceId || !agentName) return;
      pendingRef.current = { ws: workspaceId, agent: agentName, state };
      if (timerRef.current === null) {
        timerRef.current = setTimeout(() => {
          timerRef.current = null;
          const pending = pendingRef.current;
          pendingRef.current = null;
          if (pending) {
            saveAgentWorkPanelView(pending.ws, pending.agent, pending.state);
          }
        }, PERSIST_DEBOUNCE_MS);
      }
    },
    [workspaceId, agentName],
  );

  useEffect(() => {
    const loaded = loadAgentWorkPanelView(workspaceId, agentName);
    setStatusFilter(loaded.statusFilter);
    setLeadFilter(loaded.leadFilter);
    setTaskSearch(loaded.taskSearch);
    setExpandedEpics(loaded.expandedEpics);
    return flush;
  }, [workspaceId, agentName, flush]);

  useEffect(() => {
    schedulePersist({
      statusFilter,
      leadFilter,
      taskSearch,
      expandedEpics,
    });
  }, [statusFilter, leadFilter, taskSearch, expandedEpics, schedulePersist]);

  useEffect(() => flush, [flush]);

  return {
    statusFilter,
    setStatusFilter,
    leadFilter,
    setLeadFilter,
    taskSearch,
    setTaskSearch,
    expandedEpics,
    setExpandedEpics,
  };
}
