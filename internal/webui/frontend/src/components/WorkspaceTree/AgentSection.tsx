/**
 * AgentSection displays a flat list of agents in the sidebar.
 * Each agent row: avatar + name + status label on line 1, scope on line 2.
 * Merges fleet (live) agents with workspace config (placeholder) agents.
 */

import { useMemo } from "react";
import { useStore } from "zustand";

import { useAgentStoreInstance, useWorkspaceContext } from "@/hooks";
import type { LoomAgentStatus } from "@/types";
import { parseLoomStatus } from "@/types";
import { getStatusDotColor, getStatusLabel } from "@/components/AgentCard";
import { getAvatarColor, shouldUseWhiteText } from "@/utils/colorUtils";

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
  const agentStore = useAgentStoreInstance();
  const fleetAgents = useStore(agentStore, (s) => s.agents);
  const { agents: workspaceConfigAgents, workspace } = useWorkspaceContext();

  // Merge fleet agents with workspace config agents.
  // Config agents that aren't yet running appear as "configured" placeholders.
  const agents = useMemo<LoomAgentStatus[]>(() => {
    if (workspaceConfigAgents.length === 0) return fleetAgents;
    const fleetNames = new Set(fleetAgents.map((a) => a.name));
    const configPlaceholders: LoomAgentStatus[] = workspaceConfigAgents
      .filter((ca) => !fleetNames.has(ca.name))
      .map((ca) => {
        const entry: LoomAgentStatus = {
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
          const parsed = parseLoomStatus(agent.status);
          const avatarColor = getAvatarColor(agent.name);
          const dotColor = getStatusDotColor(parsed.type);
          const statusLabel = getStatusLabel(parsed);
          const initial = agent.name.charAt(0) || "?";
          const textColor = shouldUseWhiteText(avatarColor)
            ? "#fff"
            : "#1f2937";
          const taskTitle = agentTasks?.[agent.name]?.title;
          const isError = parsed.type === "error";

          const handleClick = onAgentClick
            ? () => onAgentClick(agent.name)
            : undefined;

          return (
            <div
              key={agent.name}
              className={styles.agentRow}
              data-status={parsed.type}
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
              <div className={styles.row}>
                <div className={styles.avatarContainer}>
                  <div
                    className={styles.avatar}
                    style={{ backgroundColor: avatarColor, color: textColor }}
                    aria-hidden="true"
                  >
                    {initial}
                  </div>
                  <span
                    className={styles.statusDot}
                    style={{ backgroundColor: dotColor }}
                    aria-hidden="true"
                  />
                </div>
                <span className={styles.name}>{agent.name}</span>
                <span
                  className={styles.statusLabel}
                  data-error={isError || undefined}
                  title={taskTitle || statusLabel}
                >
                  {statusLabel}
                </span>
              </div>
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
        <button type="button" className={styles.addButton} onClick={onAddClick}>
          + Add agent
        </button>
      )}
    </div>
  );
}
