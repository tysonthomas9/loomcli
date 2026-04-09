/**
 * AgentSection displays a flat list of agents in the sidebar.
 * Each agent shows name + status on line 1, repo · branch on line 2.
 * Merges fleet (live) agents with workspace config (placeholder) agents.
 */

import { useMemo } from "react";

import { AgentCard } from "@/components/AgentCard";
import { useAgentContext, useWorkspaceContext } from "@/hooks";

import styles from "./AgentSection.module.css";

export interface AgentSectionProps {
  onAgentClick?: ((agentName: string) => void) | undefined;
  agentTasks?: Record<string, { title: string }> | undefined;
  onAddClick?: (() => void) | undefined;
}

export function AgentSection({
  onAgentClick,
  agentTasks,
  onAddClick,
}: AgentSectionProps): JSX.Element {
  const { agents: fleetAgents } = useAgentContext();
  const { agents: workspaceConfigAgents, workspace } = useWorkspaceContext();

  // Merge fleet agents with workspace config agents.
  // Config agents that aren't yet running appear as "configured" placeholders.
  const agents = useMemo(() => {
    if (workspaceConfigAgents.length === 0) return fleetAgents;
    const fleetNames = new Set(fleetAgents.map((a) => a.name));
    const configPlaceholders: typeof fleetAgents = workspaceConfigAgents
      .filter((ca) => !fleetNames.has(ca.name))
      .map((ca) => {
        const entry: (typeof fleetAgents)[number] = {
          name: ca.name,
          branch: "",
          status: "configured",
          ahead: 0,
          behind: 0,
          workspace: workspace?.name ?? "",
          cross_repo: ca.cross_repo,
        };
        if (ca.repos?.[0]) entry.repo = ca.repos[0];
        return entry;
      });
    return [...fleetAgents, ...configPlaceholders];
  }, [fleetAgents, workspaceConfigAgents, workspace?.name]);

  return (
    <div className={styles.section}>
      <div className={styles.header}>Agents</div>
      <div className={styles.list}>
        {agents.map((agent) => {
          const handleClick = onAgentClick
            ? () => onAgentClick(agent.name)
            : undefined;
          return (
            <div
              key={agent.name}
              className={styles.agentRow}
              onClick={handleClick}
              role={handleClick ? "button" : undefined}
              tabIndex={handleClick ? 0 : undefined}
              aria-label={handleClick ? `Agent: ${agent.name}` : undefined}
              onKeyDown={
                handleClick
                  ? (e) => {
                      if (e.key === "Enter" || e.key === " ") {
                        e.preventDefault();
                        handleClick();
                      }
                    }
                  : undefined
              }
            >
              <AgentCard
                agent={agent}
                showRepoBadge={false}
                taskTitle={agentTasks?.[agent.name]?.title}
              />
              <div className={styles.scopeLine}>
                {agent.cross_repo ? (
                  <span className={styles.scopeLabel}>workspace</span>
                ) : agent.repo ? (
                  <span className={styles.scopeLabel}>
                    {agent.repo}
                    {agent.branch ? (
                      <>
                        {" "}
                        <span className={styles.scopeBranch}>
                          &middot; {agent.branch}
                        </span>
                      </>
                    ) : null}
                  </span>
                ) : null}
              </div>
            </div>
          );
        })}
      </div>
      {onAddClick && (
        <button
          type="button"
          className={styles.addButton}
          onClick={onAddClick}
        >
          + Add agent
        </button>
      )}
    </div>
  );
}
