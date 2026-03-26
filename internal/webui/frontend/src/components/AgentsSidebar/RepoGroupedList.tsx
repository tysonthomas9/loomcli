/**
 * RepoGroupedList renders agents grouped by repo affinity.
 * Used by AgentsSidebar when repo filters are active.
 */

import { useState, useCallback, useEffect } from "react";

import { useWorkspaceContext } from "@/hooks/useWorkspaceContext";
import type { LoomAgentStatus } from "@/types";
import { wsGet, wsSet, getLastWorkspaceId } from "@/utils/scopedStorage";

import { AgentCard } from "../AgentCard";
import type { AgentTaskMap } from "./AgentsSidebar";
import styles from "./AgentsSidebar.module.css";
import { groupAgentsByRepo } from "./AgentsSidebar";

const SK_REPO_GROUPS_COLLAPSED = "agents-sidebar-repo-groups-collapsed";

export interface RepoGroupedListProps {
  agents: LoomAgentStatus[];
  selectedRepos: string[];
  agentTasks: AgentTaskMap;
  onAgentClick?: (agentName: string) => void;
}

export function RepoGroupedList({
  agents,
  selectedRepos,
  agentTasks,
  onAgentClick,
}: RepoGroupedListProps): JSX.Element {
  const { workspaceId } = useWorkspaceContext();

  const [collapsedGroups, setCollapsedGroups] = useState<
    Record<string, boolean>
  >(() => {
    const wsId = getLastWorkspaceId();
    if (!wsId) return {};
    const stored = wsGet(wsId, SK_REPO_GROUPS_COLLAPSED);
    if (!stored) return {};
    try {
      return JSON.parse(stored);
    } catch {
      return {};
    }
  });

  useEffect(() => {
    if (workspaceId)
      wsSet(
        workspaceId,
        SK_REPO_GROUPS_COLLAPSED,
        JSON.stringify(collapsedGroups),
      );
  }, [collapsedGroups, workspaceId]);

  const handleGroupToggle = useCallback((repo: string) => {
    setCollapsedGroups((prev) => ({ ...prev, [repo]: !prev[repo] }));
  }, []);

  const { grouped, other } = groupAgentsByRepo(agents, selectedRepos);

  const renderAgent = (agent: LoomAgentStatus) => (
    <AgentCard
      key={agent.name}
      agent={agent}
      taskTitle={agentTasks[agent.name]?.title}
      {...(onAgentClick !== undefined && {
        onClick: () => onAgentClick(agent.name),
      })}
    />
  );

  return (
    <>
      {Array.from(grouped.entries()).map(([repo, repoAgents]) => (
        <div key={repo} className={styles.repoGroup}>
          <div
            className={styles.repoGroupHeader}
            onClick={() => handleGroupToggle(repo)}
            role="button"
            tabIndex={0}
            onKeyDown={(e) => e.key === "Enter" && handleGroupToggle(repo)}
          >
            <span className={styles.repoGroupToggle}>
              {collapsedGroups[repo] ? ">" : "v"}
            </span>
            <span className={styles.repoGroupName}>{repo}</span>
            <span className={styles.repoGroupCount}>{repoAgents.length}</span>
          </div>
          {!collapsedGroups[repo] && repoAgents.map(renderAgent)}
        </div>
      ))}
      {other.length > 0 && (
        <div className={styles.repoGroup}>
          <div
            className={styles.repoGroupHeader}
            onClick={() => handleGroupToggle("__other__")}
            role="button"
            tabIndex={0}
            onKeyDown={(e) =>
              e.key === "Enter" && handleGroupToggle("__other__")
            }
          >
            <span className={styles.repoGroupToggle}>
              {collapsedGroups["__other__"] ? ">" : "v"}
            </span>
            <span className={styles.repoGroupName}>Other</span>
            <span className={styles.repoGroupCount}>{other.length}</span>
          </div>
          {!collapsedGroups["__other__"] && other.map(renderAgent)}
        </div>
      )}
    </>
  );
}
