/**
 * WorkspaceGroupedList renders agents grouped by workspace.
 * Used by AgentsSidebar when multiple workspaces are present.
 */

import { useState, useCallback, useMemo } from "react";

import type { LoomAgentStatus } from "@/types";

import { AgentCard } from "../AgentCard";
import type { AgentTaskMap } from "./AgentsSidebar";
import styles from "./AgentsSidebar.module.css";

const WS_COLLAPSED_STORAGE_KEY = "agents-sidebar-ws-collapsed";

export interface WorkspaceGroupedListProps {
  agents: LoomAgentStatus[];
  agentTasks: AgentTaskMap;
  onAgentClick?: (agentName: string) => void;
}

/**
 * Group agents by workspace, sorted alphabetically with "(default)" last.
 * Returns empty array if all agents share the same workspace (single-workspace optimization).
 */
export function useWorkspaceGroups(
  agents: LoomAgentStatus[],
): [string, LoomAgentStatus[]][] {
  return useMemo(() => {
    const groups = new Map<string, LoomAgentStatus[]>();
    for (const agent of agents) {
      const ws = agent.workspace || "(default)";
      const list = groups.get(ws) || [];
      list.push(agent);
      groups.set(ws, list);
    }
    if (groups.size <= 1) return [];
    return [...groups.entries()].sort(([a], [b]) => {
      if (a === "(default)") return 1;
      if (b === "(default)") return -1;
      return a.localeCompare(b);
    });
  }, [agents]);
}

export function WorkspaceGroupedList({
  agents,
  agentTasks,
  onAgentClick,
}: WorkspaceGroupedListProps): JSX.Element {
  const [collapsedWorkspaces, setCollapsedWorkspaces] = useState<
    Record<string, boolean>
  >(() => {
    try {
      const stored = localStorage.getItem(WS_COLLAPSED_STORAGE_KEY);
      if (!stored) return {};
      const parsed = JSON.parse(stored);
      return parsed && typeof parsed === "object" && !Array.isArray(parsed)
        ? (parsed as Record<string, boolean>)
        : {};
    } catch {
      return {};
    }
  });

  const workspaceGroups = useWorkspaceGroups(agents);

  const toggleWorkspaceCollapse = useCallback((wsName: string) => {
    setCollapsedWorkspaces((prev) => {
      const next = { ...prev, [wsName]: !prev[wsName] };
      try {
        localStorage.setItem(WS_COLLAPSED_STORAGE_KEY, JSON.stringify(next));
      } catch {
        // Ignore localStorage errors
      }
      return next;
    });
  }, []);

  const renderAgent = (agent: LoomAgentStatus) => (
    <AgentCard
      key={agent.name}
      agent={agent}
      taskTitle={agentTasks[agent.name]?.title}
      issueId={agentTasks[agent.name]?.id}
      {...(onAgentClick !== undefined && {
        onClick: () => onAgentClick(agent.name),
      })}
    />
  );

  // Single workspace optimization: render flat list without headers
  if (workspaceGroups.length === 0) {
    return <>{agents.map(renderAgent)}</>;
  }

  return (
    <>
      {workspaceGroups.map(([wsName, wsAgents]) => (
        <div key={wsName} className={styles.workspaceGroup}>
          <button
            type="button"
            className={styles.workspaceHeader}
            onClick={() => toggleWorkspaceCollapse(wsName)}
          >
            <span
              className={styles.workspaceCollapseIcon}
              data-expanded={!collapsedWorkspaces[wsName]}
            >
              ›
            </span>
            <span className={styles.workspaceHeaderText}>{wsName}</span>
            <span className={styles.workspaceCount}>{wsAgents.length}</span>
          </button>
          {!collapsedWorkspaces[wsName] && wsAgents.map(renderAgent)}
        </div>
      ))}
    </>
  );
}
